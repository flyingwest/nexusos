package version

import (
	"strings"
	"testing"
)

func TestGetDefaults(t *testing.T) {
	info := Get()
	if info.Version == "" {
		t.Fatal("version empty")
	}
	if !strings.HasPrefix(info.GoVersion, "go") {
		t.Fatalf("go_version: %q", info.GoVersion)
	}
}
