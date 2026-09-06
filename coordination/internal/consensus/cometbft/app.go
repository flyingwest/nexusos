// Package cometbft implements an ABCI application and in-process CometBFT node
// that map NexusOS ledger txs (image integrity + placement) onto consensus.
//
// CometBFT is the sole coordinator consensus engine. See docs/cometbft-spike.md.
package cometbft

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	abcitypes "github.com/cometbft/cometbft/abci/types"

	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

// App is an ABCI 2.0 application that verifies and applies existing ledger.Tx
// values via ledger.ApplyTx / Store.ApplyTxs.
//
// Validator set: ABCI 2.0 has no separate EndBlock; ValidatorUpdates are
// returned from FinalizeBlock (take effect at height+2). The app diffs
// ledger Members (JoinMember/LeaveMember txs) against the tracked set each block.
type App struct {
	abcitypes.BaseApplication

	Ledger  *ledger.Store
	Members *membership.Store // optional; when set, non-members are rejected

	mu         sync.Mutex
	height     int64
	appHash    []byte
	persistDir string
	// pending holds txs accepted in the last FinalizeBlock until Commit.
	pending []ledger.Tx

	// valSet tracks pubkey-hex → power after InitChain and after each proposed update.
	valSet map[string]int64
	// localPubKeyHex is the in-process FilePV pubkey (hex). Used to refuse
	// membership sync that would remove the only key this node can sign with.
	localPubKeyHex string
}

// NewApp constructs an ABCI app bound to the coordinator ledger.
func NewApp(led *ledger.Store, members *membership.Store) *App {
	return &App{
		Ledger:  led,
		Members: members,
		appHash: []byte{},
		valSet:  make(map[string]int64),
	}
}

// SetLocalConsensusPubKey records the FilePV ed25519 pubkey (32 raw bytes or hex).
func (a *App) SetLocalConsensusPubKey(pub []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(pub) == 0 {
		a.localPubKeyHex = ""
		return
	}
	// Accept raw 32-byte or hex-encoded.
	if len(pub) == 32 {
		a.localPubKeyHex = hex.EncodeToString(pub)
		return
	}
	a.localPubKeyHex = string(pub)
}

func (a *App) Info(context.Context, *abcitypes.RequestInfo) (*abcitypes.ResponseInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &abcitypes.ResponseInfo{
		Data:             "nexusos-ledger",
		Version:          "0.3",
		AppVersion:       1,
		LastBlockHeight:  a.height,
		LastBlockAppHash: append([]byte(nil), a.appHash...),
	}, nil
}

// InitChain records genesis validators so FinalizeBlock can diff against membership.
func (a *App) InitChain(_ context.Context, req *abcitypes.RequestInitChain) (*abcitypes.ResponseInitChain, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.valSet = make(map[string]int64, len(req.Validators))
	for _, v := range req.Validators {
		hexKey, ok := validatorUpdatePubKeyHex(v)
		if !ok {
			continue
		}
		if v.Power > 0 {
			a.valSet[hexKey] = v.Power
		}
	}
	return &abcitypes.ResponseInitChain{}, nil
}

// CheckTx validates signature, membership, and that ApplyTx would succeed on a
// snapshot of current state (deterministic dry-run).
func (a *App) CheckTx(_ context.Context, req *abcitypes.RequestCheckTx) (*abcitypes.ResponseCheckTx, error) {
	tx, code, logMsg := a.validateBytes(req.Tx)
	if code != CodeOK {
		return &abcitypes.ResponseCheckTx{Code: code, Log: logMsg}, nil
	}
	// Dry-run apply against a snapshot so invalid payloads are rejected early.
	st := a.Ledger.Snapshot()
	if err := ledger.ApplyTx(&st, tx); err != nil {
		return &abcitypes.ResponseCheckTx{Code: CodeApply, Log: err.Error()}, nil
	}
	return &abcitypes.ResponseCheckTx{Code: CodeOK, GasWanted: 1}, nil
}

// FinalizeBlock verifies each tx, stages them for Commit, and proposes
// ValidatorUpdates so CometBFT tracks permissioned membership (join/pair/leave).
func (a *App) FinalizeBlock(_ context.Context, req *abcitypes.RequestFinalizeBlock) (*abcitypes.ResponseFinalizeBlock, error) {
	results := make([]*abcitypes.ExecTxResult, len(req.Txs))
	accepted := make([]ledger.Tx, 0, len(req.Txs))

	// Apply sequentially on a working copy so later txs see earlier ones in-block.
	st := a.Ledger.Snapshot()
	for i, raw := range req.Txs {
		tx, code, logMsg := a.validateBytes(raw)
		if code != CodeOK {
			results[i] = &abcitypes.ExecTxResult{Code: code, Log: logMsg}
			continue
		}
		if err := ledger.ApplyTx(&st, tx); err != nil {
			results[i] = &abcitypes.ExecTxResult{Code: CodeApply, Log: err.Error()}
			continue
		}
		results[i] = &abcitypes.ExecTxResult{Code: CodeOK}
		accepted = append(accepted, tx)
	}

	appHash := hashState(&st)

	a.mu.Lock()
	valUpdates := a.validatorUpdatesFromLedgerLocked(&st)
	a.pending = accepted
	a.height = req.Height
	a.appHash = appHash
	a.mu.Unlock()

	return &abcitypes.ResponseFinalizeBlock{
		TxResults:        results,
		AppHash:          appHash,
		ValidatorUpdates: valUpdates,
	}, nil
}

