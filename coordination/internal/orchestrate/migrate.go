package orchestrate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/runtime"
)

// MigrationController coordinates cold migration as a ledger transaction:
// propose → checkpoint (source) → transfer artifact → restore (dest) → complete|fail.
type MigrationController struct {
	KP          *identity.KeyPair
	Ledger      *ledger.Store
	Runtime     runtime.Runtime
	Submit      TxSubmitter
	NodeID      string
	Advertise   string
	ArtifactDir string
	HTTP        *http.Client
	APIToken    string
}

// MigrateRequest is the operator input for a cold move.
type MigrateRequest struct {
	ContainerID string
	ToNode      string
	FromNode    string // optional; defaults to container.CurrentNode
	MigrationID string // optional; generated if empty
}

// MigrateResult is returned after the controller finishes (success or fail tx).
type MigrateResult struct {
	Migration ledger.MigrationRecord `json:"migration"`
	Error     string                 `json:"error,omitempty"`
}

// ArtifactBundle is the wire format for transferring a checkpoint between nodes.
type ArtifactBundle struct {
	MigrationID    string                 `json:"migration_id"`
	CheckpointHash string                 `json:"checkpoint_hash"`
	Meta           runtime.CheckpointMeta `json:"meta"`
	// Files maps relative path → base64 content (mock dumps are tiny).
	Files map[string]string `json:"files"`
}

// Run executes a full cold migration. On any step failure after Propose it
// submits FailMigration (rollback basics: Desired=Running on from_node).
func (m *MigrationController) Run(ctx context.Context, req MigrateRequest) (*MigrateResult, error) {
	if m == nil || m.Ledger == nil || m.KP == nil || m.Submit == nil {
		return nil, fmt.Errorf("migration controller not configured")
	}
	st := m.Ledger.Snapshot()
	ctr, ok := st.Containers[req.ContainerID]
	if !ok {
		return nil, fmt.Errorf("container %s not found on ledger", req.ContainerID)
	}
	from := req.FromNode
	if from == "" {
		from = ctr.CurrentNode
	}
	if from == "" {
		return nil, fmt.Errorf("container %s has no current_node", req.ContainerID)
	}
	if req.ToNode == "" {
		return nil, fmt.Errorf("to_node is required")
	}
	if from == req.ToNode {
		return nil, fmt.Errorf("already on node %s", req.ToNode)
	}
	if node, ok := st.Nodes[req.ToNode]; !ok || node.Status == ledger.StatusOffline {
		// Allow missing liveness record if destination is a known member.
		if _, memOK := st.Members[req.ToNode]; !memOK {
			if !ok {
				return nil, fmt.Errorf("destination node %s unknown", req.ToNode)
			}
		}
	}

	migID := req.MigrationID
	if migID == "" {
		migID = "mig-" + uuid.New().String()
	}
	now := time.Now().UTC()
	if err := m.submit(ctx, ledger.MsgProposeMigration, ledger.ProposeMigrationPayload{
		MigrationID: migID,
		ContainerID: req.ContainerID,
		FromNode:    from,
		ToNode:      req.ToNode,
	}, now); err != nil {
		return nil, fmt.Errorf("propose: %w", err)
	}

	result := &MigrateResult{}
	hash, err := m.checkpointAndRestore(ctx, migID, req.ContainerID, from, req.ToNode, ctr)
	if err != nil {
		_ = m.submit(ctx, ledger.MsgFailMigration, ledger.FailMigrationPayload{
			MigrationID: migID,
			Reason:      err.Error(),
		}, time.Now().UTC())
		st2 := m.Ledger.Snapshot()
		result.Migration = st2.Migrations[migID]
		result.Error = err.Error()
		return result, fmt.Errorf("migration failed: %w", err)
	}

	if err := m.submit(ctx, ledger.MsgCompleteMigration, ledger.CompleteMigrationPayload{
		MigrationID:    migID,
		CheckpointHash: hash,
	}, time.Now().UTC()); err != nil {
		_ = m.submit(ctx, ledger.MsgFailMigration, ledger.FailMigrationPayload{
			MigrationID: migID,
			Reason:      "complete tx: " + err.Error(),
		}, time.Now().UTC())
		return nil, fmt.Errorf("complete: %w", err)
	}
	st2 := m.Ledger.Snapshot()
	result.Migration = st2.Migrations[migID]
	return result, nil
}

