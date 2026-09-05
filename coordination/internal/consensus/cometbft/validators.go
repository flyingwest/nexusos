package cometbft

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	abcitypes "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/privval"
	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/nexusos/coordination/internal/membership"
)

// DefaultValidatorPower is voting power assigned to each membership-derived
// validator (equal weight; permissioned).
const DefaultValidatorPower int64 = 10

// MembershipValidator is a suggested CometBFT validator derived from a NexusOS member.
type MembershipValidator struct {
	NodeID    string `json:"node_id"`
	PubKeyHex string `json:"pubkey_hex,omitempty"`
	Power     int64  `json:"power"`
	Address   string `json:"address,omitempty"`
	Note      string `json:"note,omitempty"`
}

// SuggestedValidatorsFromMembership maps membership public keys to CometBFT
// Ed25519 pubkeys when the member stores a 32-byte hex public key (NexusOS
// identity). Members without a usable key are listed with a note.
//
// Power / pubkey mapping rules:
//   - Power: every usable member gets DefaultValidatorPower (equal weight).
//   - PubKey: member PublicKey hex must decode to exactly 32 bytes (Ed25519).
//     NodeID is hex(pubkey) in NexusOS, so PublicKey and NodeID normally match.
//   - Keyring correlation: when the in-process node is started with the NexusOS
//     identity private key, FilePV consensus key == identity pubkey, so the
//     local member can vote. Pre-existing random FilePV keys do not match;
//     see docs/cometbft-spike.md (delete cometbft dir once to re-seed).
func SuggestedValidatorsFromMembership(members *membership.Store) []MembershipValidator {
	if members == nil {
		return nil
	}
	list := members.List()
	sort.Slice(list, func(i, j int) bool { return list[i].NodeID < list[j].NodeID })
	out := make([]MembershipValidator, 0, len(list))
	for _, m := range list {
		sv := MembershipValidator{
			NodeID: m.NodeID,
			Power:  DefaultValidatorPower,
		}
		pk, err := decodeMemberPubKey(m.PublicKey)
		if err != nil {
			sv.Note = err.Error()
			out = append(out, sv)
			continue
		}
		sv.PubKeyHex = hex.EncodeToString(pk)
		sv.Address = fmt.Sprintf("%X", cmted25519.PubKey(pk).Address())
		out = append(out, sv)
	}
	return out
}

// MembershipPowerByPubKey returns pubkey-hex → power for members with usable keys.
func MembershipPowerByPubKey(members *membership.Store) map[string]int64 {
	out := make(map[string]int64)
	for _, s := range SuggestedValidatorsFromMembership(members) {
		if s.PubKeyHex == "" {
			continue
		}
		out[s.PubKeyHex] = s.Power
	}
	return out
}

// ValidatorUpdatesFromMembership builds ABCI ValidatorUpdate messages for the
// full desired set (not a diff). Prefer DiffValidatorUpdates for EndBlock.
func ValidatorUpdatesFromMembership(members *membership.Store) []abcitypes.ValidatorUpdate {
	desired := MembershipPowerByPubKey(members)
	keys := sortedKeys(desired)
	out := make([]abcitypes.ValidatorUpdate, 0, len(keys))
	for _, hexKey := range keys {
		raw, err := hex.DecodeString(hexKey)
		if err != nil || len(raw) != cmted25519.PubKeySize {
			continue
		}
		out = append(out, abcitypes.Ed25519ValidatorUpdate(raw, desired[hexKey]))
	}
	return out
}

