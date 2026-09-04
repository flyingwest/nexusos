package runtime

import (
	"context"
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
	Name        string
	ImageRef    string // prefer digest form: @sha256:...
	Labels      map[string]string
	Env         []string
	Command     []string
	Args        []string
	CPUShares   uint64
	MemoryLimit int64 // bytes
}
