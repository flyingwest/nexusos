package cometbft

import (
	"encoding/json"
	"fmt"

	"github.com/nexusos/coordination/internal/ledger"
)

// EncodeTx serializes a ledger.Tx for the ABCI wire (JSON).
// Same bytes are what CheckTx / FinalizeBlock decode.
func EncodeTx(tx ledger.Tx) ([]byte, error) {
	b, err := json.Marshal(tx)
	if err != nil {
		return nil, fmt.Errorf("encode ledger tx: %w", err)
	}
	return b, nil
}

// DecodeTx parses ABCI tx bytes into a ledger.Tx.
func DecodeTx(bz []byte) (ledger.Tx, error) {
	var tx ledger.Tx
	if err := json.Unmarshal(bz, &tx); err != nil {
		return ledger.Tx{}, fmt.Errorf("decode ledger tx: %w", err)
	}
	return tx, nil
}
