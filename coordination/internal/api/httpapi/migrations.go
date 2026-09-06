package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/orchestrate"
)

func (s *Server) handleListMigrations(w http.ResponseWriter, r *http.Request) {
	st := s.ledger.Snapshot()
	list := make([]ledger.MigrationRecord, 0, len(st.Migrations))
	for _, m := range st.Migrations {
		list = append(list, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"migrations": list})
}

func (s *Server) handleGetMigration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.ledger.Snapshot()
	m, ok := st.Migrations[id]
	if !ok {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

type migrateRequest struct {
	ContainerID string `json:"container_id"`
	ToNode      string `json:"to_node"`
	FromNode    string `json:"from_node,omitempty"`
	MigrationID string `json:"migration_id,omitempty"`
}

func (s *Server) handleCreateMigration(w http.ResponseWriter, r *http.Request) {
	if s.migrator == nil {
		writeError(w, http.StatusServiceUnavailable, "migration controller not configured")
		return
	}
	var req migrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.ContainerID == "" || req.ToNode == "" {
		writeError(w, http.StatusBadRequest, "container_id and to_node are required")
		return
	}
	ctx := r.Context()
	res, err := s.migrator.Run(ctx, orchestrate.MigrateRequest{
		ContainerID: req.ContainerID,
		ToNode:      req.ToNode,
		FromNode:    req.FromNode,
		MigrationID: req.MigrationID,
	})
	if err != nil {
		if res != nil && res.Migration.MigrationID != "" {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     err.Error(),
				"migration": res.Migration,
			})
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res.Migration)
}

func (s *Server) handleMigrationCheckpoint(w http.ResponseWriter, r *http.Request) {
	if s.migrator == nil {
		writeError(w, http.StatusServiceUnavailable, "migration controller not configured")
		return
	}
	var req struct {
		MigrationID string `json:"migration_id"`
		ContainerID string `json:"container_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.MigrationID == "" || req.ContainerID == "" {
		writeError(w, http.StatusBadRequest, "migration_id and container_id required")
		return
	}
	bundle, err := s.migrator.LocalCheckpoint(r.Context(), req.MigrationID, req.ContainerID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) handleMigrationRestore(w http.ResponseWriter, r *http.Request) {
	if s.migrator == nil {
		writeError(w, http.StatusServiceUnavailable, "migration controller not configured")
		return
	}
	var bundle orchestrate.ArtifactBundle
	if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.migrator.LocalRestore(r.Context(), bundle); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored", "migration_id": bundle.MigrationID})
}
