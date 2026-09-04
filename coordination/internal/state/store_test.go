package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore_UpsertAndList(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "node-test")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().UTC()
	err = s.UpsertContainer(ContainerRecord{
		ID:          "c1",
		Name:        "web",
		ImageDigest: "sha256:abc",
		State:       "running",
		Desired:     "Running",
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert container: %v", err)
	}

	err = s.UpsertImage(ImageRecord{
		Digest:     "sha256:abc",
		Size:       1234,
		Verified:   true,
		VerifiedAt: now,
	})
	if err != nil {
		t.Fatalf("upsert image: %v", err)
	}

	// Reload from disk
	s2, err := NewStore(dir, "node-test")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	c, ok := s2.GetContainer("c1")
	if !ok || c.Name != "web" {
		t.Fatalf("container not persisted: ok=%v %+v", ok, c)
	}
	imgs := s2.ListImages()
	if len(imgs) != 1 || imgs[0].Digest != "sha256:abc" {
		t.Fatalf("images not persisted: %+v", imgs)
	}

	if err := s2.DeleteContainer("c1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s2.GetContainer("c1"); ok {
		t.Fatal("container should be gone")
	}

	// state file should exist
	if _, err := filepath.Glob(filepath.Join(dir, "state.json")); err != nil {
		t.Fatal(err)
	}
}
