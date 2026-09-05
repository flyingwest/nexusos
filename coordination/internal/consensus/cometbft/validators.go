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
// validator suggestion (equal weight; permissioned).
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
// Limits (documented for operators — full dynamic EndBlock sync is deferred):
//   - Genesis / voting set for the in-process node is still the local CometBFT
//     FilePV (single-node bootstrap), not this list.
//   - Dynamic validator updates via ResponseFinalizeBlock.ValidatorUpdates are
//     not applied yet; pairing does not resize the CometBFT set.
//   - NexusOS node identity keys and CometBFT consensus keys are separate
//     keyrings today; this helper only proposes a mapping when PublicKey is set.
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

// ValidatorUpdatesFromMembership builds ABCI ValidatorUpdate messages for a
// future EndBlock path. Returns only members with usable 32-byte Ed25519 keys.
func ValidatorUpdatesFromMembership(members *membership.Store) []abcitypes.ValidatorUpdate {
	suggested := SuggestedValidatorsFromMembership(members)
	out := make([]abcitypes.ValidatorUpdate, 0, len(suggested))
	for _, s := range suggested {
		if s.PubKeyHex == "" {
			continue
		}
		raw, err := hex.DecodeString(s.PubKeyHex)
		if err != nil || len(raw) != cmted25519.PubKeySize {
			continue
		}
		out = append(out, abcitypes.Ed25519ValidatorUpdate(raw, s.Power))
	}
	return out
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

// WriteMembershipValidatorSnapshot writes suggested validators under the
// CometBFT home for operator inspection (scaffolding, not consensus input).
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
