package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/mell/bridge/internal/protocol"
)

// Server proxies byte streams between devices. Plain TCP; daemons run TLS on top.

type waitConn struct {
	conn net.Conn
	done chan struct{}
}

type Server struct {
	Token string
	mu    sync.Mutex
	wait  map[string]*waitConn
}

func NewServer(token string) *Server {
	return &Server{Token: token, wait: map[string]*waitConn{}}
}

func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("bridge relay listening on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	typ, body, err := protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return
	}
	switch typ {
	case protocol.TypeRelayHello:
		hello, err := protocol.Decode[protocol.RelayHello](body)
		if err != nil {
			conn.Close()
			return
		}
		if s.Token != "" && hello.Token != s.Token {
			_ = protocol.WriteMessage(conn, protocol.TypeError, protocol.ErrorBody{Message: "unauthorized"})
			conn.Close()
			return
		}
		done := make(chan struct{})
		s.register(hello.DeviceID, conn, done)
		<-done
	case protocol.TypeRelayConnect:
		var auth struct {
			TargetID string `json:"target_id"`
			Token    string `json:"token"`
			DeviceID string `json:"device_id"`
		}
		if err := json.Unmarshal(body, &auth); err != nil {
			conn.Close()
			return
		}
		if s.Token != "" && auth.Token != s.Token {
			_ = protocol.WriteMessage(conn, protocol.TypeError, protocol.ErrorBody{Message: "unauthorized"})
			conn.Close()
			return
		}
		target := s.take(auth.TargetID)
		if target == nil {
			_ = protocol.WriteMessage(conn, protocol.TypeError, protocol.ErrorBody{Message: "peer not connected to relay"})
			conn.Close()
			return
		}
		_ = protocol.WriteMessage(conn, protocol.TypeRelayReady, map[string]string{"status": "ok"})
		_ = protocol.WriteMessage(target.conn, protocol.TypeRelayReady, map[string]string{"status": "incoming"})
		splice(conn, target.conn)
		close(target.done)
	default:
		_ = protocol.WriteMessage(conn, protocol.TypeError, protocol.ErrorBody{Message: "unknown message"})
		conn.Close()
	}
}

func (s *Server) register(deviceID string, conn net.Conn, done chan struct{}) {
	s.mu.Lock()
	if old, ok := s.wait[deviceID]; ok {
		old.conn.Close()
		close(old.done)
	}
	s.wait[deviceID] = &waitConn{conn: conn, done: done}
	s.mu.Unlock()
	log.Printf("relay: device %s registered", deviceID)
}

func (s *Server) take(deviceID string) *waitConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.wait[deviceID]
	delete(s.wait, deviceID)
	return c
}

func splice(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
}

type Client struct {
	URL      string // host:port
	Token    string
	DeviceID string
}

func (c *Client) Dial(targetID string) (net.Conn, error) {
	conn, err := net.Dial("tcp", c.URL)
	if err != nil {
		return nil, err
	}
	body := map[string]string{
		"target_id": targetID,
		"token":     c.Token,
		"device_id": c.DeviceID,
	}
	if err := protocol.WriteMessage(conn, protocol.TypeRelayConnect, body); err != nil {
		conn.Close()
		return nil, err
	}
	typ, raw, err := protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if typ == protocol.TypeError {
		eb, _ := protocol.Decode[protocol.ErrorBody](raw)
		conn.Close()
		return nil, fmt.Errorf("relay: %s", eb.Message)
	}
	if typ != protocol.TypeRelayReady {
		conn.Close()
		return nil, fmt.Errorf("unexpected relay response %s", typ)
	}
	return conn, nil
}

// AcceptLoop registers on the relay and invokes onIncoming for each spliced session.
func (c *Client) AcceptLoop(stop <-chan struct{}, onIncoming func(net.Conn)) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		conn, err := net.Dial("tcp", c.URL)
		if err != nil {
			select {
			case <-stop:
				return
			default:
			}
			continue
		}
		if err := protocol.WriteMessage(conn, protocol.TypeRelayHello, protocol.RelayHello{
			DeviceID: c.DeviceID,
			Token:    c.Token,
		}); err != nil {
			conn.Close()
			continue
		}
		typ, _, err := protocol.ReadMessage(conn)
		if err != nil {
			conn.Close()
			select {
			case <-stop:
				return
			default:
			}
			continue
		}
		if typ != protocol.TypeRelayReady {
			conn.Close()
			continue
		}
		onIncoming(conn)
	}
}
