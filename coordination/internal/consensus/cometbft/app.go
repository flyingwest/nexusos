// Package cometbft implements a minimal ABCI application that maps NexusOS
// ledger txs (image integrity + placement) onto CometBFT consensus.
//
// This spike embeds the Application in-process. Starting a full CometBFT
// consensus node (sidecar or node.New) is deferred; see docs/cometbft-spike.md
// and cmd/cometbft-abci-harness.
package cometbft

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	abcitypes "github.com/cometbft/cometbft/abci/types"

	"github.com/nexusos/coordination/internal/ledger"
	"github.com/nexusos/coordination/internal/membership"
)

// App is an ABCI 2.0 application that verifies and applies existing ledger.Tx
// values via ledger.ApplyTx / Store.ApplyTxs.
type App struct {
	abcitypes.BaseApplication

	Ledger  *ledger.Store
	Members *membership.Store // optional; when set, non-members are rejected

	mu      sync.Mutex
	height  int64
	appHash []byte
	// pending holds txs accepted in the last FinalizeBlock until Commit.
	pending []ledger.Tx
}

// NewApp constructs an ABCI app bound to the coordinator ledger.
func NewApp(led *ledger.Store, members *membership.Store) *App {
	return &App{
		Ledger:  led,
		Members: members,
		appHash: []byte{},
	}
}

func (a *App) Info(context.Context, *abcitypes.RequestInfo) (*abcitypes.ResponseInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &abcitypes.ResponseInfo{
		Data:             "nexusos-ledger",
		Version:          "spike",
		AppVersion:       1,
		LastBlockHeight:  a.height,
		LastBlockAppHash: append([]byte(nil), a.appHash...),
	}, nil
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

// FinalizeBlock verifies each tx and stages them for Commit. It does not
// persist; Commit calls Store.ApplyTxs so crash-before-commit does not mutate.
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
	a.pending = accepted
	a.height = req.Height
	a.appHash = appHash
	a.mu.Unlock()

	return &abcitypes.ResponseFinalizeBlock{
		TxResults: results,
		AppHash:   appHash,
	}, nil
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
	if a.Members != nil && !a.Members.Contains(tx.NodeID) {
		return ledger.Tx{}, CodeNotMember, fmt.Sprintf("tx from non-member %s", tx.NodeID)
	}
	switch tx.Type {
	case ledger.MsgRegisterImage, ledger.MsgVerifyImage,
		ledger.MsgCreateContainer, ledger.MsgUpdateContainer,
		ledger.MsgRemoveContainer:
		// ok
	default:
		return ledger.Tx{}, CodeUnsupported, fmt.Sprintf("unsupported tx type %s", tx.Type)
	}
	return tx, CodeOK, ""
}

func hashState(st *ledger.State) []byte {
	b, err := json.Marshal(st)
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

// Ensure App implements abcitypes.Application.
var _ abcitypes.Application = (*App)(nil)
