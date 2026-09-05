package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MockRuntime is an in-memory runtime for development and testing.
// It does not require containerd or root privileges.
type MockRuntime struct {
	mu         sync.RWMutex
	containers map[string]*ContainerInfo
	images     map[string]*ImageInfo // digest -> info
}

// NewMockRuntime creates a ready-to-use mock.
func NewMockRuntime() *MockRuntime {
	return &MockRuntime{
		containers: make(map[string]*ContainerInfo),
		images:     make(map[string]*ImageInfo),
	}
}

func (m *MockRuntime) List(ctx context.Context) ([]ContainerInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ContainerInfo, 0, len(m.containers))
	for _, c := range m.containers {
		out = append(out, *c)
	}
	return out, nil
}

func (m *MockRuntime) Get(ctx context.Context, id string) (*ContainerInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.containers[id]
	if !ok {
		return nil, fmt.Errorf("container %q not found", id)
	}
	cp := *c
	return &cp, nil
}

func (m *MockRuntime) Start(ctx context.Context, opts StartOptions) (*ContainerInfo, error) {
	if opts.ImageRef == "" {
		return nil, fmt.Errorf("image_ref is required")
	}

	// Auto-register a fake image if needed
	img, err := m.VerifyImage(ctx, opts.ImageRef)
	if err != nil {
		img, err = m.Pull(ctx, opts.ImageRef)
		if err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := opts.ID
	if id == "" {
		id = uuid.New().String()
	} else if _, exists := m.containers[id]; exists {
		return nil, fmt.Errorf("container %q already exists", id)
	}
	name := opts.Name
	if name == "" {
		if len(id) >= 8 {
			name = id[:8]
		} else {
			name = id
		}
	}

	now := time.Now().UTC()
	c := &ContainerInfo{
		ID:          id,
		Name:        name,
		ImageRef:    opts.ImageRef,
		ImageDigest: img.Digest,
		State:       "running",
		CreatedAt:   now,
		Labels:      opts.Labels,
	}
	m.containers[id] = c
	cp := *c
	return &cp, nil
}


func (m *MockRuntime) Stop(ctx context.Context, id string, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.containers[id]
	if !ok {
		return fmt.Errorf("container %q not found", id)
	}
	c.State = "stopped"
	return nil
}

func (m *MockRuntime) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.containers[id]; !ok {
		return fmt.Errorf("container %q not found", id)
	}
	delete(m.containers, id)
	return nil
}

func (m *MockRuntime) VerifyImage(ctx context.Context, ref string) (*ImageInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Accept either a known digest or a previously pulled ref
	if info, ok := m.images[ref]; ok {
		return info, nil
	}
	for _, info := range m.images {
		if info.Ref == ref || info.Digest == ref {
			return info, nil
		}
	}
	return nil, fmt.Errorf("image %q not found (use Pull first in mock mode)", ref)
}

func (m *MockRuntime) Pull(ctx context.Context, ref string) (*ImageInfo, error) {
	// Generate a deterministic fake digest from the ref
	h := sha256.Sum256([]byte("nexusos-mock:" + ref))
	digest := "sha256:" + hex.EncodeToString(h[:])

	info := &ImageInfo{
		Digest: digest,
		Size:   42 * 1024 * 1024, // pretend 42 MiB
		Ref:    ref,
	}

	m.mu.Lock()
	m.images[digest] = info
	m.images[ref] = info
	m.mu.Unlock()

	return info, nil
}

func (m *MockRuntime) Close() error {
	return nil
}
