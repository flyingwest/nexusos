package p2p

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

// MembershipTxSubmitter commits JoinMember/LeaveMember through consensus.
type MembershipTxSubmitter interface {
	Submit(ctx context.Context, tx ledger.Tx) error
}

// Engine runs heartbeats and permissioned ledger sync with peers.
type Engine struct {
	KP               *identity.KeyPair
	Advertise        string
	JoinToken        string
	Mode             string
	Ledger           *ledger.Store
	Members          *membership.Store
	Peers            *Store
	Client           *Client
	Interval         time.Duration
	HeartbeatTimeout time.Duration
	Now              func() time.Time
	// MembershipTx, when set, admits peers via JoinMember consensus txs instead
	// of local members.json Upsert (required for CometBFT validator determinism).
	MembershipTx MembershipTxSubmitter
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e *Engine) signedHello() Hello {
	return SignHello(e.KP, e.Advertise, e.now())
}

func (e *Engine) heartbeat() {
	pub := e.KP.PublicJSON()
	rec := ledger.NodeRecord{
		NodeID:    pub.NodeID,
		PublicKey: pub.PublicKey,
		Status:    ledger.StatusOnline,
		Mode:      e.Mode,
	}
	if existing, ok := e.Ledger.GetNode(pub.NodeID); ok {
		rec = existing
		// Preserve Draining (scheduling disabled / cordon); do not auto-uncordon on heartbeat.
		if existing.Status != ledger.StatusDraining {
			rec.Status = ledger.StatusOnline
		}
		rec.Mode = e.Mode
	}
	if e.Advertise != "" {
		rec.Addresses = unionOne(rec.Addresses, e.Advertise)
	}
	_ = e.Ledger.UpsertNode(rec)
}

func (e *Engine) sweep() []string {
	marked, err := e.Ledger.SweepExpired(e.now(), e.HeartbeatTimeout, e.KP.NodeID)
	if err != nil {
		log.Printf("p2p sweep: %v", err)
		return nil
	}
	for _, id := range marked {
		log.Printf("p2p node %s marked Offline (heartbeat timeout %s)", id, e.HeartbeatTimeout)
	}
	return marked
}

// Refresh writes a self-heartbeat and marks stale peers Offline.
func (e *Engine) Refresh() []string {
	e.heartbeat()
	return e.sweep()
}

// heardFrom records that we just received a signed message from nodeID,
// using local time so failure detection does not depend on the sender's clock.
func (e *Engine) heardFrom(nodeID, publicKey string) {
	if nodeID == "" || nodeID == e.KP.NodeID {
		return
	}
	rec := ledger.NodeRecord{
		NodeID:    nodeID,
		PublicKey: publicKey,
		Status:    ledger.StatusOnline,
		Mode:      e.Mode,
	}
	if existing, ok := e.Ledger.GetNode(nodeID); ok {
		rec = existing
		if existing.Status != ledger.StatusDraining {
			rec.Status = ledger.StatusOnline
		}
		if publicKey != "" {
			rec.PublicKey = publicKey
		}
	}
	_ = e.Ledger.UpsertNode(rec)
}

func unionOne(in []string, extra string) []string {
	for _, s := range in {
		if s == extra {
			return in
		}
	}
	return append(append([]string{}, in...), extra)
}

func tokenOK(expected, got string) bool {
	if expected == "" {
		return true
	}
	if len(expected) != len(got) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(got)) == 1
}

// HandleHello verifies a remote hello and returns ours.
func (e *Engine) HandleHello(h Hello) (Hello, error) {
	if err := VerifyHello(h, e.now()); err != nil {
		return Hello{}, err
	}
	return e.signedHello(), nil
}

// HandlePair admits a signed remote node into membership + peer list.
// When MembershipTx is set, membership is ordered via JoinMember consensus;
// local Upsert is skipped (cache syncs on commit). Peer URL is always local.
func (e *Engine) HandlePair(ctx context.Context, req PairRequest) (Hello, error) {
	if err := VerifyHello(req.Hello, e.now()); err != nil {
		return Hello{}, err
	}
	if !tokenOK(e.JoinToken, req.JoinToken) {
		return Hello{}, fmt.Errorf("invalid join token")
	}
	callback := NormalizeURL(req.CallbackURL)
	if callback == "" {
		callback = NormalizeURL(req.Advertise)
	}
	if callback == "" {
		return Hello{}, fmt.Errorf("callback url required")
	}
	if callback == NormalizeURL(e.Advertise) || req.NodeID == e.KP.NodeID {
		return Hello{}, fmt.Errorf("cannot pair with self")
	}
	m := membership.Member{
		NodeID:    req.NodeID,
		PublicKey: req.PublicKey,
		Addresses: []string{callback},
		Label:     "peer",
	}
	if err := e.admitMember(ctx, m); err != nil {
		return Hello{}, err
	}
	if err := e.Peers.Upsert(Peer{
		URL:       callback,
		NodeID:    req.NodeID,
		PublicKey: req.PublicKey,
	}); err != nil {
		return Hello{}, err
	}
	return e.signedHello(), nil
}

