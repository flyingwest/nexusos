package ledger

import (
	"sort"
	"time"
)

// MergeState unions src into dst. Last-write-wins for placement and nodes;
// image verifiers are unioned. Absence of a record is not a delete — tombstones
// are required to remove objects across peers.
func MergeState(dst *State, src State) {
	if dst.Nodes == nil {
		dst.Nodes = make(map[string]NodeRecord)
	}
	if dst.Images == nil {
		dst.Images = make(map[string]ImageRecord)
	}
	if dst.Containers == nil {
		dst.Containers = make(map[string]ContainerRecord)
	}
	if dst.Migrations == nil {
		dst.Migrations = make(map[string]MigrationRecord)
	}
	if dst.Tombstones == nil {
		dst.Tombstones = make(map[string]time.Time)
	}
	if dst.Members == nil {
		dst.Members = make(map[string]MemberRecord)
	}
	// Members are consensus-ordered (JoinMember/LeaveMember). Never merge from
	// HTTP snapshot sync or peers can diverge on NextValidatorsHash.

	for k, ts := range src.Tombstones {
		if existing, ok := dst.Tombstones[k]; !ok || ts.After(existing) {
			dst.Tombstones[k] = ts
		}
	}

	for id, remote := range src.Nodes {
		local, ok := dst.Nodes[id]
		if !ok {
			dst.Nodes[id] = remote
			continue
		}
		dst.Nodes[id] = mergeNode(local, remote)
	}

	for digest, remote := range src.Images {
		local, ok := dst.Images[digest]
		if !ok {
			dst.Images[digest] = remote
			continue
		}
		local.VerifiedBy = unionStrings(local.VerifiedBy, remote.VerifiedBy)
		if remote.Size > local.Size {
			local.Size = remote.Size
		}
		if local.FirstSeen.IsZero() || (!remote.FirstSeen.IsZero() && remote.FirstSeen.Before(local.FirstSeen)) {
			local.FirstSeen = remote.FirstSeen
		}
		dst.Images[digest] = local
	}

	for id, remote := range src.Containers {
		if supersededByTomb(dst, ContainerTomb(id), remote.UpdatedAt) {
			delete(dst.Containers, id)
			continue
		}
		local, ok := dst.Containers[id]
		if !ok || remote.UpdatedAt.After(local.UpdatedAt) {
			dst.Containers[id] = remote
		}
	}

	for id, rec := range dst.Containers {
		if supersededByTomb(dst, ContainerTomb(id), rec.UpdatedAt) {
			delete(dst.Containers, id)
		}
	}

	for id, remote := range src.Migrations {
		if supersededByTomb(dst, MigrationTomb(id), migTime(remote)) {
			delete(dst.Migrations, id)
			continue
		}
		local, ok := dst.Migrations[id]
		if !ok || migTime(remote).After(migTime(local)) {
			dst.Migrations[id] = remote
		}
	}

	for id, rec := range dst.Migrations {
		if supersededByTomb(dst, MigrationTomb(id), migTime(rec)) {
			delete(dst.Migrations, id)
		}
	}
}

func mergeNode(local, remote NodeRecord) NodeRecord {
	out := local
	newer := remote
	if local.LastHeartbeat.After(remote.LastHeartbeat) {
		out, newer = local, remote
	} else if remote.LastHeartbeat.After(local.LastHeartbeat) {
		out, newer = remote, local
	} else {
		// Same observation time: Offline must spread, or a dead node stays Online
		// on peers that have not swept yet.
		out.Addresses = unionStrings(local.Addresses, remote.Addresses)
		out.Labels = unionLabels(local.Labels, remote.Labels)
		if local.Status == StatusOffline || remote.Status == StatusOffline {
			out.Status = StatusOffline
		}
		if out.PublicKey == "" {
			out.PublicKey = remote.PublicKey
		}
		if out.Mode == "" {
			out.Mode = remote.Mode
		}
		return out
	}
	out.Addresses = unionStrings(out.Addresses, newer.Addresses)
	out.Labels = unionLabels(out.Labels, newer.Labels)
	return out
}

func supersededByTomb(st *State, key string, updated time.Time) bool {
	ts, ok := st.Tombstones[key]
	if !ok {
		return false
	}
	return !updated.After(ts)
}

func migTime(m MigrationRecord) time.Time {
	if m.FinishedAt != nil && !m.FinishedAt.IsZero() {
		return *m.FinishedAt
	}
	return m.StartedAt
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(a, b...) {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func unionLabels(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
