package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nexusos/coordination/internal/consensus"
	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/p2p"
	"github.com/nexusos/coordination/internal/runtime"
	"github.com/nexusos/coordination/internal/state"
)

// Server exposes the coordination HTTP API.
// TxSubmitter commits signed ledger txs (image/placement) through a consensus path.
type TxSubmitter interface {
	Submit(ctx context.Context, tx ledger.Tx) error
}

type Server struct {
	addr               string
	apiToken           string
	tlsCertFile        string
	tlsKeyFile         string
	requireImageDigest bool
	mode               string
	kp                 *identity.KeyPair
	rt                 runtime.Runtime
	store              *state.Store
	ledger             *ledger.Store
	members            *membership.Store
	engine             *p2p.Engine
	consensus          *consensus.Engine
	txSubmitter        TxSubmitter // when set (CometBFT), strict — no local upsert fallback
	joinToken          string
	cometbftHome       string
	cometbftP2P        string // tcp://host:port listen used for bootstrap peer string
	server             *http.Server
}

// Options configures the HTTP API.
type Options struct {
	Addr               string
	APIToken           string
	TLSCertFile        string
	TLSKeyFile         string
	RequireImageDigest bool
	Mode               string
	KeyPair            *identity.KeyPair
	Runtime            runtime.Runtime
	Store              *state.Store
	Ledger             *ledger.Store
	Members            *membership.Store
	Engine             *p2p.Engine
	Consensus          *consensus.Engine
	// TxSubmitter, when non-nil, handles image/placement commits strictly
	// (no silent local ledger upsert on failure). Used for CometBFT.
	TxSubmitter TxSubmitter
	// JoinToken authorizes GET /v1/cometbft/bootstrap (alternative to API token).
	JoinToken string
	// CometBFTHome / CometBFTP2P enable the shared-genesis bootstrap endpoint.
	CometBFTHome string
	CometBFTP2P  string
}

// New creates an HTTP API server.
func New(opts Options) *Server {
	s := &Server{
		addr:               opts.Addr,
		apiToken:           opts.APIToken,
		tlsCertFile:        opts.TLSCertFile,
		tlsKeyFile:         opts.TLSKeyFile,
		requireImageDigest: opts.RequireImageDigest,
		mode:               opts.Mode,
		kp:                 opts.KeyPair,
		rt:                 opts.Runtime,
		store:              opts.Store,
		ledger:             opts.Ledger,
		members:            opts.Members,
		engine:             opts.Engine,
		consensus:          opts.Consensus,
		txSubmitter:        opts.TxSubmitter,
		joinToken:          opts.JoinToken,
		cometbftHome:       opts.CometBFTHome,
		cometbftP2P:        opts.CometBFTP2P,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /v1/node", s.handleNode)
	mux.HandleFunc("GET /v1/containers", s.handleListContainers)
	mux.HandleFunc("GET /v1/containers/{id}", s.handleGetContainer)
	mux.HandleFunc("POST /v1/containers", s.handleStartContainer)
	mux.HandleFunc("POST /v1/containers/{id}/stop", s.handleStopContainer)
	mux.HandleFunc("DELETE /v1/containers/{id}", s.handleRemoveContainer)
	mux.HandleFunc("GET /v1/images", s.handleListImages)
	mux.HandleFunc("POST /v1/images/pull", s.handlePullImage)
	mux.HandleFunc("POST /v1/images/verify", s.handleVerifyImage)
	mux.HandleFunc("GET /v1/state", s.handleState)
	mux.HandleFunc("GET /v1/ledger", s.handleLedger)
	mux.HandleFunc("GET /v1/nodes", s.handleListNodes)
	mux.HandleFunc("GET /v1/members", s.handleListMembers)
	mux.HandleFunc("POST /v1/members", s.handleAddMember)
	mux.HandleFunc("DELETE /v1/members/{id}", s.handleRemoveMember)
	mux.HandleFunc("GET /v1/peers", s.handleListPeers)
	mux.HandleFunc("POST /v1/peers", s.handleAddPeer)
	mux.HandleFunc("POST /v1/peers/remove", s.handleRemovePeer)
	mux.HandleFunc("POST /v1/cluster/pair", s.handleClusterPair)
	mux.HandleFunc("POST /v1/cluster/leave", s.handleClusterLeave)
	mux.HandleFunc("POST /v1/cluster/sync", s.handleClusterSync)
	mux.HandleFunc("GET /v1/chain", s.handleChain)
	mux.HandleFunc("GET /v1/cometbft/bootstrap", s.handleCometBFTBootstrap)
	mux.HandleFunc("POST /v1/net/hello", s.handleNetHello)
	mux.HandleFunc("POST /v1/net/pair", s.handleNetPair)
	mux.HandleFunc("POST /v1/net/sync", s.handleNetSync)
	mux.HandleFunc("POST /v1/net/propose", s.handleNetPropose)
	mux.HandleFunc("POST /v1/net/commit", s.handleNetCommit)
	mux.HandleFunc("POST /v1/net/tx", s.handleNetTx)

	handler := withLogging(withAuth(opts.APIToken, mux))
	s.server = &http.Server{
		Addr:              opts.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	tlsOn := s.tlsCertFile != "" && s.tlsKeyFile != ""
	scheme := "http"
	if tlsOn {
		scheme = "https"
	}
	log.Printf("HTTP API listening on %s://%s (auth=%v tls=%v digest_required=%v)",
		scheme, s.addr, s.apiToken != "", tlsOn, s.requireImageDigest)
	if tlsOn {
		return s.server.ListenAndServeTLS(s.tlsCertFile, s.tlsKeyFile)
	}
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Handler returns the HTTP handler (used by tests).
func (s *Server) Handler() http.Handler {
	return s.server.Handler
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"node_id": s.kp.NodeID,
	})
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	pub := s.kp.PublicJSON()
	advertise := ""
	if s.engine != nil {
		advertise = s.engine.Advertise
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":    pub.NodeID,
		"public_key": pub.PublicKey,
		"mode":       s.mode,
		"advertise":  advertise,
		"role":       "coordinator",
		"phase":      "2-consensus",
		"consensus":  s.consensus != nil,
	})
}

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	list, err := s.rt.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": list})
}

