package daemon

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mell/bridge/internal/config"
	"github.com/mell/bridge/internal/discovery"
	"github.com/mell/bridge/internal/identity"
	"github.com/mell/bridge/internal/pair"
	"github.com/mell/bridge/internal/peerstore"
	"github.com/mell/bridge/internal/protocol"
	"github.com/mell/bridge/internal/relay"
	"github.com/mell/bridge/internal/transfer"
)

type Daemon struct {
	cfg      config.Config
	id       *identity.Identity
	peers    *peerstore.Store
	pairer   *pair.Manager
	browser  *discovery.Browser
	announce *discovery.Announcer

	mu        sync.Mutex
	incoming  map[string]*pendingTransfer
	stopRelay chan struct{}
}

type pendingTransfer struct {
	Offer   protocol.Offer
	Conn    net.Conn
	Decision chan bool // true=accept
}

func New() (*Daemon, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	id, err := identity.LoadOrCreate()
	if err != nil {
		return nil, err
	}
	peers, err := peerstore.Load()
	if err != nil {
		return nil, err
	}
	return &Daemon{
		cfg:      cfg,
		id:       id,
		peers:    peers,
		pairer:   pair.NewManager(),
		browser:  discovery.NewBrowser(),
		incoming: map[string]*pendingTransfer{},
		stopRelay: make(chan struct{}),
	}, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	ann, err := discovery.Announce(d.id.DeviceID, d.cfg.DeviceName, d.cfg.ListenPort)
	if err != nil {
		log.Printf("mDNS announce warning: %v (LAN discovery may not work)", err)
	} else {
		d.announce = ann
		defer d.announce.Shutdown()
	}
	if err := d.browser.Start(ctx); err != nil {
		log.Printf("mDNS browse warning: %v", err)
	}

	ln, err := tls.Listen("tcp", fmt.Sprintf(":%d", d.cfg.ListenPort), d.id.ServerTLSConfig(d.verifyPeer))
	if err != nil {
		return err
	}
	go d.acceptLoop(ctx, ln)

	if d.cfg.RelayURL != "" {
		go d.relayAcceptLoop(ctx)
	}

	mux := http.NewServeMux()
	d.routes(mux)
	api := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", d.cfg.APIPort),
		Handler: mux,
	}
	go func() {
		<-ctx.Done()
		_ = api.Shutdown(context.Background())
		ln.Close()
		close(d.stopRelay)
	}()

	log.Printf("bridge daemon online as %s (%s) listen=:%d api=127.0.0.1:%d",
		d.cfg.DeviceName, d.id.DeviceID, d.cfg.ListenPort, d.cfg.APIPort)
	return api.ListenAndServe()
}

func (d *Daemon) verifyPeer(rawCerts [][]byte) error {
	// Accept any client cert at TLS layer. Pairing PIN / peerstore checks happen
	// in protocol handlers — otherwise macOS clients hit "bad certificate" when
	// the PIN window is tight or mDNS retries race the handshake.
	if len(rawCerts) == 0 {
		return fmt.Errorf("no certificate")
	}
	return nil
}

func peerCertFingerprint(conn net.Conn) (string, bool) {
	tc, ok := conn.(*tls.Conn)
	if !ok {
		return "", false
	}
	state := tc.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", false
	}
	return identity.Fingerprint(state.PeerCertificates[0].Raw), true
}

func (d *Daemon) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) relayAcceptLoop(ctx context.Context) {
	client := &relay.Client{
		URL:      d.cfg.RelayURL,
		Token:    d.cfg.RelayToken,
		DeviceID: d.id.DeviceID,
	}
	go client.AcceptLoop(d.stopRelay, func(conn net.Conn) {
		tlsConn := tls.Server(conn, d.id.ServerTLSConfig(d.verifyPeer))
		if err := tlsConn.Handshake(); err != nil {
			tlsConn.Close()
			return
		}
		d.handleConn(tlsConn)
	})
	<-ctx.Done()
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	typ, body, err := protocol.ReadMessage(conn)
	if err != nil {
		return
	}
	switch typ {
	case protocol.TypePairHello:
		d.handlePairHello(conn, body)
	case protocol.TypeOffer:
		d.handleOffer(conn, body)
	default:
		_ = protocol.WriteMessage(conn, protocol.TypeError, protocol.ErrorBody{Message: "unexpected " + string(typ)})
	}
}

