package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nexusos/coordination/internal/ledger"
)

type workloadRequest struct {
	WorkloadID  string              `json:"workload_id"`
	ImageDigest string              `json:"image_digest"`
	ImageRef    string              `json:"image_ref"`
	Replicas    uint32              `json:"replicas"`
	Resources   ledger.ResourceSpec `json:"resources"`
	Strategy    string              `json:"strategy"`
	Labels      map[string]string   `json:"labels"`
}

type scaleRequest struct {
	Replicas uint32 `json:"replicas"`
}

func (s *Server) handleListWorkloads(w http.ResponseWriter, r *http.Request) {
	st := s.ledger.Snapshot()
	list := make([]ledger.WorkloadRecord, 0, len(st.Workloads))
	for _, wl := range st.Workloads {
		list = append(list, enrichWorkloadStatus(wl, st))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workloads": list})
}

func (s *Server) handleGetWorkload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.ledger.Snapshot()
	wl, ok := st.Workloads[id]
	if !ok {
		writeError(w, http.StatusNotFound, "workload not found")
		return
	}
	writeJSON(w, http.StatusOK, enrichWorkloadStatus(wl, st))
}

func (s *Server) handleCreateWorkload(w http.ResponseWriter, r *http.Request) {
	var req workloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.WorkloadID == "" {
		writeError(w, http.StatusBadRequest, "workload_id is required")
		return
	}
	if req.ImageDigest == "" && req.ImageRef == "" {
		writeError(w, http.StatusBadRequest, "image_digest or image_ref is required")
		return
	}
	if req.ImageDigest == "" && req.ImageRef != "" {
		info, err := s.rt.VerifyImage(r.Context(), req.ImageRef)
		if err != nil {
			info, err = s.rt.Pull(r.Context(), req.ImageRef)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "image not available: "+err.Error())
			return
		}
		req.ImageDigest = info.Digest
	}
	if s.requireImageDigest && !strings.HasPrefix(req.ImageDigest, "sha256:") {
		writeError(w, http.StatusBadRequest, "image_digest must be sha256:...")
		return
	}
	if req.Strategy == "" {
		req.Strategy = "Recreate"
	}
	payload := ledger.WorkloadPayload{
		WorkloadID:  req.WorkloadID,
		ImageDigest: req.ImageDigest,
		ImageRef:    req.ImageRef,
		Replicas:    req.Replicas,
		Resources:   req.Resources,
		Strategy:    req.Strategy,
		Labels:      req.Labels,
	}
	if err := s.commitWorkload(r.Context(), ledger.MsgCreateWorkload, payload); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// CometBFT Submit waits for commit; snapshot should include the workload.
	st := s.ledger.Snapshot()
	wl, ok := st.Workloads[req.WorkloadID]
	if !ok {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":      "submitted",
			"workload_id": req.WorkloadID,
			"replicas":    req.Replicas,
		})
		return
	}
	writeJSON(w, http.StatusCreated, enrichWorkloadStatus(wl, st))
}

func (s *Server) handleDeleteWorkload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.commitWorkload(r.Context(), ledger.MsgDeleteWorkload, ledger.DeleteWorkloadPayload{WorkloadID: id}); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "workload_id": id})
}

func (s *Server) handleScaleWorkload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req scaleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, ok := s.ledger.GetWorkload(id); !ok {
		writeError(w, http.StatusNotFound, "workload not found")
		return
	}
	if err := s.commitWorkload(r.Context(), ledger.MsgScaleWorkload, ledger.ScaleWorkloadPayload{
		WorkloadID: id,
		Replicas:   req.Replicas,
	}); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	st := s.ledger.Snapshot()
	wl, ok := st.Workloads[id]
	if !ok {
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "submitted", "workload_id": id, "replicas": req.Replicas})
		return
	}
	writeJSON(w, http.StatusOK, enrichWorkloadStatus(wl, st))
}

func (s *Server) commitWorkload(ctx context.Context, typ ledger.MessageType, payload any) error {
	tx, err := ledger.NewTx(s.kp, typ, payload, time.Now().UTC())
	if err != nil {
		return err
	}
	if s.txSubmitter != nil {
		return s.txSubmitter.Submit(ctx, tx)
	}
	return s.ledger.ApplyTxs([]ledger.Tx{tx})
}

func enrichWorkloadStatus(wl ledger.WorkloadRecord, st ledger.State) ledger.WorkloadRecord {
	var current, available uint32
	for _, c := range st.Containers {
		if c.WorkloadID != wl.WorkloadID {
			continue
		}
		current++
		if c.Desired == "Running" && c.CurrentNode != "" {
			available++
		}
	}
	wl.Status.Desired = wl.Replicas
	wl.Status.Current = current
	wl.Status.Available = available
	return wl
}
