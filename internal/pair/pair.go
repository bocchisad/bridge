package pair

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"
	"time"
)

type Session struct {
	PIN       string
	ExpiresAt time.Time
}

type Manager struct {
	mu      sync.Mutex
	current *Session
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Start() (Session, error) {
	pin, err := generatePIN()
	if err != nil {
		return Session{}, err
	}
	s := Session{
		PIN:       pin,
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}
	m.mu.Lock()
	m.current = &s
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) Validate(pin string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return false
	}
	if time.Now().After(m.current.ExpiresAt) {
		m.current = nil
		return false
	}
	ok := m.current.PIN == pin
	if ok {
		m.current = nil
	}
	return ok
}

func (m *Manager) Current() (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || time.Now().After(m.current.ExpiresAt) {
		return Session{}, false
	}
	return *m.current, true
}

func generatePIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