func (d *Daemon) handlePairHello(conn net.Conn, body json.RawMessage) {
	hello, err := protocol.Decode[protocol.PairHello](body)
	if err != nil {
		return
	}
	if !d.pairer.Validate(hello.PIN) {
		_ = protocol.WriteMessage(conn, protocol.TypePairAck, protocol.PairAck{OK: false, Error: "invalid or expired PIN"})
		return
	}
	ack := protocol.PairAck{
		DeviceID:   d.id.DeviceID,
		Name:       d.cfg.DeviceName,
		CertDER:    d.id.CertDER,
		ListenPort: d.cfg.ListenPort,
		OK:         true,
	}
	if err := protocol.WriteMessage(conn, protocol.TypePairAck, ack); err != nil {
		return
	}
	host := ""
	if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		host = ra.IP.String()
	}
	p := peerstore.Peer{
		DeviceID:    hello.DeviceID,
		Name:        hello.Name,
		CertDER:     hello.CertDER,
		Fingerprint: identity.Fingerprint(hello.CertDER),
		LastHost:    host,
		LastPort:    hello.ListenPort,
		PairedAt:    time.Now(),
	}
	_ = d.peers.Upsert(p)
	_ = protocol.WriteMessage(conn, protocol.TypePairDone, map[string]bool{"ok": true})
	log.Printf("paired with %s (%s)", p.Name, p.DeviceID)
}

func (d *Daemon) handleOffer(conn net.Conn, body json.RawMessage) {
	offer, err := protocol.Decode[protocol.Offer](body)
	if err != nil {
		return
	}
	peer, ok := d.peers.Get(offer.FromID)
	if !ok {
		_ = protocol.WriteMessage(conn, protocol.TypeReject, protocol.Reject{TransferID: offer.TransferID, Reason: "not paired"})
		return
	}
	if fp, ok := peerCertFingerprint(conn); !ok || fp != peer.Fingerprint {
		_ = protocol.WriteMessage(conn, protocol.TypeReject, protocol.Reject{TransferID: offer.TransferID, Reason: "certificate mismatch"})
		return
	}

	dec := make(chan bool, 1)
	pending := &pendingTransfer{Offer: offer, Conn: conn, Decision: dec}
	d.mu.Lock()
	d.incoming[offer.TransferID] = pending
	d.mu.Unlock()

	if d.cfg.AutoAccept {
		dec <- true
	}

	select {
	case ok := <-dec:
		d.mu.Lock()
		delete(d.incoming, offer.TransferID)
		d.mu.Unlock()
		if !ok {
			_ = protocol.WriteMessage(conn, protocol.TypeReject, protocol.Reject{TransferID: offer.TransferID, Reason: "rejected"})
			return
		}
		if err := protocol.WriteMessage(conn, protocol.TypeAccept, protocol.Accept{TransferID: offer.TransferID}); err != nil {
			return
		}
		kind := transfer.KindFile
		if offer.Kind == "folder" {
			kind = transfer.KindFolder
		}
		res, err := transfer.Receive(conn, d.cfg.InboxDir, offer.Name, offer.Size, offer.SHA256, kind, nil)
		if err != nil {
			_ = protocol.WriteMessage(conn, protocol.TypeDone, protocol.Done{TransferID: offer.TransferID, OK: false, Error: err.Error()})
			log.Printf("receive failed: %v", err)
			return
		}
		_ = protocol.WriteMessage(conn, protocol.TypeDone, protocol.Done{TransferID: offer.TransferID, OK: res.OK})
		log.Printf("received %s -> %s", offer.Name, res.Path)
	case <-time.After(2 * time.Minute):
		d.mu.Lock()
		delete(d.incoming, offer.TransferID)
		d.mu.Unlock()
		_ = protocol.WriteMessage(conn, protocol.TypeReject, protocol.Reject{TransferID: offer.TransferID, Reason: "timeout"})
	}
}

func (d *Daemon) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", d.apiStatus)
	mux.HandleFunc("/api/peers", d.apiPeers)
	mux.HandleFunc("/api/pair/start", d.apiPairStart)
	mux.HandleFunc("/api/pair/join", d.apiPairJoin)
	mux.HandleFunc("/api/send", d.apiSend)
	mux.HandleFunc("/api/incoming", d.apiIncoming)
	mux.HandleFunc("/api/incoming/accept", d.apiAccept)
	mux.HandleFunc("/api/incoming/reject", d.apiReject)
}