func (s *Server) handleGetContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	info, err := s.rt.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type startRequest struct {
	Name     string            `json:"name"`
	ImageRef string            `json:"image_ref"`
	Labels   map[string]string `json:"labels"`
	Command  []string          `json:"command"`
	Args     []string          `json:"args"`
}

func (s *Server) handleStartContainer(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.ImageRef == "" {
		writeError(w, http.StatusBadRequest, "image_ref is required")
		return
	}
	if s.requireImageDigest && !strings.HasPrefix(req.ImageRef, "sha256:") && !strings.Contains(req.ImageRef, "@sha256:") {
		writeError(w, http.StatusBadRequest, "image_ref must be a digest when require_image_digest is enabled")
		return
	}

	if _, err := s.rt.VerifyImage(r.Context(), req.ImageRef); err != nil {
		if _, err := s.rt.Pull(r.Context(), req.ImageRef); err != nil {
			writeError(w, http.StatusBadRequest, "image not available: "+err.Error())
			return
		}
	}

	info, err := s.rt.Start(r.Context(), runtime.StartOptions{
		Name:     req.Name,
		ImageRef: req.ImageRef,
		Labels:   req.Labels,
		Command:  req.Command,
		Args:     req.Args,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = s.store.UpsertContainer(state.ContainerRecord{
		ID:          info.ID,
		Name:        info.Name,
		ImageDigest: info.ImageDigest,
		ImageRef:    info.ImageRef,
		State:       info.State,
		Desired:     "Running",
		Labels:      info.Labels,
		CreatedAt:   info.CreatedAt,
	})

	if info.ImageDigest != "" {
		_ = s.store.UpsertImage(state.ImageRecord{
			Digest:     info.ImageDigest,
			Ref:        info.ImageRef,
			Verified:   true,
			VerifiedAt: time.Now().UTC(),
		})
		s.commitImage(r.Context(), info.ImageDigest, 0)
		s.commitContainer(r.Context(), ledger.ContainerPayload{
			ContainerID: info.ID,
			ImageDigest: info.ImageDigest,
			Desired:     "Running",
			CurrentNode: s.kp.NodeID,
			Labels:      info.Labels,
		}, ledger.MsgCreateContainer)
	}

	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleStopContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.rt.Stop(r.Context(), id, 10*time.Second); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rec, ok := s.store.GetContainer(id); ok {
		rec.State = "stopped"
		rec.Desired = "Stopped"
		_ = s.store.UpsertContainer(rec)
	}
	if rec, ok := s.ledger.GetContainer(id); ok {
		s.commitContainer(r.Context(), ledger.ContainerPayload{
			ContainerID: rec.ContainerID,
			ImageDigest: rec.ImageDigest,
			Desired:     "Stopped",
			CurrentNode: rec.CurrentNode,
			Labels:      rec.Labels,
			Owner:       rec.Owner,
		}, ledger.MsgUpdateContainer)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped", "id": id})
}

func (s *Server) handleRemoveContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.rt.Remove(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.store.DeleteContainer(id)
	s.commitRemove(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "id": id})
}

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"images": s.store.ListImages()})
}

