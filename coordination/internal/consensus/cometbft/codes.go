package cometbft

// ABCI response codes for the NexusOS spike application.
const (
	CodeOK            uint32 = 0
	CodeDecode        uint32 = 1
	CodeBadSig        uint32 = 2
	CodeNotMember     uint32 = 3
	CodeUnsupported   uint32 = 4
	CodeApply         uint32 = 5
)
