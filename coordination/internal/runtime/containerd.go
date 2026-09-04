package runtime

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
)

// ContainerdRuntime talks to a real containerd daemon.
type ContainerdRuntime struct {
	client    *containerd.Client
	namespace string
}

// NewContainerdRuntime connects to the given containerd socket.
func NewContainerdRuntime(socket, namespace string) (*ContainerdRuntime, error) {
	if namespace == "" {
		namespace = "nexusos"
	}
	client, err := containerd.New(socket)
	if err != nil {
		return nil, fmt.Errorf("connect to containerd at %s: %w", socket, err)
	}
	return &ContainerdRuntime{
		client:    client,
		namespace: namespace,
	}, nil
}

func (c *ContainerdRuntime) withNS(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, c.namespace)
}

func (c *ContainerdRuntime) List(ctx context.Context) ([]ContainerInfo, error) {
	ctx = c.withNS(ctx)
	containers, err := c.client.Containers(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]ContainerInfo, 0, len(containers))
	for _, ctr := range containers {
		info, err := c.containerInfo(ctx, ctr)
		if err != nil {
			continue
		}
		out = append(out, *info)
	}
	return out, nil
}

func (c *ContainerdRuntime) Get(ctx context.Context, id string) (*ContainerInfo, error) {
	ctx = c.withNS(ctx)
	ctr, err := c.client.LoadContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("container %q not found: %w", id, err)
	}
	return c.containerInfo(ctx, ctr)
}

func (c *ContainerdRuntime) containerInfo(ctx context.Context, ctr containerd.Container) (*ContainerInfo, error) {
	info, err := ctr.Info(ctx)
	if err != nil {
		return nil, err
	}

	state := "created"
	task, err := ctr.Task(ctx, nil)
	if err == nil {
		st, err := task.Status(ctx)
		if err == nil {
			switch st.Status {
			case containerd.Running:
				state = "running"
			case containerd.Stopped, containerd.Created:
				state = string(st.Status)
			default:
				state = string(st.Status)
			}
		}
	}

	digest := ""
	if info.Image != "" {
		img, err := c.client.GetImage(ctx, info.Image)
		if err == nil {
			digest = img.Target().Digest.String()
		}
	}

	labels := info.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	return &ContainerInfo{
		ID:          info.ID,
		Name:        labels["io.nexusos/name"],
		ImageRef:    info.Image,
		ImageDigest: digest,
		State:       state,
		CreatedAt:   info.CreatedAt,
		Labels:      labels,
	}, nil
}

func (c *ContainerdRuntime) Start(ctx context.Context, opts StartOptions) (*ContainerInfo, error) {
	ctx = c.withNS(ctx)

	if opts.ImageRef == "" {
		return nil, fmt.Errorf("image_ref is required")
	}

	// Ensure image is present
	img, err := c.client.GetImage(ctx, opts.ImageRef)
	if err != nil {
		img, err = c.client.Pull(ctx, opts.ImageRef, containerd.WithPullUnpack)
		if err != nil {
			return nil, fmt.Errorf("pull image %q: %w", opts.ImageRef, err)
		}
	}

	id := uuid.New().String()
	name := opts.Name
	if name == "" {
		name = id[:8]
	}

	labels := map[string]string{
		"io.nexusos/name": name,
	}
	for k, v := range opts.Labels {
		labels[k] = v
	}

	specOpts := []oci.SpecOpts{
		oci.WithImageConfig(img),
		
	}
	if len(opts.Command) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(append(opts.Command, opts.Args...)...))
	}
	if opts.MemoryLimit > 0 {
		specOpts = append(specOpts, oci.WithMemoryLimit(uint64(opts.MemoryLimit)))
	}

	ctr, err := c.client.NewContainer(ctx, id,
		containerd.WithImage(img),
		containerd.WithNewSnapshot(id+"-snap", img),
		containerd.WithNewSpec(specOpts...),
		containerd.WithContainerLabels(labels),
	)
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	task, err := ctr.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		_ = ctr.Delete(ctx, containerd.WithSnapshotCleanup)
		return nil, fmt.Errorf("create task: %w", err)
	}

	if err := task.Start(ctx); err != nil {
		_, _ = task.Delete(ctx)
		_ = ctr.Delete(ctx, containerd.WithSnapshotCleanup)
		return nil, fmt.Errorf("start task: %w", err)
	}

	return c.containerInfo(ctx, ctr)
}

func (c *ContainerdRuntime) Stop(ctx context.Context, id string, timeout time.Duration) error {
	ctx = c.withNS(ctx)
	ctr, err := c.client.LoadContainer(ctx, id)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", id, err)
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		return nil // already stopped / no task
	}

	exitStatusC, err := task.Wait(ctx)
	if err != nil {
		return err
	}

	if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
		// force kill
		_ = task.Kill(ctx, syscall.SIGKILL)
	}

	select {
	case <-exitStatusC:
	case <-time.After(timeout):
		_ = task.Kill(ctx, syscall.SIGKILL)
		<-exitStatusC
	}

	_, _ = task.Delete(ctx)
	return nil
}

func (c *ContainerdRuntime) Remove(ctx context.Context, id string) error {
	ctx = c.withNS(ctx)
	ctr, err := c.client.LoadContainer(ctx, id)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", id, err)
	}

	// Ensure stopped
	_ = c.Stop(ctx, id, 5*time.Second)

	return ctr.Delete(ctx, containerd.WithSnapshotCleanup)
}

func (c *ContainerdRuntime) VerifyImage(ctx context.Context, ref string) (*ImageInfo, error) {
	ctx = c.withNS(ctx)
	img, err := c.client.GetImage(ctx, ref)
	if err != nil {
		// try as pure digest
		if strings.HasPrefix(ref, "sha256:") {
			images, err2 := c.client.ListImages(ctx)
			if err2 != nil {
				return nil, err
			}
			for _, i := range images {
				if i.Target().Digest.String() == ref {
					img = i
					err = nil
					break
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("image %q not found: %w", ref, err)
		}
	}

	size, _ := img.Size(ctx)
	return &ImageInfo{
		Digest: img.Target().Digest.String(),
		Size:   size,
		Ref:    img.Name(),
	}, nil
}

func (c *ContainerdRuntime) Pull(ctx context.Context, ref string) (*ImageInfo, error) {
	ctx = c.withNS(ctx)
	img, err := c.client.Pull(ctx, ref, containerd.WithPullUnpack)
	if err != nil {
		return nil, fmt.Errorf("pull %q: %w", ref, err)
	}
	size, _ := img.Size(ctx)
	return &ImageInfo{
		Digest: img.Target().Digest.String(),
		Size:   size,
		Ref:    img.Name(),
	}, nil
}

func (c *ContainerdRuntime) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Ensure specs import is used (for future resource limits)
var _ = specs.LinuxResources{}
