package membership

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Member is a known node in permissioned mode.
type Member struct {
	NodeID    string   `json:"node_id"`
	PublicKey string   `json:"public_key,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Label     string   `json:"label,omitempty"`
}

// Store holds the permissioned membership list.
type Store struct {
	mu      sync.RWMutex
	path    string
	members map[string]Member
}

// NewStore loads or creates a membership file.
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "members.json")
	s := &Store{
		path:    path,
		members: make(map[string]Member),
	}
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
	var list []Member
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("parse members: %w", err)
	}
	s.members = make(map[string]Member, len(list))
	for _, m := range list {
		s.members[m.NodeID] = m
	}
	return nil
}

func (s *Store) save() error {
	s.mu.RLock()
	list := make([]Member, 0, len(s.members))
	for _, m := range s.members {
		list = append(list, m)
	}
	s.mu.RUnlock()

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Upsert adds or updates a member.
func (s *Store) Upsert(m Member) error {
	if m.NodeID == "" {
		return fmt.Errorf("node_id is required")
	}
	s.mu.Lock()
	s.members[m.NodeID] = m
	s.mu.Unlock()
	return s.save()
}

// Remove deletes a member.
func (s *Store) Remove(nodeID string) error {
	s.mu.Lock()
	delete(s.members, nodeID)
	s.mu.Unlock()
	return s.save()
}

// List returns all members.
func (s *Store) List() []Member {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Member, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, m)
	}
	return out
}

// Contains reports whether nodeID is in the membership set.
// Empty membership = open (any node allowed) for easier single-node dev.
func (s *Store) Contains(nodeID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.members) == 0 {
		return true
	}
	_, ok := s.members[nodeID]
	return ok
}

// IsEmpty reports whether no members are configured.
func (s *Store) IsEmpty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.members) == 0
}

// Get returns a member by node id.
func (s *Store) Get(nodeID string) (Member, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[nodeID]
	return m, ok
}

// IDs returns sorted member node ids.
func (s *Store) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.members))
	for id := range s.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
