package ledger

import (
	"encoding/json"
	"fmt"
	"time"
)

// ApplyTx applies a verified transaction to in-memory state.
// Timestamps come from the tx so every replica computes the same result.
func ApplyTx(st *State, tx Tx) error {
	if st == nil {
		return fmt.Errorf("nil state")
	}
	if st.Nodes == nil {
		st.Nodes = make(map[string]NodeRecord)
	}
	if st.Images == nil {
		st.Images = make(map[string]ImageRecord)
	}
	if st.Containers == nil {
		st.Containers = make(map[string]ContainerRecord)
	}
	if st.Migrations == nil {
		st.Migrations = make(map[string]MigrationRecord)
	}
	if st.Tombstones == nil {
		st.Tombstones = make(map[string]time.Time)
	}
	if st.Members == nil {
		st.Members = make(map[string]MemberRecord)
	}

	switch tx.Type {
	case MsgRegisterImage, MsgVerifyImage:
		var p ImagePayload
		if err := json.Unmarshal(tx.Payload, &p); err != nil {
			return fmt.Errorf("image payload: %w", err)
		}
		if p.Digest == "" {
			return fmt.Errorf("image digest is required")
		}
		img, ok := st.Images[p.Digest]
		if !ok {
			img = ImageRecord{Digest: p.Digest, FirstSeen: tx.Timestamp}
		}
		if p.Size > img.Size {
			img.Size = p.Size
		}
		if img.FirstSeen.IsZero() || (!tx.Timestamp.IsZero() && tx.Timestamp.Before(img.FirstSeen)) {
			img.FirstSeen = tx.Timestamp
		}
		img.VerifiedBy = unionStrings(img.VerifiedBy, []string{tx.NodeID})
		st.Images[p.Digest] = img
		return nil

	case MsgCreateContainer, MsgUpdateContainer:
		var p ContainerPayload
		if err := json.Unmarshal(tx.Payload, &p); err != nil {
			return fmt.Errorf("container payload: %w", err)
		}
		if p.ContainerID == "" {
			return fmt.Errorf("container_id is required")
		}
		owner := p.Owner
		if owner == "" {
			owner = tx.NodeID
		}
		st.Containers[p.ContainerID] = ContainerRecord{
			ContainerID: p.ContainerID,
			ImageDigest: p.ImageDigest,
			Owner:       owner,
			Desired:     p.Desired,
			CurrentNode: p.CurrentNode,
			Labels:      p.Labels,
			UpdatedAt:   tx.Timestamp,
		}
		delete(st.Tombstones, ContainerTomb(p.ContainerID))
		return nil

	case MsgRemoveContainer:
		var p RemovePayload
		if err := json.Unmarshal(tx.Payload, &p); err != nil {
			return fmt.Errorf("remove payload: %w", err)
		}
		if p.ContainerID == "" {
			return fmt.Errorf("container_id is required")
		}
		delete(st.Containers, p.ContainerID)
		st.Tombstones[ContainerTomb(p.ContainerID)] = tx.Timestamp
		return nil

	case MsgJoinMember:
		var p MemberPayload
		if err := json.Unmarshal(tx.Payload, &p); err != nil {
			return fmt.Errorf("member payload: %w", err)
		}
		if p.NodeID == "" {
			return fmt.Errorf("member node_id is required")
		}
		if p.PublicKey == "" {
			return fmt.Errorf("member public_key is required")
		}
		if err := checkTxIdentity(p.NodeID, p.PublicKey); err != nil {
			return fmt.Errorf("member identity: %w", err)
		}
		// Ensure the admitting signer is recorded so validator diffs include both.
		if _, ok := st.Members[tx.NodeID]; !ok {
			st.Members[tx.NodeID] = MemberRecord{
				NodeID:    tx.NodeID,
				PublicKey: tx.PublicKey,
				Label:     "member",
				UpdatedAt: tx.Timestamp,
			}
		}
		st.Members[p.NodeID] = MemberRecord{
			NodeID:    p.NodeID,
			PublicKey: p.PublicKey,
			Addresses: p.Addresses,
			Label:     p.Label,
			UpdatedAt: tx.Timestamp,
		}
		delete(st.Tombstones, MemberTomb(p.NodeID))
		return nil

	case MsgLeaveMember:
		var p MemberPayload
		if err := json.Unmarshal(tx.Payload, &p); err != nil {
			return fmt.Errorf("member payload: %w", err)
		}
		if p.NodeID == "" {
			return fmt.Errorf("member node_id is required")
		}
		rec, existed := st.Members[p.NodeID]
		if !existed {
			// Idempotent leave: still stamp tombstone so AppHash reflects the intent.
			st.Tombstones[MemberTomb(p.NodeID)] = tx.Timestamp
			return nil
		}
		before := MembershipPower(st.Members)
		delete(st.Members, p.NodeID)
		after := MembershipPower(st.Members)
		// Refuse emptying the usable validator set (would brick CometBFT).
		if len(before) > 0 && len(after) == 0 {
			st.Members[p.NodeID] = rec
			return fmt.Errorf("%w: cannot leave the last validator (node_id=%s)", ErrLastValidator, p.NodeID)
		}
		st.Tombstones[MemberTomb(p.NodeID)] = tx.Timestamp
		return nil

	default:
		return fmt.Errorf("unsupported tx type %s", tx.Type)
	}
}

// ErrLastValidator is returned when LeaveMember would remove the last usable
// validator (MembershipPower would become empty). CometBFT cannot make progress
// with an empty validator set.
var ErrLastValidator = fmt.Errorf("last validator")

// MembershipPower returns pubkey-hex → equal voting power for members with
// usable 32-byte Ed25519 public keys (same mapping CometBFT validators use).
func MembershipPower(members map[string]MemberRecord) map[string]int64 {
	const power int64 = 10 // keep in sync with cometbft.DefaultValidatorPower
	out := make(map[string]int64)
	for _, m := range members {
		if m.PublicKey == "" {
			continue
		}
		// NodeID is hex(pubkey); PublicKey must be 64 hex chars (32 bytes).
		if len(m.PublicKey) != 64 {
			continue
		}
		out[m.PublicKey] = power
	}
	return out
}

// CanLeaveMember reports whether leaving nodeID is safe given current members.
// Returns nil if leave is allowed (including idempotent leave of an unknown id).
func CanLeaveMember(members map[string]MemberRecord, nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("member node_id is required")
	}
	if _, ok := members[nodeID]; !ok {
		return nil
	}
	before := MembershipPower(members)
	next := make(map[string]MemberRecord, len(members))
	for k, v := range members {
		if k == nodeID {
			continue
		}
		next[k] = v
	}
	after := MembershipPower(next)
	if len(before) > 0 && len(after) == 0 {
		return fmt.Errorf("%w: cannot leave the last validator (node_id=%s)", ErrLastValidator, nodeID)
	}
	return nil
}
