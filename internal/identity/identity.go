package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"encoding/hex"

	"github.com/mell/bridge/internal/config"
)

type Identity struct {
	DeviceID  string `json:"device_id"`
	CertPEM   []byte `json:"-"`
	KeyPEM    []byte `json:"-"`
	Cert      tls.Certificate
	CertDER   []byte
	PublicDER []byte // leaf cert raw for pinning
}

type metaFile struct {
	DeviceID string `json:"device_id"`
}

func LoadOrCreate() (*Identity, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	metaPath := filepath.Join(dir, "identity.json")
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if _, err := os.Stat(metaPath); err == nil {
		return load(metaPath, certPath, keyPath)
	}
	return create(metaPath, certPath, keyPath)
}

func load(metaPath, certPath, keyPath string) (*Identity, error) {
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var meta metaFile
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	return &Identity{
		DeviceID:  meta.DeviceID,
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
		Cert:      cert,
		CertDER:   cert.Certificate[0],
		PublicDER: leaf.Raw,
	}, nil
}

func create(metaPath, certPath, keyPath string) (*Identity, error) {
	deviceID, err := newDeviceID()
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   deviceID,
			Organization: []string{"bridge"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	meta, err := json.MarshalIndent(metaFile{DeviceID: deviceID}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(metaPath, meta, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &Identity{
		DeviceID:  deviceID,
		CertPEM:   certPEM,
		KeyPEM:    keyPEM,
		Cert:      cert,
		CertDER:   der,
		PublicDER: der,
	}, nil
}

func newDeviceID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	), nil
}

func Fingerprint(certDER []byte) string {
	sum := sha256.Sum256(certDER)
	return fmt.Sprintf("%x", sum[:])
}

func (id *Identity) Fingerprint() string {
	return Fingerprint(id.CertDER)
}

func (id *Identity) ServerTLSConfig(verifyPeer func(rawCerts [][]byte) error) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.Cert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyPeer(rawCerts)
		},
		InsecureSkipVerify: true, // custom pin verification
	}
}

func (id *Identity) ClientTLSConfig(expectedCertDER []byte) *tls.Config {
	want := Fingerprint(expectedCertDER)
	return &tls.Config{
		Certificates:       []tls.Certificate{id.Cert},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no peer certificate")
			}
			if Fingerprint(rawCerts[0]) != want {
				return fmt.Errorf("peer certificate fingerprint mismatch")
			}
			return nil
		},
	}
}

func (id *Identity) PairingTLSConfig() *tls.Config {
	// During pairing we accept any cert; we pin after success.
	return &tls.Config{
		Certificates:       []tls.Certificate{id.Cert},
		ClientAuth:         tls.RequireAnyClientCert,
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no peer certificate")
			}
			return nil
		},
	}
}
