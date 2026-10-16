package discovery

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const ServiceType = "_bridge._tcp"

type PeerInfo struct {
	DeviceID string
	Name     string
	Host     string
	Port     int
}

type Announcer struct {
	server *zeroconf.Server
}

func Announce(deviceID, name string, port int) (*Announcer, error) {
	server, err := zeroconf.Register(name, ServiceType, "local.", port, []string{
		"id=" + deviceID,
		"name=" + name,
	}, nil)
	if err != nil {
		return nil, err
	}
	return &Announcer{server: server}, nil
}

func (a *Announcer) Shutdown() {
	if a != nil && a.server != nil {
		a.server.Shutdown()
	}
}

type Browser struct {
	mu    sync.RWMutex
	peers map[string]PeerInfo
}

func NewBrowser() *Browser {
	return &Browser{peers: map[string]PeerInfo{}}
}

func (b *Browser) Start(ctx context.Context) error {
	go b.loop(ctx)
	return nil
}

func (b *Browser) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := b.browseOnce(ctx); err != nil {
			log.Printf("mDNS browse: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (b *Browser) browseOnce(ctx context.Context) error {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return err
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	browseCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			b.handle(e)
		}
	}()

	err = resolver.Browse(browseCtx, ServiceType, "local.", entries)
	<-done
	return err
}

func (b *Browser) handle(e *zeroconf.ServiceEntry) {
	info := PeerInfo{
		Name: e.Instance,
		Port: e.Port,
	}
	for _, t := range e.Text {
		if len(t) > 3 && t[:3] == "id=" {
			info.DeviceID = t[3:]
		}
		if len(t) > 5 && t[:5] == "name=" {
			info.Name = t[5:]
		}
	}
	if info.DeviceID == "" {
		return
	}
	if len(e.AddrIPv4) > 0 {
		info.Host = e.AddrIPv4[0].String()
	} else if len(e.AddrIPv6) > 0 {
		info.Host = e.AddrIPv6[0].String()
	} else {
		return
	}
	b.mu.Lock()
	b.peers[info.DeviceID] = info
	b.mu.Unlock()
}

func (b *Browser) List() []PeerInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]PeerInfo, 0, len(b.peers))
	for _, p := range b.peers {
		out = append(out, p)
	}
	return out
}

func (b *Browser) Lookup(deviceID string) (PeerInfo, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	p, ok := b.peers[deviceID]
	return p, ok
}

func DialAddr(host string, port int) string {
	ip := net.ParseIP(host)
	if ip != nil && ip.To4() == nil {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}
