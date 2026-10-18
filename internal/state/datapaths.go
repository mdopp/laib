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

// StatusPath is the service's status file with ${DATA_DIR} expanded, or ""
// when it declares none (#309).
func StatusPath(st *State, svc *Service) string {
	if svc.Contract.Status == nil {
		return ""
	}
	return strings.ReplaceAll(svc.Contract.Status.File, "${DATA_DIR}", st.Box.DataDir)
}
