package ledger

import (
	"strconv"
	"time"
)

// MessageType identifies ledger transactions.
type MessageType string

const (
	MsgRegisterNode      MessageType = "RegisterNode"
	MsgHeartbeat         MessageType = "Heartbeat"
	MsgRegisterImage     MessageType = "RegisterImage"
	MsgVerifyImage       MessageType = "VerifyImage"
	MsgCreateContainer   MessageType = "CreateContainer"
	MsgUpdateContainer   MessageType = "UpdateContainer"
	MsgRemoveContainer   MessageType = "RemoveContainer"
	MsgProposeMigration  MessageType = "ProposeMigration"
	MsgCompleteMigration MessageType = "CompleteMigration"
	MsgFailMigration     MessageType = "FailMigration"
	MsgJoinMember        MessageType = "JoinMember"
	MsgLeaveMember       MessageType = "LeaveMember"
	MsgCreateWorkload    MessageType = "CreateWorkload"
	MsgUpdateWorkload    MessageType = "UpdateWorkload"
	MsgScaleWorkload     MessageType = "ScaleWorkload"
	MsgDeleteWorkload    MessageType = "DeleteWorkload"
)

// Envelope is a signed ledger message (Phase 2 skeleton).
type Envelope struct {
	Type      MessageType `json:"type"`
	NodeID    string      `json:"node_id"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   jsonRaw     `json:"payload"`
	Signature string      `json:"signature,omitempty"` // hex
}

// jsonRaw avoids importing encoding/json cycles in comments; use []byte alias.
type jsonRaw []byte

const (
	StatusOnline   = "Online"
	StatusOffline  = "Offline"
	StatusDraining = "Draining"
)

// NodeRecord is the network view of a node.
type NodeRecord struct {
	NodeID        string            `json:"node_id"`
	PublicKey     string            `json:"public_key"`
	Addresses     []string          `json:"addresses"`
	CapacityCPU   uint32            `json:"capacity_cpu_millis"`
	CapacityMem   uint64            `json:"capacity_memory_bytes"`
	Labels        map[string]string `json:"labels,omitempty"`
	Status        string            `json:"status"` // Online, Offline, Draining
	LastHeartbeat time.Time         `json:"last_heartbeat"`
	Mode          string            `json:"mode"`
}

// ImageRecord is network-wide image integrity state.
type ImageRecord struct {
	Digest     string    `json:"digest"`
	Size       int64     `json:"size"`
	VerifiedBy []string  `json:"verified_by"` // node IDs
	FirstSeen  time.Time `json:"first_seen"`
}

// ContainerRecord is network-wide placement state.
type ContainerRecord struct {
	ContainerID  string            `json:"container_id"`
	ImageDigest  string            `json:"image_digest"`
	Owner        string            `json:"owner,omitempty"`
	Desired      string            `json:"desired_state"`
	CurrentNode  string            `json:"current_node"`
	WorkloadID   string            `json:"workload_id,omitempty"`
	ReplicaIndex *uint32           `json:"replica_index,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// ResourceSpec is a coarse resource request (Phase 3; scheduler may ignore).
type ResourceSpec struct {
	CPUMillis   uint32 `json:"cpu_millis,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	DiskBytes   uint64 `json:"disk_bytes,omitempty"`
}

// WorkloadStatus is reported desired/current/available replica counts.
type WorkloadStatus struct {
	Desired   uint32 `json:"desired"`
	Current   uint32 `json:"current"`
	Available uint32 `json:"available"`
}

// WorkloadRecord is a declarative multi-replica placement intent (Phase 3).
type WorkloadRecord struct {
	WorkloadID  string            `json:"workload_id"`
	Owner       string            `json:"owner,omitempty"`
	ImageDigest string            `json:"image_digest"`
	ImageRef    string            `json:"image_ref,omitempty"`
	Replicas    uint32            `json:"replicas"`
	Resources   ResourceSpec      `json:"resources,omitempty"`
	Strategy       string            `json:"strategy,omitempty"` // Recreate | RollingUpdate
	MaxUnavailable uint32            `json:"max_unavailable,omitempty"` // RollingUpdate budget; 0 => 1
	Labels         map[string]string `json:"labels,omitempty"`
	Status         WorkloadStatus    `json:"status"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// Workload update strategies (Phase 3).
const (
	StrategyRecreate      = "Recreate"
	StrategyRollingUpdate = "RollingUpdate"
)


// Container desired states.
const (
	DesiredRunning   = "Running"
	DesiredStopped   = "Stopped"
	DesiredMigrating = "Migrating"
)

// Migration status values.
const (
	MigrationPending    = "Pending"
	MigrationInProgress = "InProgress"
	MigrationSuccess    = "Success"
	MigrationFailed     = "Failed"
)

// MigrationRecord tracks a coordinated move.
type MigrationRecord struct {
	MigrationID    string     `json:"migration_id"`
	ContainerID    string     `json:"container_id"`
	FromNode       string     `json:"from_node"`
	ToNode         string     `json:"to_node"`
	Status         string     `json:"status"`
	CheckpointHash string     `json:"checkpoint_hash,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// MemberRecord is consensus-ordered permissioned membership (join/pair/leave).
// Validator updates for CometBFT are derived from this map, not from the
// local HTTP membership cache alone.
type MemberRecord struct {
	NodeID    string    `json:"node_id"`
	PublicKey string    `json:"public_key"`
	Addresses []string  `json:"addresses,omitempty"`
	Label     string    `json:"label,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// State is the in-memory network ledger view.
type State struct {
	Nodes      map[string]NodeRecord      `json:"nodes"`
	Images     map[string]ImageRecord     `json:"images"`
	Containers map[string]ContainerRecord `json:"containers"`
	Migrations map[string]MigrationRecord `json:"migrations"`
	Workloads  map[string]WorkloadRecord  `json:"workloads,omitempty"`
	// Members is the consensus-ordered membership set (JoinMember/LeaveMember).
	// HTTP snapshot sync must not merge this field — see MergeState.
	Members map[string]MemberRecord `json:"members,omitempty"`
	// Tombstones records deletes so peer merge does not resurrect removed objects.
	// Key format: "container:<id>", "migration:<id>", "member:<id>", "workload:<id>".
	// Member leave stamps member:<id> so AppHash reflects eviction even after
	// the Members map entry is removed.
	Tombstones map[string]time.Time `json:"tombstones,omitempty"`
}

// NewState creates an empty ledger state.
func NewState() *State {
	return &State{
		Nodes:      make(map[string]NodeRecord),
		Images:     make(map[string]ImageRecord),
		Containers: make(map[string]ContainerRecord),
		Migrations: make(map[string]MigrationRecord),
		Workloads:  make(map[string]WorkloadRecord),
		Members:    make(map[string]MemberRecord),
		Tombstones: make(map[string]time.Time),
	}
}

func ContainerTomb(id string) string { return "container:" + id }
func MigrationTomb(id string) string { return "migration:" + id }
func MemberTomb(id string) string    { return "member:" + id }
func WorkloadTomb(id string) string  { return "workload:" + id }

// ReplicaContainerID is the deterministic ledger container id for a workload replica.
func ReplicaContainerID(workloadID string, replica uint32) string {
	return "wl:" + workloadID + ":" + strconv.FormatUint(uint64(replica), 10)
}
