package p2p

import (
	"fmt"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
)

const (
	domainHello = "nexusos-hello-v1"
	domainSync  = "nexusos-ledger-sync-v1"
	maxSkew     = 5 * time.Minute
)

// Hello is a signed node identity announcement.
type Hello struct {
	NodeID    string    `json:"node_id"`
	PublicKey string    `json:"public_key"`
	Advertise string    `json:"advertise"`
	Timestamp time.Time `json:"timestamp"`
	Signature string    `json:"signature"`
}

// PairRequest is a permissioned join from one node to another.
type PairRequest struct {
	Hello
	CallbackURL string `json:"callback_url,omitempty"`
	JoinToken   string `json:"join_token,omitempty"`
}

// SyncMessage is a signed ledger snapshot.
type SyncMessage struct {
	NodeID    string       `json:"node_id"`
	PublicKey string       `json:"public_key"`
	Advertise string       `json:"advertise,omitempty"`
	Timestamp time.Time    `json:"timestamp"`
	State     ledger.State `json:"state"`
	Signature string       `json:"signature"`
}

func helloPayload(nodeID, advertise string, ts time.Time) string {
	return fmt.Sprintf("%s\n%s\n%s\n%d", domainHello, nodeID, advertise, ts.UTC().UnixMilli())
}

func syncPayload(nodeID string, ts time.Time, stateHash string) string {
	return fmt.Sprintf("%s\n%s\n%d\n%s", domainSync, nodeID, ts.UTC().UnixMilli(), stateHash)
}

func SignHello(kp *identity.KeyPair, advertise string, now time.Time) Hello {
	pub := kp.PublicJSON()
	ts := now.UTC().Truncate(time.Millisecond)
	h := Hello{
		NodeID:    pub.NodeID,
		PublicKey: pub.PublicKey,
		Advertise: advertise,
		Timestamp: ts,
	}
	h.Signature = kp.SignHex([]byte(helloPayload(h.NodeID, h.Advertise, h.Timestamp)))
	return h
}

func VerifyHello(h Hello, now time.Time) error {
	if err := checkIdentity(h.NodeID, h.PublicKey); err != nil {
		return err
	}
	if err := checkSkew(h.Timestamp, now); err != nil {
		return err
	}
	return identity.VerifyHex(h.PublicKey, h.Signature, []byte(helloPayload(h.NodeID, h.Advertise, h.Timestamp)))
}

func SignSync(kp *identity.KeyPair, advertise string, st ledger.State, now time.Time) (SyncMessage, error) {
	hash, err := ledger.CanonicalHash(st)
	if err != nil {
		return SyncMessage{}, err
	}
	pub := kp.PublicJSON()
	ts := now.UTC().Truncate(time.Millisecond)
	msg := SyncMessage{
		NodeID:    pub.NodeID,
		PublicKey: pub.PublicKey,
		Advertise: advertise,
		Timestamp: ts,
		State:     st,
	}
	msg.Signature = kp.SignHex([]byte(syncPayload(msg.NodeID, msg.Timestamp, hash)))
	return msg, nil
}

func VerifySync(msg SyncMessage, now time.Time) error {
	if err := checkIdentity(msg.NodeID, msg.PublicKey); err != nil {
		return err
	}
	if err := checkSkew(msg.Timestamp, now); err != nil {
		return err
	}
	hash, err := ledger.CanonicalHash(msg.State)
	if err != nil {
		return err
	}
	return identity.VerifyHex(msg.PublicKey, msg.Signature, []byte(syncPayload(msg.NodeID, msg.Timestamp, hash)))
}

func checkIdentity(nodeID, pubHex string) error {
	pub, err := identity.ParsePublicKey(pubHex)
	if err != nil {
		return err
	}
	if identity.NodeIDFromPublicKey(pub) != nodeID {
		return fmt.Errorf("node_id does not match public key")
	}
	return nil
}

func checkSkew(ts, now time.Time) error {
	if ts.After(now.Add(maxSkew)) {
		return fmt.Errorf("timestamp in the future")
	}
	if now.Sub(ts) > maxSkew {
		return fmt.Errorf("timestamp too old")
	}
	return nil
}
