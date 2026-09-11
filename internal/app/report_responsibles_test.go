package app

import "testing"

func TestResponsiblePeriod(t *testing.T) {
	tests := []struct {
		name    string
		input   reportResponsiblesInput
		wantEnd string
		openEnd bool
		valid   bool
	}{
		{name: "today", input: reportResponsiblesInput{Date: "2026-09-11", Scope: "today"}, wantEnd: "2026-09-11", valid: true},
		{name: "period", input: reportResponsiblesInput{Date: "2026-09-11", EndDate: "2026-09-18", Scope: "period"}, wantEnd: "2026-09-18", valid: true},
		{name: "forever", input: reportResponsiblesInput{Date: "2026-09-11", Scope: "forever"}, openEnd: true, valid: true},
		{name: "backwards period", input: reportResponsiblesInput{Date: "2026-09-11", EndDate: "2026-09-10", Scope: "period"}},
		{name: "unknown scope", input: reportResponsiblesInput{Date: "2026-09-11", Scope: "week"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, end, valid := responsiblePeriod(tt.input)
			if valid != tt.valid {
				t.Fatalf("valid = %v, want %v", valid, tt.valid)
			}
			if !valid {
				return
			}
			if from != tt.input.Date {
				t.Fatalf("from = %q, want %q", from, tt.input.Date)
			}
			if tt.openEnd && end != nil {
				t.Fatalf("end = %v, want nil", end)
			}
			if !tt.openEnd && (end == nil || *end != tt.wantEnd) {
				t.Fatalf("end = %v, want %q", end, tt.wantEnd)
			}
		})
	}
}
