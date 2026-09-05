package httpapi

import (
	"net/http"
	"strings"

	"github.com/nexusos/coordination/internal/consensus/cometbft"
)

// handleCometBFTBootstrap returns shared genesis + peer info for joining nodes.
// Auth: Bearer API token OR X-Nexus-Join-Token matching the cluster join-token.
func (s *Server) handleCometBFTBootstrap(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeBootstrap(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.cometbftHome == "" {
		writeError(w, http.StatusServiceUnavailable, "cometbft not enabled on this node")
		return
	}
	info, err := cometbft.LoadBootstrapInfo(s.cometbftHome, s.cometbftP2P)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cometbft bootstrap unavailable: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) authorizeBootstrap(r *http.Request) bool {
	// Dev / empty tokens: allow (process Validate already gated production).
	if s.apiToken == "" && s.joinToken == "" {
		return true
	}
	if s.apiToken != "" {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") && strings.TrimPrefix(h, "Bearer ") == s.apiToken {
			return true
		}
	}
	if s.joinToken != "" && r.Header.Get(cometbft.JoinTokenHeader) == s.joinToken {
		return true
	}
	return false
}
