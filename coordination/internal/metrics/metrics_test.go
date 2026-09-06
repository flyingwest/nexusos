package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerExposesCountersAndGauges(t *testing.T) {
	APIRequests.WithLabelValues("GET", "/health", "2xx").Inc()
	SetSnapshot(Snapshot{
		Containers: 2, Workloads: 1, Migrations: 0,
		NodesOnline: 1, NodesDraining: 1, Peers: 3, ConsensusHeight: 42,
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"nexusos_api_requests_total",
		"nexusos_containers 2",
		"nexusos_workloads 1",
		"nexusos_nodes_draining 1",
		"nexusos_peers 3",
		"nexusos_consensus_height 42",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}
