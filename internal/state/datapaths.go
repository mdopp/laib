package state

import "strings"

// DataPaths expands a service's declared data paths.
func DataPaths(st *State, svc *Service) []string {
	var out []string
	for _, d := range svc.Contract.Data {
		out = append(out, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
	}
	return out
}

// SnapshotPaths are the data paths worth a pre-repair snapshot: the ones
// the service backs up (backup: false marks caches and bulk media).
func SnapshotPaths(st *State, svc *Service) []string {
	var out []string
	for _, d := range svc.Contract.Data {
		if d.Backup != nil && !*d.Backup {
			continue
		}
		out = append(out, strings.ReplaceAll(d.Path, "${DATA_DIR}", st.Box.DataDir))
	}
	return out
}
