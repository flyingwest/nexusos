package runtime

import (
	"context"
	"errors"
	"time"
)

// Runtime is the interface to the local container runtime.
// Implementations: ContainerdRuntime (real) and MockRuntime (development).
type Runtime interface {
	// List returns currently known containers.
	List(ctx context.Context) ([]ContainerInfo, error)

	// Get returns detailed info for one container.
	Get(ctx context.Context, id string) (*ContainerInfo, error)

	// Start creates and starts a container from an image reference (prefer digest).
	Start(ctx context.Context, opts StartOptions) (*ContainerInfo, error)

	// Stop stops a running container.
	Stop(ctx context.Context, id string, timeout time.Duration) error

	// Remove removes a stopped container.
	Remove(ctx context.Context, id string) error

	// VerifyImage checks that an image with the given digest (or reference) exists
	// and returns its resolved digest and size.
	VerifyImage(ctx context.Context, ref string) (*ImageInfo, error)

	// Pull pulls an image if needed and returns its digest info.
	Pull(ctx context.Context, ref string) (*ImageInfo, error)

	// Close releases resources.
	Close() error
}

// ContainerInfo describes a container.
type ContainerInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	ImageRef    string            `json:"image_ref"`
	ImageDigest string            `json:"image_digest"`
	State       string            `json:"state"` // created, running, stopped, unknown
	CreatedAt   time.Time         `json:"created_at"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// ImageInfo describes a verified image.
type ImageInfo struct {
	Digest string `json:"digest"` // sha256:...
	Size   int64  `json:"size"`
	Ref    string `json:"ref,omitempty"`
}

// StartOptions controls container creation.
type StartOptions struct {
	ID          string // optional predetermined id (workload replicas)
	Name        string
	ImageRef    string // prefer digest form: @sha256:...
	ImageDigest string // optional; when set, recorded on the running container (ledger alignment)
	Labels      map[string]string
	Env         []string
	Command     []string
	Args        []string
	CPUShares   uint64
	MemoryLimit int64 // bytes
}

// CheckpointMeta describes a cold-migration artifact (mock or CRIU dump).
type CheckpointMeta struct {
	ContainerID string            `json:"container_id"`
	Name        string            `json:"name,omitempty"`
	ImageRef    string            `json:"image_ref,omitempty"`
	ImageDigest string            `json:"image_digest,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	State       string            `json:"state,omitempty"`
}

// CheckpointArtifact is the on-disk (or in-memory) result of Checkpoint.
type CheckpointArtifact struct {
	Hash string         `json:"hash"` // sha256 hex of artifact bytes
	Dir  string         `json:"dir"`  // local directory containing the dump
	Meta CheckpointMeta `json:"meta"`
}

// RestoreOptions controls Restore from a cold checkpoint.
type RestoreOptions struct {
	ID            string // container id on the destination
	CheckpointDir string
	Meta          CheckpointMeta // required for mock; CRIU may re-derive
}

// ErrCheckpointUnsupported is returned when CRIU/checkpoint is unavailable.
var ErrCheckpointUnsupported = errors.New("checkpoint/restore not supported on this runtime")

// CheckpointRestorer is an optional capability for Phase 4 cold migration.
// MockRuntime always supports it; ContainerdRuntime supports it only when
// the host has a working CRIU binary (see SupportsCheckpointRestore).
type CheckpointRestorer interface {
	SupportsCheckpointRestore() bool
	Checkpoint(ctx context.Context, id string, destDir string) (*CheckpointArtifact, error)
	Restore(ctx context.Context, opts RestoreOptions) (*ContainerInfo, error)
}

// AsCheckpointRestorer returns the CheckpointRestorer if rt implements it.
func AsCheckpointRestorer(rt Runtime) (CheckpointRestorer, bool) {
	cr, ok := rt.(CheckpointRestorer)
	return cr, ok
}
