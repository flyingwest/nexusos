package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMockRuntime_PullVerifyStartStopRemove(t *testing.T) {
	ctx := context.Background()
	rt := NewMockRuntime()
	defer rt.Close()

	ref := "docker.io/library/nginx:alpine"

	// Pull
	img, err := rt.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if img.Digest == "" || !strings.HasPrefix(img.Digest, "sha256:") {
		t.Fatalf("expected sha256 digest, got %q", img.Digest)
	}

	// Verify
	v, err := rt.VerifyImage(ctx, ref)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.Digest != img.Digest {
		t.Fatalf("digest mismatch: %s vs %s", v.Digest, img.Digest)
	}

	// Start
	c, err := rt.Start(ctx, StartOptions{Name: "web", ImageRef: ref})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if c.State != "running" {
		t.Fatalf("expected running, got %s", c.State)
	}
	if c.ImageDigest != img.Digest {
		t.Fatalf("container digest mismatch")
	}

	// List
	list, err := rt.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(list))
	}

	// Get
	got, err := rt.Get(ctx, c.ID)
	if err != nil || got.ID != c.ID {
		t.Fatalf("get: %v", err)
	}

	// Stop
	if err := rt.Stop(ctx, c.ID, 2*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got, _ = rt.Get(ctx, c.ID)
	if got.State != "stopped" {
		t.Fatalf("expected stopped, got %s", got.State)
	}

	// Remove
	if err := rt.Remove(ctx, c.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	list, _ = rt.List(ctx)
	if len(list) != 0 {
		t.Fatalf("expected empty list after remove")
	}
}

func TestMockRuntime_StartRequiresImageRef(t *testing.T) {
	rt := NewMockRuntime()
	_, err := rt.Start(context.Background(), StartOptions{})
	if err == nil {
		t.Fatal("expected error when image_ref missing")
	}
}
