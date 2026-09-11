package app

import "strings"

type correctionCandidateEdit struct {
	Category string       `json:"category"`
	Kind     string       `json:"kind"`
	Before   *hiredWorker `json:"before,omitempty"`
	After    *hiredWorker `json:"after,omitempty"`
}

// Validate the explicit operations against both snapshots. This preserves the
// difference between removing/adding people and editing an existing entry.
func validCorrectionEdits(before, after rowInput, edits []correctionCandidateEdit) bool {
	if len(edits) == 0 {
		return true
	} // Older clients store only snapshots.
	type entry struct{ category, name, position string }
	counts := func(values rowInput) map[entry]int {
		out := map[entry]int{}
		for category, names := range values.People {
			for _, name := range names {
				out[entry{category, name, ""}]++
			}
		}
		for _, worker := range values.HiredWorkers {
			out[entry{"hired_workers", worker.FullName, worker.Position}]++
		}
		return out
	}
	original, target := counts(before), counts(after)
	added := map[entry]int{}
	for _, edit := range edits {
		if _, ok := peopleCategories[edit.Category]; !ok && edit.Category != "hired_workers" {
			return false
		}
		if edit.Kind != "added" && edit.Kind != "removed" && edit.Kind != "changed" {
			return false
		}
		if (edit.Before != nil) != (edit.Kind != "added") || (edit.After != nil) != (edit.Kind != "removed") {
			return false
		}
		for _, worker := range []*hiredWorker{edit.Before, edit.After} {
			if worker != nil && (strings.TrimSpace(worker.FullName) == "" || worker.FullName != strings.TrimSpace(worker.FullName) || worker.Position != strings.TrimSpace(worker.Position) || (edit.Category != "hired_workers" && worker.Position != "")) {
				return false
			}
		}
		if edit.Before != nil {
			key := entry{edit.Category, edit.Before.FullName, edit.Before.Position}
			if original[key] == 0 {
				return false
			}
			original[key]--
		}
		if edit.After != nil {
			added[entry{edit.Category, edit.After.FullName, edit.After.Position}]++
		}
	}
	for key, n := range added {
		original[key] += n
	}
	for key, n := range original {
		if target[key] != n {
			return false
		}
	}
	for key, n := range target {
		if original[key] != n {
			return false
		}
	}
	return true
}
