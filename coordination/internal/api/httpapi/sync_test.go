package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nexusos/coordination/internal/consensus"
	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/p2p"
	"github.com/nexusos/coordination/internal/runtime"
	"github.com/nexusos/coordination/internal/state"
)

type testNode struct {
	kp     *identity.KeyPair
	ledger *ledger.Store
	engine *p2p.Engine
	ceng   *consensus.Engine
	srv    *httptest.Server
}

func newTestNode(t *testing.T, joinToken string) *testNode {
	t.Helper()
	dir := t.TempDir()
	kp, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.NewStore(dir, kp.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mem, err := membership.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := p2p.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := kp.PublicJSON()
	_ = led.UpsertNode(ledger.NodeRecord{NodeID: pub.NodeID, PublicKey: pub.PublicKey, Status: "Online", Mode: "permissioned"})
	_ = mem.Upsert(membership.Member{NodeID: pub.NodeID, PublicKey: pub.PublicKey, Label: "self"})

	eng := &p2p.Engine{
		KP:        kp,
		JoinToken: joinToken,
		Mode:      "permissioned",
		Ledger:    led,
		Members:   mem,
		Peers:     peers,
		Client:    p2p.NewClient(),
		Interval:  time.Hour,
	}
	chain, err := consensus.NewChain(dir)
	if err != nil {
		t.Fatal(err)
	}
	ceng := &consensus.Engine{
		KP:      kp,
		Ledger:  led,
		Members: mem,
		Peers:   peers,
		Client:  p2p.NewClient(),
		Chain:   chain,
		Timeout: 2 * time.Second,
	}
	api := New(Options{
		Addr:      "127.0.0.1:0",
		Mode:      "permissioned",
		KeyPair:   kp,
		Runtime:   runtime.NewMockRuntime(),
		Store:     st,
		Ledger:    led,
		Members:   mem,
		Engine:    eng,
		Consensus: ceng,
	})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	eng.Advertise = srv.URL
	ceng.Advertise = srv.URL
	return &testNode{kp: kp, ledger: led, engine: eng, ceng: ceng, srv: srv}
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode: %v (%s)", err, data)
		}
	}
}

