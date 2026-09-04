package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nexusos/coordination/internal/identity"
	"github.com/nexusos/coordination/internal/ledger"
)

const (
	domainBlock = "nexusos-block-v1"
	domainVote  = "nexusos-vote-v1"
)

// Block is a hash-chained batch of ledger transactions.
type Block struct {
	Height    uint64      `json:"height"`
	PrevHash  string      `json:"prev_hash"`
	Timestamp time.Time   `json:"timestamp"`
	Proposer  string      `json:"proposer"`
	PublicKey string      `json:"public_key"`
	Txs       []ledger.Tx `json:"txs"`
	Hash      string      `json:"hash"`
	Signature string      `json:"signature"`
}

// Vote is a validator's signed acceptance of a block hash at a height.
type Vote struct {
	Height    uint64    `json:"height"`
	BlockHash string    `json:"block_hash"`
	NodeID    string    `json:"node_id"`
	PublicKey string    `json:"public_key"`
	Timestamp time.Time `json:"timestamp"`
	Signature string    `json:"signature"`
}

// Commit is a block plus a quorum of votes.
type Commit struct {
	Block Block  `json:"block"`
	Votes []Vote `json:"votes"`
}

// Propose is sent by the leader; the HTTP response is a Vote.
type Propose struct {
	Block     Block  `json:"block"`
	Advertise string `json:"advertise,omitempty"`
}

type canonBlock struct {
	Height    uint64   `json:"height"`
	PrevHash  string   `json:"prev_hash"`
	Timestamp string   `json:"timestamp"`
	Proposer  string   `json:"proposer"`
	TxHashes  []string `json:"tx_hashes"`
}

func blockBody(b Block) ([]byte, error) {
	hashes := make([]string, 0, len(b.Txs))
	for _, tx := range b.Txs {
		hashes = append(hashes, tx.Hash())
	}
	return json.Marshal(canonBlock{
		Height:    b.Height,
		PrevHash:  b.PrevHash,
		Timestamp: b.Timestamp.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano),
		Proposer:  b.Proposer,
		TxHashes:  hashes,
	})
}

func HashBlock(b Block) (string, error) {
	body, err := blockBody(b)
	if err != nil {
		return "", err
	}
	msg := append([]byte(domainBlock+"\n"), body...)
	sum := sha256.Sum256(msg)
	return hex.EncodeToString(sum[:]), nil
}

func SignBlock(kp *identity.KeyPair, b Block) (Block, error) {
	pub := kp.PublicJSON()
	b.Proposer = pub.NodeID
	b.PublicKey = pub.PublicKey
	h, err := HashBlock(b)
	if err != nil {
		return Block{}, err
	}
	b.Hash = h
	b.Signature = kp.SignHex([]byte(h))
	return b, nil
}

func VerifyBlock(b Block) error {
	if b.Height == 0 {
		return fmt.Errorf("block height must be > 0")
	}
	if len(b.Txs) == 0 {
		return fmt.Errorf("empty block")
	}
	if err := checkKey(b.Proposer, b.PublicKey); err != nil {
		return err
	}
	h, err := HashBlock(b)
	if err != nil {
		return err
	}
	if h != b.Hash {
		return fmt.Errorf("block hash mismatch")
	}
	if err := identity.VerifyHex(b.PublicKey, b.Signature, []byte(b.Hash)); err != nil {
		return fmt.Errorf("block signature: %w", err)
	}
	for i, tx := range b.Txs {
		if err := ledger.VerifyTx(tx); err != nil {
			return fmt.Errorf("tx %d: %w", i, err)
		}
	}
	return nil
}

func votePayload(v Vote) string {
	return fmt.Sprintf("%s\n%d\n%s\n%s\n%d",
		domainVote, v.Height, v.BlockHash, v.NodeID, v.Timestamp.UTC().UnixMilli())
}

func SignVote(kp *identity.KeyPair, height uint64, blockHash string, now time.Time) Vote {
	pub := kp.PublicJSON()
	v := Vote{
		Height:    height,
		BlockHash: blockHash,
		NodeID:    pub.NodeID,
		PublicKey: pub.PublicKey,
		Timestamp: now.UTC().Truncate(time.Millisecond),
	}
	v.Signature = kp.SignHex([]byte(votePayload(v)))
	return v
}

func VerifyVote(v Vote) error {
	if v.Height == 0 || v.BlockHash == "" {
		return fmt.Errorf("incomplete vote")
	}
	if err := checkKey(v.NodeID, v.PublicKey); err != nil {
		return err
	}
	return identity.VerifyHex(v.PublicKey, v.Signature, []byte(votePayload(v)))
}

func checkKey(nodeID, pubHex string) error {
	pub, err := identity.ParsePublicKey(pubHex)
	if err != nil {
		return err
	}
	if identity.NodeIDFromPublicKey(pub) != nodeID {
		return fmt.Errorf("node_id does not match public key")
	}
	return nil
}