type imageRefRequest struct {
	Ref string `json:"ref"`
}

func (s *Server) handlePullImage(w http.ResponseWriter, r *http.Request) {
	var req imageRefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return
	}
	info, err := s.rt.Pull(r.Context(), req.Ref)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.store.UpsertImage(state.ImageRecord{
		Digest: info.Digest, Size: info.Size, Ref: info.Ref,
		Verified: true, VerifiedAt: time.Now().UTC(),
	})
	s.commitImage(r.Context(), info.Digest, info.Size)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleVerifyImage(w http.ResponseWriter, r *http.Request) {
	var req imageRefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return
	}
	info, err := s.rt.VerifyImage(r.Context(), req.Ref)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	_ = s.store.UpsertImage(state.ImageRecord{
		Digest: info.Digest, Size: info.Size, Ref: info.Ref,
		Verified: true, VerifiedAt: time.Now().UTC(),
	})
	s.commitImage(r.Context(), info.Digest, info.Size)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot())
}

func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request) {
	if s.engine != nil {
		s.engine.Refresh()
	}
	writeJSON(w, http.StatusOK, s.ledger.Snapshot())
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	if s.engine != nil {
		s.engine.Refresh()
	}
	nodes := s.ledger.ListNodes()
	online, offline := 0, 0
	for _, n := range nodes {
		switch n.Status {
		case ledger.StatusOffline:
			offline++
		default:
			online++
		}
	}
	timeout := ""
	if s.engine != nil && s.engine.HeartbeatTimeout > 0 {
		timeout = s.engine.HeartbeatTimeout.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":             nodes,
		"online":            online,
		"offline":           offline,
		"heartbeat_timeout": timeout,
	})
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"members": s.members.List(),
		"open":    s.members.IsEmpty(),
	})
}

func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	var m membership.Member
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil || m.NodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}
	if m.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "public_key is required")
		return
	}
	if s.submitMembership(w, r, ledger.MsgJoinMember, ledger.MemberPayload{
		NodeID: m.NodeID, PublicKey: m.PublicKey, Addresses: m.Addresses, Label: m.Label,
	}) {
		writeJSON(w, http.StatusCreated, m)
		return
	}
	if err := s.members.Upsert(m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Evict/leave via LeaveMember consensus tx (API token auth). Join-token is
	// for pair/join only — leave is signed by this node's member/validator key.
	if s.ledger != nil {
		if err := ledger.CanLeaveMember(s.ledger.Snapshot().Members, id); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	if s.submitMembership(w, r, ledger.MsgLeaveMember, ledger.MemberPayload{NodeID: id}) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "node_id": id})
		return
	}
	if err := s.members.Remove(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "node_id": id})
}

// handleClusterLeave submits LeaveMember for this node's identity (self-leave).
// Requires API token. Refuses if this node is the last usable validator.
func (s *Server) handleClusterLeave(w http.ResponseWriter, r *http.Request) {
	if s.kp == nil {
		writeError(w, http.StatusServiceUnavailable, "node identity not configured")
		return
	}
	id := s.kp.NodeID
	if s.ledger != nil {
		if err := ledger.CanLeaveMember(s.ledger.Snapshot().Members, id); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	if s.submitMembership(w, r, ledger.MsgLeaveMember, ledger.MemberPayload{NodeID: id}) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "left", "node_id": id})
		return
	}
	if s.members != nil {
		if err := s.members.Remove(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "left", "node_id": id})
}

// submitMembership tries TxSubmitter then hash-chain consensus. Returns true if
// handled (caller should not local-mutate). On submit error writes response.
func (s *Server) submitMembership(w http.ResponseWriter, r *http.Request, typ ledger.MessageType, payload ledger.MemberPayload) bool {
	if s.kp == nil {
		return false
	}
	tx, err := ledger.NewTx(s.kp, typ, payload, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return true
	}
	if s.txSubmitter != nil {
		if err := s.txSubmitter.Submit(r.Context(), tx); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return true
		}
		return true
	}
	if s.consensus != nil {
		if err := s.consensus.Submit(r.Context(), tx); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return true
		}
		return true
	}
	return false
}

func (s *Server) requireEngine(w http.ResponseWriter) bool {
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "p2p engine not configured")
		return false
	}
	return true
}

func (s *Server) handleListPeers(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": s.engine.Peers.List()})
}

type peerURLRequest struct {
	URL string `json:"url"`
}

func (s *Server) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	var req peerURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	p := p2p.Peer{URL: req.URL}
	if err := s.engine.Peers.Upsert(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleRemovePeer(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	var req peerURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if err := s.engine.Peers.Remove(req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "url": req.URL})
}

