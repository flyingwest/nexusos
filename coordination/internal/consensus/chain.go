package consensus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Chain is a persisted hash-chained log of committed blocks.
type Chain struct {
	mu     sync.RWMutex
	path   string
	blocks []Block
}

func NewChain(dataDir string) (*Chain, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	c := &Chain{path: filepath.Join(dataDir, "chain.json")}
	if err := c.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return c, nil
}

func (c *Chain) load() error {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var blocks []Block
	if err := json.Unmarshal(b, &blocks); err != nil {
		return fmt.Errorf("parse chain: %w", err)
	}
	c.blocks = blocks
	return nil
}

func (c *Chain) save() error {
	b, err := json.MarshalIndent(c.blocks, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (c *Chain) Height() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.blocks) == 0 {
		return 0
	}
	return c.blocks[len(c.blocks)-1].Height
}

func (c *Chain) HeadHash() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.blocks) == 0 {
		return ""
	}
	return c.blocks[len(c.blocks)-1].Hash
}

func (c *Chain) Last() (Block, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.blocks) == 0 {
		return Block{}, false
	}
	return c.blocks[len(c.blocks)-1], true
}

func (c *Chain) ContainsTx(hash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, b := range c.blocks {
		for _, tx := range b.Txs {
			if tx.Hash() == hash {
				return true
			}
		}
	}
	return false
}

func (c *Chain) Append(b Block) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.blocks) > 0 {
		head := c.blocks[len(c.blocks)-1]
		if b.Height != head.Height+1 {
			return fmt.Errorf("height %d does not follow %d", b.Height, head.Height)
		}
		if b.PrevHash != head.Hash {
			return fmt.Errorf("prev_hash mismatch")
		}
	} else if b.Height != 1 || b.PrevHash != "" {
		return fmt.Errorf("genesis block must be height 1 with empty prev_hash")
	}
	c.blocks = append(c.blocks, b)
	if err := c.save(); err != nil {
		c.blocks = c.blocks[:len(c.blocks)-1]
		return err
	}
	return nil
}

func (c *Chain) Has(height uint64, hash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, b := range c.blocks {
		if b.Height == height && b.Hash == hash {
			return true
		}
	}
	return false
}

func (c *Chain) Blocks() []Block {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Block, len(c.blocks))
	copy(out, c.blocks)
	return out
}
