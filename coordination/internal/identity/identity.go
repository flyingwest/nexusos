package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyPair is a node identity.
type KeyPair struct {
	PublicKey  ed25519.PublicKey  `json:"-"`
	PrivateKey ed25519.PrivateKey `json:"-"`
	NodeID     string             `json:"node_id"` // hex of public key
}

// Generate creates a new Ed25519 identity.
func Generate() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{
		PublicKey:  pub,
		PrivateKey: priv,
		NodeID:     hex.EncodeToString(pub),
	}, nil
}

// Sign signs a message with the private key.
func (k *KeyPair) Sign(msg []byte) []byte {
	return ed25519.Sign(k.PrivateKey, msg)
}

// Verify checks a signature against this public key.
func (k *KeyPair) Verify(msg, sig []byte) bool {
	return ed25519.Verify(k.PublicKey, msg, sig)
}

// VerifyWith checks a signature with an arbitrary public key.
func VerifyWith(pub ed25519.PublicKey, msg, sig []byte) bool {
	return ed25519.Verify(pub, msg, sig)
}

// Save writes the key pair to dataDir/node.key (private) and node.id (public id).
func (k *KeyPair) Save(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	privPath := filepath.Join(dataDir, "node.key")
	idPath := filepath.Join(dataDir, "node.id")

	if err := os.WriteFile(privPath, []byte(hex.EncodeToString(k.PrivateKey)), 0o600); err != nil {
		return err
	}
	return os.WriteFile(idPath, []byte(k.NodeID+"\n"), 0o644)
}

// Load reads an existing identity from dataDir, or generates and saves a new one.
func LoadOrCreate(dataDir string) (*KeyPair, error) {
	privPath := filepath.Join(dataDir, "node.key")
	b, err := os.ReadFile(privPath)
	if err != nil {
		if os.IsNotExist(err) {
			kp, err := Generate()
			if err != nil {
				return nil, err
			}
			if err := kp.Save(dataDir); err != nil {
				return nil, err
			}
			return kp, nil
		}
		return nil, err
	}

	privHex := string(b)
	// trim newline if present
	if len(privHex) > 0 && privHex[len(privHex)-1] == '\n' {
		privHex = privHex[:len(privHex)-1]
	}
	privBytes, err := hex.DecodeString(privHex)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: %d", len(privBytes))
	}
	priv := ed25519.PrivateKey(privBytes)
	pub := priv.Public().(ed25519.PublicKey)
	return &KeyPair{
		PublicKey:  pub,
		PrivateKey: priv,
		NodeID:     hex.EncodeToString(pub),
	}, nil
}

// PublicJSON is a serializable public identity.
type PublicJSON struct {
	NodeID    string `json:"node_id"`
	PublicKey string `json:"public_key"` // hex
}

func (k *KeyPair) PublicJSON() PublicJSON {
	return PublicJSON{
		NodeID:    k.NodeID,
		PublicKey: hex.EncodeToString(k.PublicKey),
	}
}

func (k *KeyPair) String() string {
	b, _ := json.Marshal(k.PublicJSON())
	return string(b)
}

// SignHex signs msg and returns a hex-encoded signature.
func (k *KeyPair) SignHex(msg []byte) string {
	return hex.EncodeToString(k.Sign(msg))
}

// ParsePublicKey decodes a hex-encoded Ed25519 public key.
func ParsePublicKey(h string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(h))
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: %d", len(b))
	}
	return ed25519.PublicKey(b), nil
}

// NodeIDFromPublicKey returns the canonical node ID (hex of the public key).
func NodeIDFromPublicKey(pub ed25519.PublicKey) string {
	return hex.EncodeToString(pub)
}

// VerifyHex checks a hex signature against a hex public key.
func VerifyHex(pubHex, sigHex string, msg []byte) error {
	pub, err := ParsePublicKey(pubHex)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(pub, msg, sig) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
