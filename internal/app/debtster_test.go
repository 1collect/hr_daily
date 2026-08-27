package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchDebtsterDepartments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != debtsterDepartmentsPath {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":12,"name":" rp_almaty ","display_name":" РП Алматы "},{"id":12,"name":"duplicate","display_name":"Duplicate"},{"id":18,"name":"rp_astana","display_name":"РП Астана"}]}`))
	}))
	defer server.Close()

	got, err := fetchDebtsterDepartments(context.Background(), server.Client(), server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d departments, want 2", len(got))
	}
	if got[0].ID != 12 || got[0].Name != "rp_almaty" || got[0].DisplayName != "РП Алматы" {
		t.Fatalf("unexpected first department: %#v", got[0])
	}
}

func TestFetchDebtsterDepartmentsRejectsErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if _, err := fetchDebtsterDepartments(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("expected an error for non-2xx response")
	}
}

func TestUsesDebtsterDepartmentsFromAugust28(t *testing.T) {
	if usesDebtsterDepartments("2026-08-27") {
		t.Fatal("Debtster departments enabled before 2026-08-28")
	}
	for _, date := range []string{"2026-08-28", "2026-08-29", "2027-01-01"} {
		if !usesDebtsterDepartments(date) {
			t.Fatalf("Debtster departments are disabled for %s", date)
		}
	}
}

func TestShouldSyncDebtsterDepartmentsOnlyForCurrentDate(t *testing.T) {
	today := "2026-08-29"
	for _, date := range []string{"2026-08-28", "2026-08-30", "2027-01-01"} {
		if shouldSyncDebtsterDepartments(date, today) {
			t.Fatalf("Debtster sync enabled for non-current date %s", date)
		}
	}
	if !shouldSyncDebtsterDepartments(today, today) {
		t.Fatal("Debtster sync disabled for current date")
	}
	if shouldSyncDebtsterDepartments("2026-08-27", "2026-08-27") {
		t.Fatal("Debtster sync enabled before 2026-08-28")
	}
}
