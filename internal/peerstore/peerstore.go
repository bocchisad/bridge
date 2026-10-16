package peerstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mell/bridge/internal/config"
)

type Peer struct {
	DeviceID    string    `json:"device_id"`
	Name        string    `json:"name"`
	CertDER     []byte    `json:"cert_der"`
	Fingerprint string    `json:"fingerprint"`
	LastHost    string    `json:"last_host,omitempty"`
	LastPort    int       `json:"last_port,omitempty"`
	PairedAt    time.Time `json:"paired_at"`
}

type Store struct {
	mu    sync.RWMutex
	path  string
	Peers map[string]Peer `json:"peers"`
}

func Load() (*Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "peers.json")
	s := &Store{path: path, Peers: map[string]Peer{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, s.saveLocked()
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Peers == nil {
		s.Peers = map[string]Peer{}
	}
	s.path = path
	return s, nil
}

func (s *Store) List() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Peer, 0, len(s.Peers))
	for _, p := range s.Peers {
		out = append(out, p)
	}
	return out
}

func (s *Store) Get(deviceID string) (Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.Peers[deviceID]
	return p, ok
}

func (s *Store) GetByName(name string) (Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.Peers {
		if p.Name == name || p.DeviceID == name {
			return p, true
		}
	}
	return Peer{}, false
}

func (s *Store) Upsert(p Peer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Peers[p.DeviceID] = p
	return s.saveLocked()
}

func (s *Store) Remove(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Peers, deviceID)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(struct {
		Peers map[string]Peer `json:"peers"`
	}{Peers: s.Peers}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
