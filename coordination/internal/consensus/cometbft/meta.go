package cometbft

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// abciMeta persists last committed height and app hash so Info matches
// CometBFT's store across restarts (avoids block replay onto an already-applied ledger).
type abciMeta struct {
	Height  int64  `json:"height"`
	AppHash string `json:"app_hash_hex"`
}

func (a *App) metaPath() string {
	if a.persistDir == "" {
		return ""
	}
	return filepath.Join(a.persistDir, "abci-meta.json")
}

// SetPersistDir enables height/app-hash persistence under dir (typically data-dir/cometbft).
func (a *App) SetPersistDir(dir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if dir == "" {
		a.persistDir = ""
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	a.persistDir = dir
	return a.loadMetaLocked()
}

func (a *App) loadMetaLocked() error {
	path := a.metaPath()
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var m abciMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("abci meta: %w", err)
	}
	a.height = m.Height
	if m.AppHash != "" {
		h, err := hex.DecodeString(m.AppHash)
		if err != nil {
			return fmt.Errorf("abci meta app hash: %w", err)
		}
		a.appHash = h
	}
	return nil
}

func (a *App) saveMetaLocked() error {
	path := a.metaPath()
	if path == "" {
		return nil
	}
	m := abciMeta{
		Height:  a.height,
		AppHash: hex.EncodeToString(a.appHash),
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
