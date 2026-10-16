package ipc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mell/bridge/internal/config"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New() (*Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return &Client{
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort),
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

func (c *Client) get(path string, out any) error {
	resp, err := c.HTTP.Get(c.BaseURL + path)
	if err != nil {
		return fmt.Errorf("daemon not reachable (%v). Start it with: bridge daemon", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s", string(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *Client) post(path string, in any, out any) error {
	var buf bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&buf).Encode(in); err != nil {
			return err
		}
	}
	resp, err := c.HTTP.Post(c.BaseURL+path, "application/json", &buf)
	if err != nil {
		return fmt.Errorf("daemon not reachable (%v). Start it with: bridge daemon", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s", string(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

type StatusResp struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Fingerprint string `json:"fingerprint"`
	InboxDir   string `json:"inbox_dir"`
	RelayURL   string `json:"relay_url"`
	ListenPort int    `json:"listen_port"`
	Online     bool   `json:"online"`
}

type PeerResp struct {
	DeviceID    string `json:"device_id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Online      bool   `json:"online"`
	Host        string `json:"host,omitempty"`
}

type PairStartResp struct {
	PIN       string `json:"pin"`
	ExpiresAt string `json:"expires_at"`
}

type PairJoinReq struct {
	PIN  string `json:"pin"`
	Addr string `json:"addr,omitempty"`
}

type SendReq struct {
	Peer string `json:"peer"`
	Path string `json:"path"`
	Yes  bool   `json:"yes"`
}

type SendResp struct {
	TransferID string `json:"transfer_id"`
	Status     string `json:"status"`
	Path       string `json:"path,omitempty"`
	Error      string `json:"error,omitempty"`
}

type IncomingResp struct {
	TransferID string `json:"transfer_id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Kind       string `json:"kind"`
	FromName   string `json:"from_name"`
	FromID     string `json:"from_id"`
}

func (c *Client) Status() (StatusResp, error) {
	var s StatusResp
	return s, c.get("/api/status", &s)
}

func (c *Client) Peers() ([]PeerResp, error) {
	var out struct {
		Peers []PeerResp `json:"peers"`
	}
	if err := c.get("/api/peers", &out); err != nil {
		return nil, err
	}
	return out.Peers, nil
}

func (c *Client) PairStart() (PairStartResp, error) {
	var s PairStartResp
	return s, c.post("/api/pair/start", nil, &s)
}

func (c *Client) PairJoin(pin, addr string) error {
	return c.post("/api/pair/join", PairJoinReq{PIN: pin, Addr: addr}, nil)
}

func (c *Client) Send(peer, path string, yes bool) (SendResp, error) {
	var s SendResp
	err := c.post("/api/send", SendReq{Peer: peer, Path: path, Yes: yes}, &s)
	return s, err
}

func (c *Client) Incoming() ([]IncomingResp, error) {
	var out struct {
		Incoming []IncomingResp `json:"incoming"`
	}
	if err := c.get("/api/incoming", &out); err != nil {
		return nil, err
	}
	return out.Incoming, nil
}

func (c *Client) Accept(transferID string) error {
	return c.post("/api/incoming/accept", map[string]string{"transfer_id": transferID}, nil)
}

func (c *Client) Reject(transferID, reason string) error {
	return c.post("/api/incoming/reject", map[string]string{"transfer_id": transferID, "reason": reason}, nil)
}