func (s *Server) handleClusterPair(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	var req peerURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if err := s.engine.PairWith(r.Context(), req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "paired",
		"url":    req.URL,
		"peers":  s.engine.Peers.List(),
	})
}

func (s *Server) handleClusterSync(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	if err := s.engine.SyncOnce(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "synced",
		"peers":  s.engine.Peers.List(),
		"ledger": s.ledger.Snapshot(),
	})
}

func (s *Server) handleNetHello(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	var req p2p.Hello
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	resp, err := s.engine.HandleHello(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNetPair(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	var req p2p.PairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	resp, err := s.engine.HandlePair(r.Context(), req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "invalid join token" {
			status = http.StatusUnauthorized
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNetSync(w http.ResponseWriter, r *http.Request) {
	if !s.requireEngine(w) {
		return
	}
	var req p2p.SyncMessage
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	resp, err := s.engine.HandleSync(req)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not a member") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) requireConsensus(w http.ResponseWriter) bool {
	if s.consensus == nil {
		writeError(w, http.StatusServiceUnavailable, "consensus engine not configured")
		return false
	}
	return true
}

func (s *Server) handleChain(w http.ResponseWriter, r *http.Request) {
	if !s.requireConsensus(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.consensus.Status())
}

func (s *Server) handleNetPropose(w http.ResponseWriter, r *http.Request) {
	if !s.requireConsensus(w) {
		return
	}
	var req consensus.Propose
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	vote, err := s.consensus.HandlePropose(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vote)
}

func (s *Server) handleNetCommit(w http.ResponseWriter, r *http.Request) {
	if !s.requireConsensus(w) {
		return
	}
	var req consensus.Commit
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.consensus.HandleCommit(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "committed",
		"height": req.Block.Height,
		"hash":   req.Block.Hash,
	})
}

func (s *Server) handleNetTx(w http.ResponseWriter, r *http.Request) {
	if !s.requireConsensus(w) {
		return
	}
	var tx ledger.Tx
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	commit, err := s.consensus.HandleTx(r.Context(), tx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, commit)
}

func (s *Server) tryConsensus(ctx context.Context, typ ledger.MessageType, payload any) bool {
	if s.consensus == nil {
		return false
	}
	tx, err := ledger.NewTx(s.kp, typ, payload, time.Now().UTC())
	if err != nil {
		log.Printf("consensus tx: %v", err)
		return false
	}
	if err := s.consensus.Submit(ctx, tx); err != nil {
		log.Printf("consensus submit: %v", err)
		return false
	}
	return true
}

// submitStrict sends a tx through TxSubmitter. Returns true if a submitter is
// configured (caller must not fall back to local upserts either way).
func (s *Server) submitStrict(ctx context.Context, typ ledger.MessageType, payload any) bool {
	if s.txSubmitter == nil {
		return false
	}
	tx, err := ledger.NewTx(s.kp, typ, payload, time.Now().UTC())
	if err != nil {
		log.Printf("consensus tx (strict): %v", err)
		return true
	}
	if err := s.txSubmitter.Submit(ctx, tx); err != nil {
		log.Printf("consensus submit (strict): %v", err)
	}
	return true
}

func (s *Server) commitImage(ctx context.Context, digest string, size int64) {
	payload := ledger.ImagePayload{Digest: digest, Size: size}
	if s.submitStrict(ctx, ledger.MsgRegisterImage, payload) {
		return
	}
	if s.tryConsensus(ctx, ledger.MsgRegisterImage, payload) {
		return
	}
	_ = s.ledger.UpsertImage(ledger.ImageRecord{
		Digest:     digest,
		Size:       size,
		VerifiedBy: []string{s.kp.NodeID},
		FirstSeen:  time.Now().UTC(),
	})
}

func (s *Server) commitContainer(ctx context.Context, payload ledger.ContainerPayload, typ ledger.MessageType) {
	if s.submitStrict(ctx, typ, payload) {
		return
	}
	if s.tryConsensus(ctx, typ, payload) {
		return
	}
	_ = s.ledger.UpsertContainer(ledger.ContainerRecord{
		ContainerID: payload.ContainerID,
		ImageDigest: payload.ImageDigest,
		Owner:       payload.Owner,
		Desired:     payload.Desired,
		CurrentNode: payload.CurrentNode,
		Labels:      payload.Labels,
	})
}

func (s *Server) commitRemove(ctx context.Context, id string) {
	if s.submitStrict(ctx, ledger.MsgRemoveContainer, ledger.RemovePayload{ContainerID: id}) {
		return
	}
	if s.tryConsensus(ctx, ledger.MsgRemoveContainer, ledger.RemovePayload{ContainerID: id}) {
		return
	}
	_ = s.ledger.RemoveContainer(id)
}
