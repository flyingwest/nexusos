package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMockCheckpointRestore(t *testing.T) {
	m := NewMockRuntime()
	ctx := context.Background()
	info, err := m.Start(ctx, StartOptions{ID: "c1", ImageRef: "alpine:test", Labels: map[string]string{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	art, err := m.Checkpoint(ctx, "c1", filepath.Join(dir, "dump"))
	if err != nil {
		t.Fatal(err)
	}
	if art.Hash == "" || art.Meta.ContainerID != "c1" {
		t.Fatalf("artifact: %+v", art)
	}
	got, err := m.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "stopped" {
		t.Fatalf("expected stopped after cold checkpoint, got %s", got.State)
	}
	_ = m.Remove(ctx, "c1")

	restored, err := m.Restore(ctx, RestoreOptions{ID: "c1", CheckpointDir: art.Dir, Meta: art.Meta})
	if err != nil {
		t.Fatal(err)
	}
	if restored.State != "running" || restored.ImageDigest != info.ImageDigest {
		t.Fatalf("restored: %+v", restored)
	}
}

func TestContainerdCRIUCapabilityAbsent(t *testing.T) {
	// Without a real containerd client we only assert the helper vars exist and
	// criuAvailable is false when LookPath fails.
	oldLook := execLookPath
	oldCmd := execCommand
	defer func() { execLookPath = oldLook; execCommand = oldCmd }()
	execLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if criuAvailable() {
		t.Fatal("expected criu unavailable")
	}
}