// DiffValidatorUpdates computes ABCI updates to move current → desired.
//
// Safety:
//   - If desired is empty, returns nil (preserve genesis / current set). This
//     keeps single-node FilePV voting when membership is open/empty or has no keys.
//   - Never returns an update set that would leave zero validators with power > 0.
//   - If localPubKeyHex is non-empty and not in desired: allow only deterministic
//     self-removal (desired ⊆ current and every non-local current key stays).
//     Refuse replacing the FilePV set with unrelated keys.
//   - Removals use power 0 (LeaveMember / eviction). Active validators may remove
//     peers; non-validator locals may only expand (desired ⊇ current).
//   - Ledger ApplyTx refuses LeaveMember that would empty MembershipPower, so
//     desired should not collapse to empty via a successful leave tx.
func DiffValidatorUpdates(current, desired map[string]int64, localPubKeyHex string) []abcitypes.ValidatorUpdate {
	if len(desired) == 0 {
		return nil
	}
	if localPubKeyHex != "" {
		if _, ok := desired[localPubKeyHex]; !ok {
			// Local not in desired: allow only when this is a deterministic
			// self-removal (or removal of local plus no other churn) so the
			// leaving node emits the same ValidatorUpdates as remaining peers.
			// Refuse if desired introduces keys absent from current (would
			// replace the FilePV set with keys this node cannot sign with).
			for k := range desired {
				if _, inCurrent := current[k]; !inCurrent {
					return nil
				}
			}
			for k := range current {
				if k == localPubKeyHex {
					continue
				}
				if _, still := desired[k]; !still {
					return nil // would also drop other validators — unsafe/ambiguous
				}
			}
			// Fall through: compute power-0 removal of local (and only local).
		} else if _, localActive := current[localPubKeyHex]; !localActive && len(current) > 0 {
			// If this node is not yet an active validator, only allow pure expansions
			// (desired must contain every current key). Prevents a freshly started peer
			// whose membership is still {self} from proposing to replace genesis.
			for k := range current {
				if _, ok := desired[k]; !ok {
					return nil
				}
			}
		}
	}

	// Simulate applying the diff; refuse if it would empty the set.
	next := make(map[string]int64, len(current)+len(desired))
	for k, v := range current {
		next[k] = v
	}

	var updates []abcitypes.ValidatorUpdate
	// Removals / power changes for keys leaving desired.
	for hexKey, curPower := range current {
		want, ok := desired[hexKey]
		if !ok {
			raw, err := hex.DecodeString(hexKey)
			if err != nil || len(raw) != cmted25519.PubKeySize {
				continue
			}
			updates = append(updates, abcitypes.Ed25519ValidatorUpdate(raw, 0))
			delete(next, hexKey)
			continue
		}
		if want != curPower {
			raw, _ := hex.DecodeString(hexKey)
			updates = append(updates, abcitypes.Ed25519ValidatorUpdate(raw, want))
			next[hexKey] = want
		}
	}
	// Additions.
	for hexKey, want := range desired {
		if _, ok := current[hexKey]; ok {
			continue
		}
		raw, err := hex.DecodeString(hexKey)
		if err != nil || len(raw) != cmted25519.PubKeySize {
			continue
		}
		updates = append(updates, abcitypes.Ed25519ValidatorUpdate(raw, want))
		next[hexKey] = want
	}

	alive := 0
	for _, p := range next {
		if p > 0 {
			alive++
		}
	}
	if alive == 0 {
		return nil
	}

	// Stable order for determinism across nodes.
	sort.Slice(updates, func(i, j int) bool {
		ai := hex.EncodeToString(updates[i].PubKey.GetEd25519())
		aj := hex.EncodeToString(updates[j].PubKey.GetEd25519())
		if ai != aj {
			return ai < aj
		}
		return updates[i].Power < updates[j].Power
	})
	return updates
}

// ApplyValidatorUpdatesMut applies updates to a pubkey→power map (test / tracking helper).
func ApplyValidatorUpdatesMut(set map[string]int64, updates []abcitypes.ValidatorUpdate) {
	for _, u := range updates {
		pk := u.PubKey.GetEd25519()
		if len(pk) != cmted25519.PubKeySize {
			continue
		}
		hexKey := hex.EncodeToString(pk)
		if u.Power == 0 {
			delete(set, hexKey)
			continue
		}
		set[hexKey] = u.Power
	}
}

func decodeMemberPubKey(pubHex string) ([]byte, error) {
	if pubHex == "" {
		return nil, fmt.Errorf("no public_key on member; cannot map to CometBFT validator")
	}
	raw, err := hex.DecodeString(pubHex)
	if err != nil {
		return nil, fmt.Errorf("public_key not hex: %w", err)
	}
	if len(raw) != cmted25519.PubKeySize {
		return nil, fmt.Errorf("public_key length %d want %d", len(raw), cmted25519.PubKeySize)
	}
	return raw, nil
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WriteMembershipValidatorSnapshot writes suggested validators under the
// CometBFT home for operator inspection.
func WriteMembershipValidatorSnapshot(home string, members *membership.Store) error {
	if home == "" {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	path := filepath.Join(home, "validators-from-membership.json")
	b, err := json.MarshalIndent(SuggestedValidatorsFromMembership(members), "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// genesisValidatorFromFilePV builds the single genesis validator for local bootstrap.
func genesisValidatorFromFilePV(pv *privval.FilePV, name string) (cmttypes.GenesisValidator, error) {
	pub, err := pv.GetPubKey()
	if err != nil {
		return cmttypes.GenesisValidator{}, err
	}
	if name == "" {
		name = "nexusos-local"
	}
	return cmttypes.GenesisValidator{
		Address: pub.Address(),
		PubKey:  pub,
		Power:   DefaultValidatorPower,
		Name:    name,
	}, nil
}

// validatorUpdatePubKeyHex extracts ed25519 pubkey hex from an ABCI update.
func validatorUpdatePubKeyHex(u abcitypes.ValidatorUpdate) (string, bool) {
	pk := u.PubKey.GetEd25519()
	if len(pk) != cmted25519.PubKeySize {
		return "", false
	}
	return hex.EncodeToString(pk), true
}
