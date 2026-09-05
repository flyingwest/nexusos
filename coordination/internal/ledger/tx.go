package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nexusos/coordination/internal/identity"
)

const domainTx = "nexusos-tx-v1"

// Tx is a signed ledger mutation. Consensus orders these into blocks;
// HTTP snapshot sync remains catch-up for liveness (heartbeats) and lag.
type Tx struct {
	Type      MessageType     `json:"type"`
	NodeID    string          `json:"node_id"`
	PublicKey string          `json:"public_key"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// ImagePayload is RegisterImage / VerifyImage.
type ImagePayload struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// ContainerPayload is CreateContainer / UpdateContainer.
type ContainerPayload struct {
	ContainerID  string            `json:"container_id"`
	ImageDigest  string            `json:"image_digest"`
	Desired      string            `json:"desired_state"`
	CurrentNode  string            `json:"current_node"`
	WorkloadID   string            `json:"workload_id,omitempty"`
	ReplicaIndex *uint32           `json:"replica_index,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Owner        string            `json:"owner,omitempty"`
}

// RemovePayload is RemoveContainer.
type RemovePayload struct {
	ContainerID string `json:"container_id"`
}

// MemberPayload is JoinMember / LeaveMember.
type MemberPayload struct {
	NodeID    string   `json:"node_id"`
	PublicKey string   `json:"public_key,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Label     string   `json:"label,omitempty"`
}

// WorkloadPayload is CreateWorkload / UpdateWorkload.
type WorkloadPayload struct {
	WorkloadID  string            `json:"workload_id"`
	ImageDigest string            `json:"image_digest"`
	ImageRef    string            `json:"image_ref,omitempty"`
	Replicas    uint32            `json:"replicas"`
	Resources   ResourceSpec      `json:"resources,omitempty"`
	Strategy    string            `json:"strategy,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Owner       string            `json:"owner,omitempty"`
}

// ScaleWorkloadPayload adjusts replica count only.
type ScaleWorkloadPayload struct {
	WorkloadID string `json:"workload_id"`
	Replicas   uint32 `json:"replicas"`
}

// DeleteWorkloadPayload removes a workload intent.
type DeleteWorkloadPayload struct {
	WorkloadID string `json:"workload_id"`
}

func txSignPayload(tx Tx) string {
	return fmt.Sprintf("%s\n%s\n%s\n%d\n%s",
		domainTx, tx.Type, tx.NodeID, tx.Timestamp.UTC().UnixMilli(), string(tx.Payload))
}

// NewTx signs a mutation with the node's key.
func NewTx(kp *identity.KeyPair, typ MessageType, payload any, now time.Time) (Tx, error) {
	if kp == nil {
		return Tx{}, fmt.Errorf("keypair is required")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Tx{}, err
	}
	pub := kp.PublicJSON()
	tx := Tx{
		Type:      typ,
		NodeID:    pub.NodeID,
		PublicKey: pub.PublicKey,
		Timestamp: now.UTC().Truncate(time.Millisecond),
		Payload:   raw,
	}
	tx.Signature = kp.SignHex([]byte(txSignPayload(tx)))
	return tx, nil
}

// Hash is sha256 of the signed payload (stable id for the mempool).
func (tx Tx) Hash() string {
	sum := sha256.Sum256([]byte(txSignPayload(tx)))
	return hex.EncodeToString(sum[:])
}

// VerifyTx checks identity and signature. It does not apply the mutation.
func VerifyTx(tx Tx) error {
	if tx.Type == "" {
		return fmt.Errorf("tx type is required")
	}
	if err := checkTxIdentity(tx.NodeID, tx.PublicKey); err != nil {
		return err
	}
	return identity.VerifyHex(tx.PublicKey, tx.Signature, []byte(txSignPayload(tx)))
}

func checkTxIdentity(nodeID, pubHex string) error {
	pub, err := identity.ParsePublicKey(pubHex)
	if err != nil {
		return err
	}
	if identity.NodeIDFromPublicKey(pub) != nodeID {
		return fmt.Errorf("node_id does not match public key")
	}
	return nil
}
