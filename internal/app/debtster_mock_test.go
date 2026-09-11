package app

import (
	"context"
	"net/http"
	"testing"
)

func TestDebtsterMockResponses(t *testing.T) {
	ctx := context.Background()
	client := newDebtsterClient(true)
	const base = "https://debtster.invalid"
	departments, err := fetchDebtsterDepartments(ctx, client, base)
	if err != nil || len(departments) != 3 {
		t.Fatalf("departments: %v, %v", departments, err)
	}
	vacancies, err := fetchDebtsterVacancies(ctx, client, base, "2026-09-10")
	if err != nil || len(vacancies) != 3 {
		t.Fatalf("vacancies: %v, %v", vacancies, err)
	}
	for i, department := range departments {
		if vacancies[i].ID != department.ID {
			t.Fatal("department IDs do not match")
		}
		trainees, err := fetchDebtsterTrainees(ctx, client, base, "2026-09-10", department.ID)
		if err != nil || len(trainees) != vacancies[i].TraineesCount {
			t.Fatalf("trainees: %v, %v", trainees, err)
		}
		for _, trainee := range trainees {
			if trainee.ReportDate != "2026-09-10" || trainee.Department != department.DisplayName {
				t.Fatalf("wrong trainee: %+v", trainee)
			}
		}
	}
	all, err := fetchDebtsterTrainees(ctx, client, base, "2026-09-10", 0)
	if err != nil || len(all) != 6 {
		t.Fatalf("all trainees: %v, %v", all, err)
	}
	empty, err := fetchDebtsterTrainees(ctx, client, base, "2026-09-10", 123)
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown department: %v, %v", empty, err)
	}
	if _, err = client.Get(base + "/unknown"); err == nil {
		t.Fatal("unknown endpoints must fail without network fallback")
	}
	count, vacancyCount, err := CheckDebtsterAPI(ctx, base, true)
	if err != nil || count != 3 || vacancyCount != 3 {
		t.Fatalf("check: %d %d %v", count, vacancyCount, err)
	}
}

func TestDebtsterClientDefaultsToRealHTTP(t *testing.T) {
	if client := newDebtsterClient(false); client.Transport != nil {
		t.Fatal("real mode must use default HTTP transport")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://debtster.invalid"+debtsterDepartmentsPath, nil)
	if _, err := (debtsterMockTransport{}).RoundTrip(req); err == nil {
		t.Fatal("mock must honor cancellation")
	}
}