func TestTwoNodesSyncPlacementAndImageIntegrity(t *testing.T) {
	a := newTestNode(t, "cluster")
	b := newTestNode(t, "cluster")

	if err := a.engine.PairWith(context.Background(), b.srv.URL); err != nil {
		t.Fatalf("pair: %v", err)
	}

	ref := "docker.io/library/nginx:alpine"
	resp := postJSON(t, a.srv.URL+"/v1/images/pull", map[string]string{"ref": ref})
	var img runtimeImage
	decodeBody(t, resp, &img)
	if img.Digest == "" {
		t.Fatal("missing digest")
	}

	resp = postJSON(t, b.srv.URL+"/v1/images/pull", map[string]string{"ref": ref})
	decodeBody(t, resp, &img)

	resp = postJSON(t, a.srv.URL+"/v1/containers", map[string]string{"image_ref": ref, "name": "web"})
	var ctr struct {
		ID string `json:"id"`
	}
	decodeBody(t, resp, &ctr)
	if ctr.ID == "" {
		t.Fatal("missing container id")
	}

	if err := a.engine.SyncOnce(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	snapB := b.ledger.Snapshot()
	got, ok := snapB.Containers[ctr.ID]
	if !ok {
		t.Fatalf("container not on B: %+v", snapB.Containers)
	}
	if got.CurrentNode != a.kp.NodeID {
		t.Fatalf("placement: got %s want %s", got.CurrentNode, a.kp.NodeID)
	}
	imgRec, ok := snapB.Images[img.Digest]
	if !ok {
		t.Fatal("image missing on B")
	}
	if len(imgRec.VerifiedBy) < 2 {
		t.Fatalf("expected both nodes in verified_by, got %v", imgRec.VerifiedBy)
	}

	req, _ := http.NewRequest(http.MethodDelete, a.srv.URL+"/v1/containers/"+ctr.ID, nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decodeBody(t, delResp, nil)

	if err := a.engine.SyncOnce(context.Background()); err != nil {
		t.Fatalf("sync after delete: %v", err)
	}
	if _, ok := b.ledger.Snapshot().Containers[ctr.ID]; ok {
		t.Fatal("tombstone should have removed container on B")
	}
}

type runtimeImage struct {
	Digest string `json:"digest"`
	Ref    string `json:"ref"`
}

func TestSyncRejectedWithoutMembership(t *testing.T) {
	a := newTestNode(t, "cluster")
	b := newTestNode(t, "cluster")
	_ = a.engine.Peers.Upsert(p2p.Peer{URL: b.srv.URL})
	err := a.engine.SyncPeer(context.Background(), b.srv.URL)
	if err == nil {
		t.Fatal("expected membership rejection")
	}
}

func TestPairRejectedWithWrongToken(t *testing.T) {
	a := newTestNode(t, "alpha")
	b := newTestNode(t, "beta")
	if err := a.engine.PairWith(context.Background(), b.srv.URL); err == nil {
		t.Fatal("expected join token rejection")
	}
}

func TestStaleNodeMarkedOfflineAndSynced(t *testing.T) {
	a := newTestNode(t, "cluster")
	b := newTestNode(t, "cluster")
	a.engine.HeartbeatTimeout = 50 * time.Millisecond
	b.engine.HeartbeatTimeout = 50 * time.Millisecond

	if err := a.engine.PairWith(context.Background(), b.srv.URL); err != nil {
		t.Fatalf("pair: %v", err)
	}

	stale := time.Now().UTC().Add(-time.Second)
	st := a.ledger.Snapshot()
	st.Nodes["deadpeer"] = ledger.NodeRecord{
		NodeID:        "deadpeer",
		Status:        ledger.StatusOnline,
		LastHeartbeat: stale,
		Mode:          "permissioned",
	}
	if err := a.ledger.Merge(st); err != nil {
		t.Fatal(err)
	}

	if marked := a.engine.Refresh(); len(marked) != 1 || marked[0] != "deadpeer" {
		t.Fatalf("expected deadpeer marked, got %v", marked)
	}
	if n, _ := a.ledger.GetNode("deadpeer"); n.Status != ledger.StatusOffline {
		t.Fatalf("A status: %s", n.Status)
	}

	if err := a.engine.SyncOnce(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n, ok := b.ledger.GetNode("deadpeer"); !ok || n.Status != ledger.StatusOffline {
		t.Fatalf("B should have deadpeer Offline, got ok=%v %+v", ok, n)
	}

	resp, err := http.Get(a.srv.URL + "/v1/nodes")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Offline int `json:"offline"`
		Online  int `json:"online"`
	}
	decodeBody(t, resp, &body)
	if body.Offline < 1 {
		t.Fatalf("expected at least one Offline node, got %+v", body)
	}
}

func TestTwoNodeConsensusCommitsImageWithoutSnapshotSync(t *testing.T) {
	a := newTestNode(t, "cluster")
	b := newTestNode(t, "cluster")
	if err := a.engine.PairWith(context.Background(), b.srv.URL); err != nil {
		t.Fatalf("pair: %v", err)
	}

	resp := postJSON(t, a.srv.URL+"/v1/images/pull", map[string]string{"ref": "docker.io/library/nginx:alpine"})
	var img runtimeImage
	decodeBody(t, resp, &img)
	if img.Digest == "" {
		t.Fatal("missing digest")
	}

	got, ok := b.ledger.Snapshot().Images[img.Digest]
	if !ok {
		t.Fatalf("B should already have the image via consensus, chain A=%d B=%d", a.ceng.Chain.Height(), b.ceng.Chain.Height())
	}
	if len(got.VerifiedBy) < 1 {
		t.Fatalf("verified_by: %v", got.VerifiedBy)
	}
	if a.ceng.Chain.Height() < 1 || b.ceng.Chain.Height() < 1 {
		t.Fatalf("expected committed height >= 1, A=%d B=%d", a.ceng.Chain.Height(), b.ceng.Chain.Height())
	}

	chainResp, err := http.Get(a.srv.URL + "/v1/chain")
	if err != nil {
		t.Fatal(err)
	}
	var chain struct {
		Height uint64 `json:"height"`
		Quorum int    `json:"quorum"`
	}
	decodeBody(t, chainResp, &chain)
	if chain.Height < 1 || chain.Quorum != 2 {
		t.Fatalf("chain status: %+v", chain)
	}
}
