package app

import "testing"

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