func (e *Engine) admitMember(ctx context.Context, m membership.Member) error {
	if e.MembershipTx == nil {
		return e.Members.Upsert(m)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := ledger.NewTx(e.KP, ledger.MsgJoinMember, ledger.MemberPayload{
		NodeID:    m.NodeID,
		PublicKey: m.PublicKey,
		Addresses: m.Addresses,
		Label:     m.Label,
	}, e.now())
	if err != nil {
		return err
	}
	if err := e.MembershipTx.Submit(ctx, tx); err != nil {
		// Non-validator side may fail CheckTx; the genesis/active validator's
		// admit is enough. Keep pairing usable and log.
		log.Printf("membership JoinMember tx: %v (peer URL still recorded; rely on validator-side admit if needed)", err)
		return nil
	}
	return nil
}

func (e *Engine) acceptMember(nodeID string) error {
	if e.Members == nil || e.Members.IsEmpty() {
		return nil
	}
	if nodeID == e.KP.NodeID {
		return nil
	}
	if !e.Members.Contains(nodeID) {
		return fmt.Errorf("node %s is not a member", nodeID)
	}
	return nil
}

// HandleSync verifies, membership-checks, merges the remote snapshot, and
// returns our post-merge snapshot so one round-trip is bidirectional.
func (e *Engine) HandleSync(msg SyncMessage) (SyncMessage, error) {
	if err := VerifySync(msg, e.now()); err != nil {
		return SyncMessage{}, err
	}
	if err := e.acceptMember(msg.NodeID); err != nil {
		return SyncMessage{}, err
	}
	e.heartbeat()
	if err := e.Ledger.Merge(msg.State); err != nil {
		return SyncMessage{}, err
	}
	e.heardFrom(msg.NodeID, msg.PublicKey)
	e.sweep()
	if msg.Advertise != "" {
		_ = e.Peers.Upsert(Peer{
			URL:       msg.Advertise,
			NodeID:    msg.NodeID,
			PublicKey: msg.PublicKey,
		})
	}
	return SignSync(e.KP, e.Advertise, e.Ledger.Snapshot(), e.now())
}

// PairWith exchanges membership with a remote coordinator.
func (e *Engine) PairWith(ctx context.Context, url string) error {
	url = NormalizeURL(url)
	if url == "" {
		return fmt.Errorf("url is required")
	}
	if url == NormalizeURL(e.Advertise) {
		return fmt.Errorf("cannot pair with self")
	}
	req := PairRequest{
		Hello:       e.signedHello(),
		CallbackURL: e.Advertise,
		JoinToken:   e.JoinToken,
	}
	resp, err := e.Client.Pair(ctx, url, req)
	if err != nil {
		return err
	}
	if err := VerifyHello(resp, e.now()); err != nil {
		return err
	}
	if err := e.admitMember(ctx, membership.Member{
		NodeID:    resp.NodeID,
		PublicKey: resp.PublicKey,
		Addresses: []string{url},
		Label:     "peer",
	}); err != nil {
		return err
	}
	return e.Peers.Upsert(Peer{
		URL:       url,
		NodeID:    resp.NodeID,
		PublicKey: resp.PublicKey,
	})
}

// SyncPeer pushes our snapshot to one peer and merges the reply.
func (e *Engine) SyncPeer(ctx context.Context, url string) error {
	url = NormalizeURL(url)
	e.heartbeat()
	msg, err := SignSync(e.KP, e.Advertise, e.Ledger.Snapshot(), e.now())
	if err != nil {
		return err
	}
	resp, err := e.Client.Sync(ctx, url, msg)
	if err != nil {
		_ = e.Peers.RecordSync(url, "", "", err)
		return err
	}
	if err := VerifySync(resp, e.now()); err != nil {
		_ = e.Peers.RecordSync(url, resp.NodeID, resp.PublicKey, err)
		return err
	}
	if err := e.acceptMember(resp.NodeID); err != nil {
		_ = e.Peers.RecordSync(url, resp.NodeID, resp.PublicKey, err)
		return err
	}
	if err := e.Ledger.Merge(resp.State); err != nil {
		_ = e.Peers.RecordSync(url, resp.NodeID, resp.PublicKey, err)
		return err
	}
	e.heardFrom(resp.NodeID, resp.PublicKey)
	return e.Peers.RecordSync(url, resp.NodeID, resp.PublicKey, nil)
}

func (e *Engine) syncAll(ctx context.Context) error {
	var first error
	for _, p := range e.Peers.List() {
		if p.NodeID == "" {
			if err := e.PairWith(ctx, p.URL); err != nil {
				log.Printf("p2p pair %s: %v", p.URL, err)
				_ = e.Peers.RecordSync(p.URL, "", "", err)
				if first == nil {
					first = err
				}
				continue
			}
		}
		if err := e.SyncPeer(ctx, p.URL); err != nil {
			log.Printf("p2p sync %s: %v", p.URL, err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// SyncOnce pairs unknown bootstrap peers if needed, then syncs every peer.
func (e *Engine) SyncOnce(ctx context.Context) error {
	e.heartbeat()
	err := e.syncAll(ctx)
	if marked := e.sweep(); len(marked) > 0 {
		// Push Offline status while live peers are still reachable.
		if pushErr := e.syncAll(ctx); pushErr != nil && err == nil {
			err = pushErr
		}
	}
	return err
}

// Run heartbeats and syncs until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	if e.Interval <= 0 {
		e.Interval = 10 * time.Second
	}
	if e.Client == nil {
		e.Client = NewClient()
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(500 * time.Millisecond):
	}

	e.heartbeat()
	if err := e.SyncOnce(ctx); err != nil {
		log.Printf("p2p initial sync: %v", err)
	}

	t := time.NewTicker(e.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.SyncOnce(ctx); err != nil {
				log.Printf("p2p sync: %v", err)
			}
		}
	}
}
