package app

import "testing"

func TestCorrectionEditsMixedOperations(t *testing.T) {
	worker := func(name string) *hiredWorker { return &hiredWorker{FullName: name} }
	before := normalizeCorrectionValues(rowInput{People: map[string][]string{"invited_candidates": {"Removed", "Old name", "Unchanged"}}})
	after := normalizeCorrectionValues(rowInput{People: map[string][]string{"invited_candidates": {"New name", "Unchanged", "Added"}}})
	edits := []correctionCandidateEdit{
		{Category: "invited_candidates", Kind: "removed", Before: worker("Removed")},
		{Category: "invited_candidates", Kind: "changed", Before: worker("Old name"), After: worker("New name")},
		{Category: "invited_candidates", Kind: "added", After: worker("Added")},
	}
	if !validCorrectionEdits(before, after, edits) {
		t.Fatal("mixed operations rejected")
	}
	if validCorrectionEdits(before, after, edits[:2]) {
		t.Fatal("missing addition accepted")
	}
	edits[0].Before = worker("Unknown")
	if validCorrectionEdits(before, after, edits) {
		t.Fatal("stale or forged original accepted")
	}
}

func TestCorrectionEditsPositionAndDuplicates(t *testing.T) {
	before := normalizeCorrectionValues(rowInput{HiredWorkers: []hiredWorker{{FullName: "Same", Position: "Old"}, {FullName: "Same", Position: "Old"}}})
	after := normalizeCorrectionValues(rowInput{HiredWorkers: []hiredWorker{{FullName: "Same", Position: "New"}}})
	edits := []correctionCandidateEdit{{Category: "hired_workers", Kind: "changed", Before: &before.HiredWorkers[0], After: &after.HiredWorkers[0]}, {Category: "hired_workers", Kind: "removed", Before: &before.HiredWorkers[1]}}
	if !validCorrectionEdits(before, after, edits) {
		t.Fatal("duplicate names and position change rejected")
	}
	if validCorrectionEdits(before, after, append(edits, edits[1])) {
		t.Fatal("duplicate removal accepted")
	}
}
