package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/nexusos/coordination/internal/orchestrate"
)

type drainRequest struct {
	// Evacuate when true cold-migrates standalone containers and lists workload
	// replicas for reconciler reschedule. Default true for POST .../drain.
	Evacuate *bool `json:"evacuate,omitempty"`
}

func (s *Server) handleCordonNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := orchestrate.Cordon(s.ledger, id)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":               "cordoned",
		"node":                 n,
		"scheduling_disabled":  true,
		"note":                 "Status=Draining; scheduler skips this node for new placements",
	})
}

func (s *Server) handleUncordonNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := orchestrate.Uncordon(s.ledger, id)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "uncordoned",
		"node":                n,
		"scheduling_disabled": false,
	})
}

func (s *Server) handleDrainNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	evacuate := true
	if r.ContentLength != 0 {
		var req drainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.Evacuate != nil {
			evacuate = *req.Evacuate
		}
	}
	res, err := orchestrate.Drain(r.Context(), s.ledger, s.migrator, id, evacuate)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	status := http.StatusOK
	if len(res.Errors) > 0 {
		status = http.StatusMultiStatus // 207 — partial evacuate
	}
	writeJSON(w, status, map[string]any{
		"status":              "draining",
		"scheduling_disabled": true,
		"result":              res,
		"note":                "Workload replicas reschedule via reconciler; standalone use cold migrate (no live migration)",
	})
}
