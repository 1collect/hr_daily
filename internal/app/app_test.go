package app

import (
	"testing"
	"time"
)

func TestEfficiency(t *testing.T) {
	tests := []struct {
		interns, plan int
		want          float64
	}{{0, 0, 0}, {3, 0, 0}, {4, 8, 50}, {3, 16, 18.75}}
	for _, tt := range tests {
		if got := Efficiency(tt.interns, tt.plan); got != tt.want {
			t.Fatalf("Efficiency(%d,%d)=%v, want %v", tt.interns, tt.plan, got, tt.want)
		}
	}
}

func TestValidDate(t *testing.T) {
	if !validDate("2026-08-06") {
		t.Fatal("valid date rejected")
	}
	for _, value := range []string{"", "06.08.2026", "2026-02-30"} {
		if validDate(value) {
			t.Fatalf("invalid date accepted: %s", value)
		}
	}
}

func TestValidatePastReportDate(t *testing.T) {
	today, err := time.Parse("2006-01-02", localToday())
	if err != nil {
		t.Fatal(err)
	}
	if got := validatePastReportDate(today.AddDate(0, 0, -1).Format("2006-01-02")); got != "" {
		t.Fatalf("past date rejected: %s", got)
	}
	for _, value := range []string{localToday(), today.AddDate(0, 0, 1).Format("2006-01-02")} {
		if got := validatePastReportDate(value); got == "" {
			t.Fatalf("non-past date accepted: %s", value)
		}
	}
}

func TestUniqueUserIDs(t *testing.T) {
	got := uniqueUserIDs([]string{" first ", "second", "first", "", "second"})
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("unexpected unique ids: %#v", got)
	}
}
