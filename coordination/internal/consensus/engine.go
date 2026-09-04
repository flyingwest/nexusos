package consensus

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
	"github.com/nexusos/coordination/internal/p2p"
)

const maxTxsPerBlock = 32

// Engine runs permissioned, hash-chained consensus for ledger mutations.
// Heartbeats stay on HTTP snapshot sync; image integrity and placement go here.
type Engine struct {
	KP        *identity.KeyPair
	Advertise string
	Ledger    *ledger.Store
	Members   *membership.Store
	Peers     *p2p.Store
	Client    *p2p.Client
	Chain     *Chain
	Timeout   time.Duration
	Now       func() time.Time

	mu         sync.Mutex
	mempool    []ledger.Tx
	lastCommit Commit
	// proposeMu serializes leader proposals so heights do not race.
	proposeMu sync.Mutex
	commitMu  sync.Mutex
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e *Engine) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return 5 * time.Second
}

func (e *Engine) validators() []string {
	if e.Members == nil {
		return []string{e.KP.NodeID}
	}
	ids := e.Members.IDs()
	if len(ids) == 0 {
		return []string{e.KP.NodeID}
	}
	return ids
}

func (e *Engine) isLeader(height uint64) bool {
	return Leader(e.validators(), height) == e.KP.NodeID
}

func (e *Engine) leaderURL(id string) string {
	if id == e.KP.NodeID {
		return ""
	}
	if e.Members != nil {
		if m, ok := e.Members.Get(id); ok {
			for _, a := range m.Addresses {
				if a != "" {
					return p2p.NormalizeURL(a)
				}
			}
		}
	}
	if e.Peers != nil {
		for _, p := range e.Peers.List() {
			if p.NodeID == id && p.URL != "" {
				return p2p.NormalizeURL(p.URL)
			}
		}
	}
	return ""
}

func (e *Engine) peerURLs() []string {
	if e.Peers == nil {
		return nil
	}
	var out []string
	for _, p := range e.Peers.List() {
		u := p2p.NormalizeURL(p.URL)
		if u == "" || u == p2p.NormalizeURL(e.Advertise) {
			continue
		}
		out = append(out, u)
	}
	return out
}

func (e *Engine) addMempool(tx ledger.Tx) {
	h := tx.Hash()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, existing := range e.mempool {
		if existing.Hash() == h {
			return
		}
	}
	e.mempool = append(e.mempool, tx)
}

func (e *Engine) takeMempool() []ledger.Tx {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(e.mempool)
	if n > maxTxsPerBlock {
		n = maxTxsPerBlock
	}
	out := append([]ledger.Tx(nil), e.mempool[:n]...)
	return out
}