func (d *Daemon) apiStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"device_id":    d.id.DeviceID,
		"device_name":  d.cfg.DeviceName,
		"fingerprint": d.id.Fingerprint(),
		"inbox_dir":   d.cfg.InboxDir,
		"relay_url":   d.cfg.RelayURL,
		"listen_port": d.cfg.ListenPort,
		"online":      true,
	})
}

func (d *Daemon) apiPeers(w http.ResponseWriter, r *http.Request) {
	var list []map[string]any
	for _, p := range d.peers.List() {
		online := false
		host := ""
		if info, ok := d.browser.Lookup(p.DeviceID); ok {
			online = true
			host = info.Host
		}
		list = append(list, map[string]any{
			"device_id":    p.DeviceID,
			"name":         p.Name,
			"fingerprint":  p.Fingerprint,
			"online":       online,
			"host":         host,
		})
	}
	if list == nil {
		list = []map[string]any{}
	}
	writeJSON(w, map[string]any{"peers": list})
}

func (d *Daemon) apiPairStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	s, err := d.pairer.Start()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Also listen with pairing-friendly TLS briefly on same port is hard;
	// pairing join dials and sends PairHello which verifyPeer rejects.
	// Fix: use a separate pairing listener OR relax verify when PIN session active.
	writeJSON(w, map[string]any{
		"pin":        s.PIN,
		"expires_at": s.ExpiresAt.Format(time.RFC3339),
	})
}

func (d *Daemon) apiPairJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var req struct {
		PIN  string `json:"pin"`
		Addr string `json:"addr,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PIN == "" {
		http.Error(w, "pin required", 400)
		return
	}
	if err := d.joinPair(req.PIN, req.Addr); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]string{"status": "paired"})
}

func (d *Daemon) joinPair(pin, addr string) error {
	if addr != "" {
		return d.pairDial(addr, pin)
	}
	candidates := d.browser.List()
	if len(candidates) == 0 {
		time.Sleep(3 * time.Second)
		candidates = d.browser.List()
	}
	var lastErr error
	for _, c := range candidates {
		if c.DeviceID == d.id.DeviceID {
			continue
		}
		a := discovery.DialAddr(c.Host, c.Port)
		if err := d.pairDial(a, pin); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no reachable devices found on LAN; pass --addr host:port or check mDNS")
}

func (d *Daemon) pairDial(addr, pin string) error {
	tlsCfg := d.id.PairingTLSConfig()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, tlsCfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	hello := protocol.PairHello{
		DeviceID:   d.id.DeviceID,
		Name:       d.cfg.DeviceName,
		PIN:        pin,
		CertDER:    d.id.CertDER,
		ListenPort: d.cfg.ListenPort,
	}
	if err := protocol.WriteMessage(conn, protocol.TypePairHello, hello); err != nil {
		return err
	}
	typ, body, err := protocol.ReadMessage(conn)
	if err != nil {
		return err
	}
	if typ != protocol.TypePairAck {
		return fmt.Errorf("unexpected response %s", typ)
	}
	ack, err := protocol.Decode[protocol.PairAck](body)
	if err != nil {
		return err
	}
	if !ack.OK {
		return fmt.Errorf("%s", ack.Error)
	}
	host, _, _ := net.SplitHostPort(addr)
	port := ack.ListenPort
	if port == 0 {
		_, portStr, _ := net.SplitHostPort(addr)
		fmt.Sscanf(portStr, "%d", &port)
	}
	p := peerstore.Peer{
		DeviceID:    ack.DeviceID,
		Name:        ack.Name,
		CertDER:     ack.CertDER,
		Fingerprint: identity.Fingerprint(ack.CertDER),
		LastHost:    host,
		LastPort:    port,
		PairedAt:    time.Now(),
	}
	if err := d.peers.Upsert(p); err != nil {
		return err
	}
	_, _, _ = protocol.ReadMessage(conn) // PairDone
	return nil
}

func (d *Daemon) apiSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var req struct {
		Peer string `json:"peer"`
		Path string `json:"path"`
		Yes  bool   `json:"yes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	res, err := d.send(req.Peer, req.Path)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, res)
}