// validatorUpdatesFromLedgerLocked diffs consensus Members → tracked set.
// Caller holds a.mu. Empty ledger Members → no updates (preserve genesis).
func (a *App) validatorUpdatesFromLedgerLocked(st *ledger.State) []abcitypes.ValidatorUpdate {
	var desired map[string]int64
	if st != nil {
		desired = ledger.MembershipPower(st.Members)
	}
	updates := DiffValidatorUpdates(a.valSet, desired, a.localPubKeyHex)
	if len(updates) == 0 {
		return nil
	}
	ApplyValidatorUpdatesMut(a.valSet, updates)
	_ = WriteMembershipValidatorSnapshot(a.persistDir, a.Members)
	return updates
}

// Commit persists txs staged by FinalizeBlock.
func (a *App) Commit(context.Context, *abcitypes.RequestCommit) (*abcitypes.ResponseCommit, error) {
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	a.mu.Unlock()

	if len(pending) > 0 {
		if err := a.Ledger.ApplyTxs(pending); err != nil {
			return nil, fmt.Errorf("commit apply: %w", err)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.saveMetaLocked(); err != nil {
		return nil, fmt.Errorf("commit meta: %w", err)
	}
	return &abcitypes.ResponseCommit{RetainHeight: 0}, nil
}

func (a *App) validateBytes(raw []byte) (ledger.Tx, uint32, string) {
	tx, err := DecodeTx(raw)
	if err != nil {
		return ledger.Tx{}, CodeDecode, err.Error()
	}
	if err := ledger.VerifyTx(tx); err != nil {
		return ledger.Tx{}, CodeBadSig, err.Error()
	}
	switch tx.Type {
	case ledger.MsgRegisterImage, ledger.MsgVerifyImage,
		ledger.MsgCreateContainer, ledger.MsgUpdateContainer,
		ledger.MsgRemoveContainer,
		ledger.MsgCreateWorkload, ledger.MsgUpdateWorkload,
		ledger.MsgScaleWorkload, ledger.MsgDeleteWorkload,
		ledger.MsgProposeMigration, ledger.MsgCompleteMigration, ledger.MsgFailMigration,
		ledger.MsgJoinMember, ledger.MsgLeaveMember:
		// ok
	default:
		return ledger.Tx{}, CodeUnsupported, fmt.Sprintf("unsupported tx type %s", tx.Type)
	}
	if code, logMsg := a.authorizeSigner(tx); code != CodeOK {
		return ledger.Tx{}, code, logMsg
	}
	return tx, CodeOK, ""
}

// authorizeSigner enforces permissioned admission.
// JoinMember may be signed by an existing member, an active validator (genesis
// FilePV / prior set), or when membership is still open/empty — so a genesis
// validator can admit a peer before local members.json has been synced.
func (a *App) authorizeSigner(tx ledger.Tx) (uint32, string) {
	if tx.Type == ledger.MsgJoinMember {
		if a.Members == nil || a.Members.IsEmpty() || a.Members.Contains(tx.NodeID) {
			return CodeOK, ""
		}
		a.mu.Lock()
		_, inVal := a.valSet[tx.PublicKey]
		a.mu.Unlock()
		if inVal {
			return CodeOK, ""
		}
		st := a.Ledger.Snapshot()
		if _, ok := st.Members[tx.NodeID]; ok {
			return CodeOK, ""
		}
		return CodeNotMember, fmt.Sprintf("join from non-member/non-validator %s", tx.NodeID)
	}
	if a.Members != nil && !a.Members.Contains(tx.NodeID) {
		return CodeNotMember, fmt.Sprintf("tx from non-member %s", tx.NodeID)
	}
	return CodeOK, ""
}

// consensusDigest is the AppHash input: only fields driven by ABCI txs.
// Nodes/heartbeats are updated via HTTP sync and must not affect AppHash, or
// peers diverge (CONSENSUS FAILURE on Block.Header.AppHash).
type consensusDigest struct {
	Images     map[string]ledger.ImageRecord     `json:"images"`
	Containers map[string]ledger.ContainerRecord `json:"containers"`
	Migrations map[string]ledger.MigrationRecord `json:"migrations"`
	Workloads  map[string]ledger.WorkloadRecord  `json:"workloads,omitempty"`
	Members    map[string]ledger.MemberRecord    `json:"members,omitempty"`
	Tombstones map[string]time.Time              `json:"tombstones,omitempty"`
}

func hashState(st *ledger.State) []byte {
	d := consensusDigest{
		Images:     st.Images,
		Containers: st.Containers,
		Migrations: st.Migrations,
		Workloads:  st.Workloads,
		Members:    st.Members,
		Tombstones: st.Tombstones,
	}
	b, err := json.Marshal(d)
	if err != nil {
		sum := sha256.Sum256(nil)
		return sum[:]
	}
	sum := sha256.Sum256(b)
	return sum[:]
}

// Height returns the last finalized height (for tests / status).
func (a *App) Height() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.height
}

// AppHashHex returns the last app hash as hex.
func (a *App) AppHashHex() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return hex.EncodeToString(a.appHash)
}

// ValidatorSetSnapshot returns a copy of the tracked pubkey→power map.
func (a *App) ValidatorSetSnapshot() map[string]int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int64, len(a.valSet))
	for k, v := range a.valSet {
		out[k] = v
	}
	return out
}

// Ensure App implements abcitypes.Application.
var _ abcitypes.Application = (*App)(nil)
