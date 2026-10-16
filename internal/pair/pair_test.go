package pair

import "testing"

func TestPINValidate(t *testing.T) {
	m := NewManager()
	s, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PIN) != 6 {
		t.Fatalf("pin len %d", len(s.PIN))
	}
	if !m.Validate(s.PIN) {
		t.Fatal("expected valid")
	}
	if m.Validate(s.PIN) {
		t.Fatal("PIN should be single-use")
	}
}
