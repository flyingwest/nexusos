package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is a local persistence of network ledger state.
// In Phase 2 this will be driven by consensus; for now it is local-only.
type Store struct {
	mu   sync.RWMutex
	path string
	data *State
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "ledger.json")
	s := &Store{path: path, data: NewState()}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return err
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
	s.data = &st
	return nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// shallow copy maps
	out := State{
		Nodes:      make(map[string]NodeRecord, len(s.data.Nodes)),
		Images:     make(map[string]ImageRecord, len(s.data.Images)),
		Containers: make(map[string]ContainerRecord, len(s.data.Containers)),
		Migrations: make(map[string]MigrationRecord, len(s.data.Migrations)),
		Tombstones: make(map[string]time.Time, len(s.data.Tombstones)),
	}
	for k, v := range s.data.Nodes {
		out.Nodes[k] = v
	}
	for k, v := range s.data.Images {
		out.Images[k] = v
	}
	for k, v := range s.data.Containers {
		out.Containers[k] = v
	}
	for k, v := range s.data.Migrations {
		out.Migrations[k] = v
	}
	for k, v := range s.data.Tombstones {
		out.Tombstones[k] = v
	}
	return out
}

// GetNode returns a node record if present.
func (s *Store) GetNode(id string) (NodeRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.data.Nodes[id]
	return n, ok
}

// GetContainer returns a container record if present.
func (s *Store) GetContainer(id string) (ContainerRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data.Containers[id]
	return c, ok
}

// Merge unions remote state into the local ledger and persists it.
func (s *Store) Merge(remote State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	MergeState(s.data, remote)
	return s.save()
}

func (s *Store) UpsertNode(n NodeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.LastHeartbeat = time.Now().UTC()
	s.data.Nodes[n.NodeID] = n
	return s.save()
}

// SweepExpired marks nodes Offline when LastHeartbeat is older than timeout.
// It does not change LastHeartbeat, so a later live heartbeat still wins merge.
// selfID is never marked Offline.
func (s *Store) SweepExpired(now time.Time, timeout time.Duration, selfID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if timeout <= 0 {
		return nil, nil
	}
	var marked []string
	for id, n := range s.data.Nodes {
		if id == selfID {
			continue
		}
		if n.LastHeartbeat.IsZero() || n.Status == StatusOffline {
			continue
		}
		if now.Sub(n.LastHeartbeat) <= timeout {
			continue
		}
		n.Status = StatusOffline
		s.data.Nodes[id] = n
		marked = append(marked, id)
	}
	if len(marked) == 0 {
		return nil, nil
	}
	return marked, s.save()
}

// ListNodes returns all node records.
func (s *Store) ListNodes() []NodeRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]NodeRecord, 0, len(s.data.Nodes))
	for _, n := range s.data.Nodes {
		out = append(out, n)
	}
	return out
}

func (s *Store) UpsertImage(img ImageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.data.Images[img.Digest]; ok {
		// merge verified_by
		seen := map[string]bool{}
		for _, id := range existing.VerifiedBy {
			seen[id] = true
		}
		for _, id := range img.VerifiedBy {
			if !seen[id] {
				existing.VerifiedBy = append(existing.VerifiedBy, id)
			}
		}
		if img.Size > 0 {
			existing.Size = img.Size
		}
		s.data.Images[img.Digest] = existing
	} else {
		if img.FirstSeen.IsZero() {
			img.FirstSeen = time.Now().UTC()
		}
		s.data.Images[img.Digest] = img
	}
	return s.save()
}

func (s *Store) UpsertContainer(c ContainerRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.UpdatedAt = time.Now().UTC()
	delete(s.data.Tombstones, ContainerTomb(c.ContainerID))
	s.data.Containers[c.ContainerID] = c
	return s.save()
}

func (s *Store) RemoveContainer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Containers, id)
	s.data.Tombstones[ContainerTomb(id)] = time.Now().UTC()
	return s.save()
}

// ApplyTxs applies an ordered, already-verified batch and persists once.
func (s *Store) ApplyTxs(txs []Tx) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, tx := range txs {
		if err := ApplyTx(s.data, tx); err != nil {
			return fmt.Errorf("tx %d (%s): %w", i, tx.Type, err)
		}
	}
	return s.save()
}
