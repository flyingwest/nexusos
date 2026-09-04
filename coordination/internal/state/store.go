package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is a simple durable local state store for Phase 1.
// Later this will be synchronized with the network ledger.
type Store struct {
	mu       sync.RWMutex
	path     string
	nodeID   string
	data     *Data
}

// Data is the persistent snapshot.
type Data struct {
	NodeID     string                     `json:"node_id"`
	UpdatedAt  time.Time                  `json:"updated_at"`
	Containers map[string]ContainerRecord `json:"containers"`
	Images     map[string]ImageRecord     `json:"images"`
	Workloads  map[string]WorkloadRecord  `json:"workloads"`
}

// ContainerRecord is the local view of a container.
type ContainerRecord struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	ImageDigest string            `json:"image_digest"`
	ImageRef    string            `json:"image_ref,omitempty"`
	State       string            `json:"state"`
	Desired     string            `json:"desired"` // Running, Stopped, Migrating
	WorkloadID  string            `json:"workload_id,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ImageRecord tracks a known/verified image.
type ImageRecord struct {
	Digest     string    `json:"digest"`
	Size       int64     `json:"size"`
	Ref        string    `json:"ref,omitempty"`
	Verified   bool      `json:"verified"`
	VerifiedAt time.Time `json:"verified_at"`
}

// WorkloadRecord is a placeholder for Phase 3.
type WorkloadRecord struct {
	ID          string    `json:"id"`
	ImageDigest string    `json:"image_digest"`
	Replicas    uint32    `json:"replicas"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NewStore opens or creates a store at dataDir/state.json.
func NewStore(dataDir, nodeID string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	path := filepath.Join(dataDir, "state.json")
	s := &Store{
		path:   path,
		nodeID: nodeID,
		data: &Data{
			NodeID:     nodeID,
			Containers: make(map[string]ContainerRecord),
			Images:     make(map[string]ImageRecord),
			Workloads:  make(map[string]WorkloadRecord),
		},
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
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	if d.Containers == nil {
		d.Containers = make(map[string]ContainerRecord)
	}
	if d.Images == nil {
		d.Images = make(map[string]ImageRecord)
	}
	if d.Workloads == nil {
		d.Workloads = make(map[string]WorkloadRecord)
	}
	s.data = &d
	return nil
}

func (s *Store) save() error {
	s.data.UpdatedAt = time.Now().UTC()
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

// Snapshot returns a copy of the current data.
func (s *Store) Snapshot() Data {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Deep-ish copy for safety
	out := Data{
		NodeID:     s.data.NodeID,
		UpdatedAt:  s.data.UpdatedAt,
		Containers: make(map[string]ContainerRecord, len(s.data.Containers)),
		Images:     make(map[string]ImageRecord, len(s.data.Images)),
		Workloads:  make(map[string]WorkloadRecord, len(s.data.Workloads)),
	}
	for k, v := range s.data.Containers {
		out.Containers[k] = v
	}
	for k, v := range s.data.Images {
		out.Images[k] = v
	}
	for k, v := range s.data.Workloads {
		out.Workloads[k] = v
	}
	return out
}

// UpsertContainer records or updates a container.
func (s *Store) UpsertContainer(rec ContainerRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.UpdatedAt = time.Now().UTC()
	s.data.Containers[rec.ID] = rec
	return s.save()
}

// DeleteContainer removes a container record.
func (s *Store) DeleteContainer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Containers, id)
	return s.save()
}

// UpsertImage records a verified image.
func (s *Store) UpsertImage(rec ImageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Images[rec.Digest] = rec
	return s.save()
}

// GetContainer returns a container record if present.
func (s *Store) GetContainer(id string) (ContainerRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.data.Containers[id]
	return r, ok
}

// ListContainers returns all container records.
func (s *Store) ListContainers() []ContainerRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ContainerRecord, 0, len(s.data.Containers))
	for _, r := range s.data.Containers {
		out = append(out, r)
	}
	return out
}

// ListImages returns all image records.
func (s *Store) ListImages() []ImageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ImageRecord, 0, len(s.data.Images))
	for _, r := range s.data.Images {
		out = append(out, r)
	}
	return out
}