func (d *Daemon) send(peerName, path string) (map[string]any, error) {
	peer, ok := d.peers.GetByName(peerName)
	if !ok {
		return nil, fmt.Errorf("unknown peer %q — pair first", peerName)
	}
	prepared, err := transfer.Prepare(path)
	if err != nil {
		return nil, err
	}
	conn, err := d.dialPeer(peer)
	if err != nil {
		hint := ""
		if d.cfg.RelayURL == "" {
			hint = " (peer offline on LAN; set relay_url in ~/.config/bridge/config.json for internet transfers)"
		}
		return nil, fmt.Errorf("cannot reach peer: %v%s", err, hint)
	}
	defer conn.Close()

	transferID := fmt.Sprintf("%d", time.Now().UnixNano())
	offer := protocol.Offer{
		TransferID: transferID,
		Name:       prepared.Name,
		Size:       prepared.Size,
		SHA256:     prepared.SHA256,
		Kind:       string(prepared.Kind),
		FromID:     d.id.DeviceID,
		FromName:   d.cfg.DeviceName,
	}
	if err := protocol.WriteMessage(conn, protocol.TypeOffer, offer); err != nil {
		return nil, err
	}
	typ, body, err := protocol.ReadMessage(conn)
	if err != nil {
		return nil, err
	}
	switch typ {
	case protocol.TypeReject:
		rej, _ := protocol.Decode[protocol.Reject](body)
		return map[string]any{"transfer_id": transferID, "status": "rejected", "error": rej.Reason}, nil
	case protocol.TypeAccept:
		// continue
	default:
		return nil, fmt.Errorf("unexpected %s", typ)
	}
	if err := transfer.Stream(conn, prepared, nil); err != nil {
		return nil, err
	}
	typ, body, err = protocol.ReadMessage(conn)
	if err != nil && err != io.EOF {
		// receiver may close after writing done
	}
	status := "sent"
	if typ == protocol.TypeDone {
		done, _ := protocol.Decode[protocol.Done](body)
		if !done.OK {
			status = "failed"
			return map[string]any{"transfer_id": transferID, "status": status, "error": done.Error}, nil
		}
	}
	return map[string]any{"transfer_id": transferID, "status": status}, nil
}

func (d *Daemon) dialPeer(peer peerstore.Peer) (net.Conn, error) {
	tlsCfg := d.id.ClientTLSConfig(peer.CertDER)
	try := func(addr string) (net.Conn, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, tlsCfg)
	}
	if info, ok := d.browser.Lookup(peer.DeviceID); ok {
		addr := discovery.DialAddr(info.Host, info.Port)
		if conn, err := try(addr); err == nil {
			peer.LastHost = info.Host
			peer.LastPort = info.Port
			_ = d.peers.Upsert(peer)
			return conn, nil
		}
	}
	if peer.LastHost != "" && peer.LastPort != 0 {
		addr := discovery.DialAddr(peer.LastHost, peer.LastPort)
		if conn, err := try(addr); err == nil {
			return conn, nil
		}
	}
	if d.cfg.RelayURL != "" {
		rc := &relay.Client{URL: d.cfg.RelayURL, Token: d.cfg.RelayToken, DeviceID: d.id.DeviceID}
		raw, err := rc.Dial(peer.DeviceID)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(raw, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			raw.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	return nil, fmt.Errorf("peer not found on LAN")
}

func (d *Daemon) apiIncoming(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var list []map[string]any
	for _, p := range d.incoming {
		list = append(list, map[string]any{
			"transfer_id": p.Offer.TransferID,
			"name":        p.Offer.Name,
			"size":        p.Offer.Size,
			"kind":        p.Offer.Kind,
			"from_name":   p.Offer.FromName,
			"from_id":     p.Offer.FromID,
		})
	}
	if list == nil {
		list = []map[string]any{}
	}
	writeJSON(w, map[string]any{"incoming": list})
}

func (d *Daemon) apiAccept(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransferID string `json:"transfer_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	d.mu.Lock()
	p, ok := d.incoming[req.TransferID]
	d.mu.Unlock()
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	select {
	case p.Decision <- true:
	default:
	}
	writeJSON(w, map[string]string{"status": "accepting"})
}

func (d *Daemon) apiReject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransferID string `json:"transfer_id"`
		Reason     string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	d.mu.Lock()
	p, ok := d.incoming[req.TransferID]
	d.mu.Unlock()
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	select {
	case p.Decision <- false:
	default:
	}
	writeJSON(w, map[string]string{"status": "rejected"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
