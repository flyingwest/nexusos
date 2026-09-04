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

	default:
		return fmt.Errorf("unsupported tx type %s", tx.Type)
	}
}