func (m *MigrationController) checkpointAndRestore(ctx context.Context, migID, containerID, from, to string, ctr ledger.ContainerRecord) (string, error) {
	dir := filepath.Join(m.artifactRoot(), migID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	var art *runtime.CheckpointArtifact
	var err error
	if from == m.NodeID {
		art, err = m.localCheckpoint(ctx, containerID, dir)
	} else {
		art, err = m.remoteCheckpoint(ctx, migID, containerID, from, dir)
	}
	if err != nil {
		return "", fmt.Errorf("checkpoint: %w", err)
	}

	bundle, err := packArtifact(migID, art)
	if err != nil {
		return "", fmt.Errorf("pack artifact: %w", err)
	}

	if to == m.NodeID {
		if err := m.localRestore(ctx, containerID, art, ctr); err != nil {
			return "", fmt.Errorf("restore: %w", err)
		}
	} else {
		if err := m.remoteRestore(ctx, to, bundle); err != nil {
			return "", fmt.Errorf("remote restore: %w", err)
		}
	}

	// After successful restore on dest, remove stopped source container locally if we are source.
	if from == m.NodeID {
		_ = m.Runtime.Remove(ctx, containerID)
	}
	return art.Hash, nil
}

func (m *MigrationController) localCheckpoint(ctx context.Context, id, destDir string) (*runtime.CheckpointArtifact, error) {
	cr, ok := runtime.AsCheckpointRestorer(m.Runtime)
	if !ok || !cr.SupportsCheckpointRestore() {
		return nil, runtime.ErrCheckpointUnsupported
	}
	return cr.Checkpoint(ctx, id, destDir)
}

func (m *MigrationController) localRestore(ctx context.Context, id string, art *runtime.CheckpointArtifact, ctr ledger.ContainerRecord) error {
	cr, ok := runtime.AsCheckpointRestorer(m.Runtime)
	if !ok || !cr.SupportsCheckpointRestore() {
		return runtime.ErrCheckpointUnsupported
	}
	meta := art.Meta
	if meta.ImageDigest == "" {
		meta.ImageDigest = ctr.ImageDigest
	}
	if meta.Labels == nil {
		meta.Labels = ctr.Labels
	}
	// Remove any stale local container with same id before restore.
	_ = m.Runtime.Stop(ctx, id, 3*time.Second)
	_ = m.Runtime.Remove(ctx, id)
	_, err := cr.Restore(ctx, runtime.RestoreOptions{
		ID:            id,
		CheckpointDir: art.Dir,
		Meta:          meta,
	})
	return err
}

func (m *MigrationController) remoteCheckpoint(ctx context.Context, migID, containerID, fromNode, destDir string) (*runtime.CheckpointArtifact, error) {
	url, err := m.peerURL(fromNode)
	if err != nil {
		return nil, err
	}
	body := map[string]string{"migration_id": migID, "container_id": containerID}
	var bundle ArtifactBundle
	if err := m.postJSON(ctx, url, "/v1/migrations/checkpoint", body, &bundle); err != nil {
		return nil, err
	}
	art, err := unpackArtifact(destDir, bundle)
	if err != nil {
		return nil, err
	}
	return art, nil
}

func (m *MigrationController) remoteRestore(ctx context.Context, toNode string, bundle ArtifactBundle) error {
	url, err := m.peerURL(toNode)
	if err != nil {
		return err
	}
	return m.postJSON(ctx, url, "/v1/migrations/restore", bundle, nil)
}

// LocalCheckpoint is the peer RPC handler: dump a local container for migration.
func (m *MigrationController) LocalCheckpoint(ctx context.Context, migID, containerID string) (*ArtifactBundle, error) {
	dir := filepath.Join(m.artifactRoot(), migID+"-src")
	art, err := m.localCheckpoint(ctx, containerID, dir)
	if err != nil {
		return nil, err
	}
	b, err := packArtifact(migID, art)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// LocalRestore is the peer RPC handler: restore from a transferred artifact.
func (m *MigrationController) LocalRestore(ctx context.Context, bundle ArtifactBundle) error {
	dir := filepath.Join(m.artifactRoot(), bundle.MigrationID+"-dst")
	art, err := unpackArtifact(dir, bundle)
	if err != nil {
		return err
	}
	st := m.Ledger.Snapshot()
	ctr := st.Containers[bundle.Meta.ContainerID]
	id := bundle.Meta.ContainerID
	if id == "" {
		return fmt.Errorf("bundle missing container_id")
	}
	return m.localRestore(ctx, id, art, ctr)
}

func (m *MigrationController) peerURL(nodeID string) (string, error) {
	st := m.Ledger.Snapshot()
	n, ok := st.Nodes[nodeID]
	if !ok || len(n.Addresses) == 0 {
		return "", fmt.Errorf("no advertise URL for node %s (need heartbeat sync)", nodeID)
	}
	return n.Addresses[0], nil
}

func (m *MigrationController) artifactRoot() string {
	if m.ArtifactDir != "" {
		return m.ArtifactDir
	}
	return filepath.Join(os.TempDir(), "nexusos-migrations")
}

func (m *MigrationController) submit(ctx context.Context, typ ledger.MessageType, payload any, now time.Time) error {
	tx, err := ledger.NewTx(m.KP, typ, payload, now)
	if err != nil {
		return err
	}
	return m.Submit.Submit(ctx, tx)
}

func (m *MigrationController) httpClient() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (m *MigrationController) postJSON(ctx context.Context, base, path string, in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stringsTrimSlash(base)+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIToken)
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s%s: %s", resp.StatusCode, base, path, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func stringsTrimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func packArtifact(migID string, art *runtime.CheckpointArtifact) (ArtifactBundle, error) {
	files := map[string]string{}
	entries, err := os.ReadDir(art.Dir)
	if err != nil {
		return ArtifactBundle{}, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(art.Dir, e.Name()))
		if err != nil {
			return ArtifactBundle{}, err
		}
		files[e.Name()] = base64.StdEncoding.EncodeToString(b)
	}
	return ArtifactBundle{
		MigrationID:    migID,
		CheckpointHash: art.Hash,
		Meta:           art.Meta,
		Files:          files,
	}, nil
}

func unpackArtifact(destDir string, bundle ArtifactBundle) (*runtime.CheckpointArtifact, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	for name, b64 := range bundle.Files {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(destDir, name), raw, 0o644); err != nil {
			return nil, err
		}
	}
	return &runtime.CheckpointArtifact{
		Hash: bundle.CheckpointHash,
		Dir:  destDir,
		Meta: bundle.Meta,
	}, nil
}

// SkipMigrating reports whether the reconciler should leave a container alone.
func SkipMigrating(c ledger.ContainerRecord, st ledger.State) bool {
	if c.Desired == ledger.DesiredMigrating {
		return true
	}
	for _, m := range st.Migrations {
		if m.ContainerID == c.ContainerID &&
			(m.Status == ledger.MigrationPending || m.Status == ledger.MigrationInProgress) {
			return true
		}
	}
	return false
}

// Log helper used by HTTP layer failures.
func LogMigration(migID string, err error) {
	if err != nil {
		log.Printf("migration %s: %v", migID, err)
	}
}
