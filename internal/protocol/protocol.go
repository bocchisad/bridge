package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const MaxMessage = 16 << 20 // 16 MiB control messages

type Type string

const (
	TypePairHello    Type = "pair_hello"
	TypePairAck      Type = "pair_ack"
	TypePairDone     Type = "pair_done"
	TypeOffer        Type = "offer"
	TypeAccept       Type = "accept"
	TypeReject       Type = "reject"
	TypeProgress     Type = "progress"
	TypeDone         Type = "done"
	TypeError        Type = "error"
	TypeRelayHello   Type = "relay_hello"
	TypeRelayConnect Type = "relay_connect"
	TypeRelayReady   Type = "relay_ready"
)

type Envelope struct {
	Type Type            `json:"type"`
	Body json.RawMessage `json:"body"`
}

type PairHello struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	PIN        string `json:"pin"`
	CertDER    []byte `json:"cert_der"`
	ListenPort int    `json:"listen_port"`
}

type PairAck struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	CertDER    []byte `json:"cert_der"`
	ListenPort int    `json:"listen_port"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

type Offer struct {
	TransferID string `json:"transfer_id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Kind       string `json:"kind"` // file | folder
	FromID     string `json:"from_id"`
	FromName   string `json:"from_name"`
}

type Accept struct {
	TransferID string `json:"transfer_id"`
}

type Reject struct {
	TransferID string `json:"transfer_id"`
	Reason     string `json:"reason,omitempty"`
}

type Progress struct {
	TransferID string `json:"transfer_id"`
	BytesSent  int64  `json:"bytes_sent"`
	Total      int64  `json:"total"`
}

type Done struct {
	TransferID string `json:"transfer_id"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

type ErrorBody struct {
	Message string `json:"message"`
}

type RelayHello struct {
	DeviceID string `json:"device_id"`
	Token    string `json:"token"`
}

type RelayConnect struct {
	TargetID string `json:"target_id"`
}

func WriteMessage(w io.Writer, typ Type, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	env, err := json.Marshal(Envelope{Type: typ, Body: raw})
	if err != nil {
		return err
	}
	if len(env) > MaxMessage {
		return fmt.Errorf("message too large")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(env)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(env)
	return err
}

func ReadMessage(r io.Reader) (Type, json.RawMessage, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxMessage {
		return "", nil, fmt.Errorf("invalid message size %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", nil, err
	}
	var env Envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return "", nil, err
	}
	return env.Type, env.Body, nil
}

func Decode[T any](body json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(body, &v)
	return v, err
}
