package relay

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/mell/bridge/internal/protocol"
)

func TestRelaySplice(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	s := NewServer("tok")
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	defer ln.Close()

	waiterDone := make(chan net.Conn, 1)
	go func() {
		c := &Client{URL: addr, Token: "tok", DeviceID: "device-b"}
		stop := make(chan struct{})
		go func() {
			time.Sleep(3 * time.Second)
			close(stop)
		}()
		c.AcceptLoop(stop, func(conn net.Conn) {
			waiterDone <- conn
		})
	}()

	time.Sleep(200 * time.Millisecond)
	dialer := &Client{URL: addr, Token: "tok", DeviceID: "device-a"}
	conn, err := dialer.Dial("device-b")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	select {
	case peer := <-waiterDone:
		defer peer.Close()
		go func() {
			_, _ = io.WriteString(conn, "hello-relay")
			conn.Close()
		}()
		buf := make([]byte, 64)
		n, err := peer.Read(buf)
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if string(buf[:n]) != "hello-relay" {
			t.Fatalf("got %q", buf[:n])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for incoming relay conn")
	}
}

func TestRelayAuth(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	s := NewServer("secret")
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		s.handle(c)
	}()
	defer ln.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = protocol.WriteMessage(conn, protocol.TypeRelayHello, protocol.RelayHello{
		DeviceID: "x",
		Token:    "wrong",
	})
	typ, body, err := protocol.ReadMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if typ != protocol.TypeError {
		t.Fatalf("expected error, got %s %s", typ, body)
	}
}
