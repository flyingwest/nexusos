package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

type kv struct {
	K string `json:"k"`
	V string `json:"v"`
}

type canonNode struct {
	NodeID        string   `json:"node_id"`
	PublicKey     string   `json:"public_key"`
	Addresses     []string `json:"addresses"`
	CapacityCPU   uint32   `json:"capacity_cpu_millis"`
	CapacityMem   uint64   `json:"capacity_memory_bytes"`
	Labels        []kv     `json:"labels,omitempty"`
	Status        string   `json:"status"`
	LastHeartbeat string   `json:"last_heartbeat"`
	Mode          string   `json:"mode"`
}

type canonImage struct {
	Digest     string   `json:"digest"`
	Size       int64    `json:"size"`
	VerifiedBy []string `json:"verified_by"`
	FirstSeen  string   `json:"first_seen"`
}

type canonContainer struct {
	ContainerID string `json:"container_id"`
	ImageDigest string `json:"image_digest"`
	Owner       string `json:"owner,omitempty"`
	Desired     string `json:"desired_state"`
	CurrentNode string `json:"current_node"`
	Labels      []kv   `json:"labels,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type canonMigration struct {
	MigrationID    string `json:"migration_id"`
	ContainerID    string `json:"container_id"`
	FromNode       string `json:"from_node"`
	ToNode         string `json:"to_node"`
	Status         string `json:"status"`
	CheckpointHash string `json:"checkpoint_hash,omitempty"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at,omitempty"`
}

type canonTomb struct {
	Key string `json:"key"`
	At  string `json:"at"`
}

type canonState struct {
	Nodes      []canonNode      `json:"nodes"`
	Images     []canonImage     `json:"images"`
	Containers []canonContainer `json:"containers"`
	Migrations []canonMigration `json:"migrations"`
	Tombstones []canonTomb      `json:"tombstones"`
}

// CanonicalBytes returns a deterministic JSON encoding of State for signatures.
func CanonicalBytes(st State) ([]byte, error) {
	out := canonState{
		Nodes:      make([]canonNode, 0, len(st.Nodes)),
		Images:     make([]canonImage, 0, len(st.Images)),
		Containers: make([]canonContainer, 0, len(st.Containers)),
		Migrations: make([]canonMigration, 0, len(st.Migrations)),
		Tombstones: make([]canonTomb, 0, len(st.Tombstones)),
	}

	nodeIDs := sortedKeys(st.Nodes)
	for _, id := range nodeIDs {
		n := st.Nodes[id]
		addrs := append([]string(nil), n.Addresses...)
		sort.Strings(addrs)
		out.Nodes = append(out.Nodes, canonNode{
			NodeID:        n.NodeID,
			PublicKey:     n.PublicKey,
			Addresses:     addrs,
			CapacityCPU:   n.CapacityCPU,
			CapacityMem:   n.CapacityMem,
			Labels:        sortedLabels(n.Labels),
			Status:        n.Status,
			LastHeartbeat: canonTime(n.LastHeartbeat),
			Mode:          n.Mode,
		})
	}

	digests := sortedKeys(st.Images)
	for _, d := range digests {
		img := st.Images[d]
		vb := append([]string(nil), img.VerifiedBy...)
		sort.Strings(vb)
		out.Images = append(out.Images, canonImage{
			Digest:     img.Digest,
			Size:       img.Size,
			VerifiedBy: vb,
			FirstSeen:  canonTime(img.FirstSeen),
		})
	}

	cids := sortedKeys(st.Containers)
	for _, id := range cids {
		c := st.Containers[id]
		out.Containers = append(out.Containers, canonContainer{
			ContainerID: c.ContainerID,
			ImageDigest: c.ImageDigest,
			Owner:       c.Owner,
			Desired:     c.Desired,
			CurrentNode: c.CurrentNode,
			Labels:      sortedLabels(c.Labels),
			UpdatedAt:   canonTime(c.UpdatedAt),
		})
	}

	mids := sortedKeys(st.Migrations)
	for _, id := range mids {
		m := st.Migrations[id]
		finished := ""
		if m.FinishedAt != nil {
			finished = canonTime(*m.FinishedAt)
		}
		out.Migrations = append(out.Migrations, canonMigration{
			MigrationID:    m.MigrationID,
			ContainerID:    m.ContainerID,
			FromNode:       m.FromNode,
			ToNode:         m.ToNode,
			Status:         m.Status,
			CheckpointHash: m.CheckpointHash,
			StartedAt:      canonTime(m.StartedAt),
			FinishedAt:     finished,
		})
	}

	tkeys := make([]string, 0, len(st.Tombstones))
	for k := range st.Tombstones {
		tkeys = append(tkeys, k)
	}
	sort.Strings(tkeys)
	for _, k := range tkeys {
		out.Tombstones = append(out.Tombstones, canonTomb{Key: k, At: canonTime(st.Tombstones[k])})
	}

	return json.Marshal(out)
}

// CanonicalHash is sha256 of CanonicalBytes, hex-encoded.
func CanonicalHash(st State) (string, error) {
	b, err := CanonicalBytes(st)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func canonTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
}

func sortedLabels(m map[string]string) []kv {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		out = append(out, kv{K: k, V: m[k]})
	}
	return out
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