func (e *Engine) dropCommitted(txs []ledger.Tx) {
	seen := make(map[string]struct{}, len(txs))
	for _, tx := range txs {
		seen[tx.Hash()] = struct{}{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	kept := e.mempool[:0]
	for _, tx := range e.mempool {
		if _, ok := seen[tx.Hash()]; !ok {
			kept = append(kept, tx)
		}
	}
	e.mempool = kept
}

// Submit appends a signed tx and tries to commit it at the next height.
func (e *Engine) Submit(ctx context.Context, tx ledger.Tx) error {
	if err := ledger.VerifyTx(tx); err != nil {
		return err
	}
	if e.Members != nil && !e.Members.Contains(tx.NodeID) {
		return fmt.Errorf("tx from non-member %s", tx.NodeID)
	}
	if e.Chain != nil && e.Chain.ContainsTx(tx.Hash()) {
		return nil
	}
	e.addMempool(tx)

	ctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()

	e.proposeMu.Lock()
	defer e.proposeMu.Unlock()

	if e.Chain != nil && e.Chain.ContainsTx(tx.Hash()) {
		return nil
	}
	height := uint64(1)
	if e.Chain != nil {
		height = e.Chain.Height() + 1
	}
	if e.isLeader(height) {
		return e.proposeAndWait(ctx)
	}
	return e.forward(ctx, tx)
}

func (e *Engine) forward(ctx context.Context, tx ledger.Tx) error {
	height := uint64(1)
	if e.Chain != nil {
		height = e.Chain.Height() + 1
	}
	leader := Leader(e.validators(), height)
	url := e.leaderURL(leader)
	if url == "" {
		return fmt.Errorf("leader %s is unreachable", leader)
	}
	if e.Client == nil {
		return fmt.Errorf("p2p client not configured")
	}
	var commit Commit
	if err := e.Client.PostJSON(ctx, url, "/v1/net/tx", tx, &commit); err != nil {
		return err
	}
	return e.HandleCommit(commit)
}

func (e *Engine) proposeAndWait(ctx context.Context) error {
	txs := e.takeMempool()
	if len(txs) == 0 {
		return nil
	}
	height := e.Chain.Height() + 1
	prev := e.Chain.HeadHash()
	blk := Block{
		Height:    height,
		PrevHash:  prev,
		Timestamp: e.now().Truncate(time.Millisecond),
		Txs:       txs,
	}
	signed, err := SignBlock(e.KP, blk)
	if err != nil {
		return err
	}
	votes := map[string]Vote{
		e.KP.NodeID: SignVote(e.KP, signed.Height, signed.Hash, e.now()),
	}

	for _, url := range e.peerURLs() {
		var vote Vote
		if err := e.Client.PostJSON(ctx, url, "/v1/net/propose", Propose{
			Block:     signed,
			Advertise: e.Advertise,
		}, &vote); err != nil {
			log.Printf("consensus propose %s: %v", url, err)
			continue
		}
		if err := VerifyVote(vote); err != nil {
			log.Printf("consensus vote from %s: %v", url, err)
			continue
		}
		if vote.Height != signed.Height || vote.BlockHash != signed.Hash {
			continue
		}
		if e.Members != nil && !e.Members.Contains(vote.NodeID) {
			continue
		}
		votes[vote.NodeID] = vote
	}

	need := Quorum(len(e.validators()))
	if len(votes) < need {
		return fmt.Errorf("no quorum: got %d need %d", len(votes), need)
	}
	commit := Commit{Block: signed, Votes: votesSlice(votes)}
	if err := e.applyCommit(commit); err != nil {
		return err
	}
	for _, url := range e.peerURLs() {
		if err := e.Client.PostJSON(ctx, url, "/v1/net/commit", commit, nil); err != nil {
			log.Printf("consensus commit %s: %v", url, err)
		}
	}
	return nil
}

func votesSlice(m map[string]Vote) []Vote {
	out := make([]Vote, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func (e *Engine) applyCommit(c Commit) error {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	if err := e.verifyCommit(c); err != nil {
		return err
	}
	if e.Chain.Has(c.Block.Height, c.Block.Hash) {
		return nil
	}
	if err := e.Ledger.ApplyTxs(c.Block.Txs); err != nil {
		return err
	}
	if err := e.Chain.Append(c.Block); err != nil {
		return err
	}
	e.dropCommitted(c.Block.Txs)
	e.mu.Lock()
	e.lastCommit = c
	e.mu.Unlock()
	return nil
}

func (e *Engine) verifyCommit(c Commit) error {
	if err := VerifyBlock(c.Block); err != nil {
		return err
	}
	height := e.Chain.Height() + 1
	if e.Chain.Has(c.Block.Height, c.Block.Hash) {
		return nil
	}
	if c.Block.Height != height {
		return fmt.Errorf("commit height %d, expected %d", c.Block.Height, height)
	}
	if c.Block.PrevHash != e.Chain.HeadHash() {
		return fmt.Errorf("commit prev_hash mismatch")
	}
	leader := Leader(e.validators(), c.Block.Height)
	if c.Block.Proposer != leader {
		return fmt.Errorf("proposer %s is not leader %s", c.Block.Proposer, leader)
	}
	seen := map[string]struct{}{}
	okVotes := 0
	for _, v := range c.Votes {
		if err := VerifyVote(v); err != nil {
			continue
		}
		if v.Height != c.Block.Height || v.BlockHash != c.Block.Hash {
			continue
		}
		if e.Members != nil && !e.Members.Contains(v.NodeID) {
			continue
		}
		if _, dup := seen[v.NodeID]; dup {
			continue
		}
		seen[v.NodeID] = struct{}{}
		okVotes++
	}
	need := Quorum(len(e.validators()))
	if okVotes < need {
		return fmt.Errorf("commit lacks quorum: %d < %d", okVotes, need)
	}
	return nil
}

// HandlePropose verifies a leader block and returns a vote. It does not apply.
func (e *Engine) HandlePropose(p Propose) (Vote, error) {
	if err := VerifyBlock(p.Block); err != nil {
		return Vote{}, err
	}
	height := e.Chain.Height() + 1
	if p.Block.Height != height {
		return Vote{}, fmt.Errorf("propose height %d, expected %d", p.Block.Height, height)
	}
	if p.Block.PrevHash != e.Chain.HeadHash() {
		return Vote{}, fmt.Errorf("propose prev_hash mismatch")
	}
	if p.Block.Proposer != Leader(e.validators(), p.Block.Height) {
		return Vote{}, fmt.Errorf("not the leader for height %d", p.Block.Height)
	}
	if e.Members != nil && !e.Members.Contains(p.Block.Proposer) {
		return Vote{}, fmt.Errorf("proposer is not a member")
	}
	for _, tx := range p.Block.Txs {
		if e.Members != nil && !e.Members.Contains(tx.NodeID) {
			return Vote{}, fmt.Errorf("tx from non-member %s", tx.NodeID)
		}
	}
	return SignVote(e.KP, p.Block.Height, p.Block.Hash, e.now()), nil
}

// HandleCommit applies a quorum-certified block.
func (e *Engine) HandleCommit(c Commit) error {
	return e.applyCommit(c)
}

// HandleTx is the leader RPC: commit this tx (and any batched mempool) and
// return the resulting commit so the follower can apply it.
func (e *Engine) HandleTx(ctx context.Context, tx ledger.Tx) (Commit, error) {
	if err := e.Submit(ctx, tx); err != nil {
		return Commit{}, err
	}
	e.mu.Lock()
	c := e.lastCommit
	e.mu.Unlock()
	if c.Block.Height == 0 {
		last, ok := e.Chain.Last()
		if !ok {
			return Commit{}, fmt.Errorf("no committed block")
		}
		c = Commit{Block: last}
	}
	return c, nil
}

// Status is a snapshot for GET /v1/chain.
func (e *Engine) Status() map[string]any {
	vals := e.validators()
	var lastHash string
	height := uint64(0)
	if e.Chain != nil {
		height = e.Chain.Height()
		lastHash = e.Chain.HeadHash()
	}
	e.mu.Lock()
	pending := len(e.mempool)
	e.mu.Unlock()
	return map[string]any{
		"height":     height,
		"hash":       lastHash,
		"validators": vals,
		"leader":     Leader(vals, height+1),
		"quorum":     Quorum(len(vals)),
		"mempool":    pending,
		"self":       e.KP.NodeID,
	}
}
