package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestCheckDebtsterAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == debtsterVacanciesPath {
			_, _ = w.Write([]byte(`{"error_code":0,"status":"success","data":[{"id":12,"rp":"РП Алматы"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":12,"name":"rp_almaty","display_name":"РП Алматы"},{"id":18,"name":"rp_astana","display_name":"РП Астана"}]}`))
	}))
	defer server.Close()

	departments, vacancies, err := CheckDebtsterAPI(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if departments != 2 || vacancies != 1 {
		t.Fatalf("got %d departments and %d vacancies, want 2 and 1", departments, vacancies)
	}
}

func TestFetchAndApplyDebtsterVacancies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != debtsterVacanciesPath || r.URL.Query().Get("report_date") != "2026-08-28" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error_code":0,"status":"success","data":[{"id":12,"rp":"РП Алматы","staff_positions_count":50,"active_employees_count":42,"vacant_positions_count":8,"trainees_count":3,"recruitment_count":2,"planned_dismissals_count":1,"planned_dismissals":[{"first_name":" Иван ","last_name":" Иванов ","middle_name":" Иванович "}]}]}`))
	}))
	defer server.Close()

	vacancies, err := fetchDebtsterVacancies(context.Background(), server.Client(), server.URL, "2026-08-28")
	if err != nil {
		t.Fatal(err)
	}
	rows := []reportRow{{OfficeID: "12", OpenVacancies: 7, PlannedReserve: 9}, {OfficeID: "18", OpenVacancies: 4}}
	applyDebtsterVacancies(rows, vacancies)
	if rows[0].StaffPositionsCount != 50 || rows[0].PlannedDismissalsCount != 1 || len(rows[0].PlannedDismissals) != 1 {
		t.Fatalf("Debtster values were not applied: %#v", rows[0])
	}
	if rows[0].PlannedDismissals[0].FirstName != "Иван" {
		t.Fatalf("name was not normalized: %#v", rows[0].PlannedDismissals[0])
	}
	if rows[0].OpenVacancies != 7 || rows[0].PlannedReserve != 9 {
		t.Fatalf("manual values were changed: %#v", rows[0])
	}
	if rows[1].StaffPositionsCount != 0 || rows[1].OpenVacancies != 4 {
		t.Fatalf("unmatched row was changed: %#v", rows[1])
	}
}

func TestFetchDebtsterTrainees(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != debtsterTraineesPath {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("report_date") != "2026-09-10" || r.URL.Query().Get("department_id") != "15" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error_code":0,"status":"success","message":"","data":[{"report_type_id":2,"department":"РП Алматы","report_date":"2026-09-10","reporter_id":41,"trainee_id":null,"full_name":"Иванов Иван","status_id":"стажируется по теории","source":"ОК","interview_date":"2026-09-01","security_approval_date":"2026-09-02","internship_start_date":"2026-09-03","task_start_date":null,"note":"Изучает материалы","file_name":"","file_object_key":""}]}`))
	}))
	defer server.Close()

	rows, err := fetchDebtsterTrainees(context.Background(), server.Client(), server.URL, "2026-09-10", 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Department != "РП Алматы" || rows[0].TraineeID != nil || rows[0].TaskStartDate != nil {
		t.Fatalf("unexpected trainees: %#v", rows)
	}
}

func TestFetchDebtsterTraineesReturnsEmptySlice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error_code":0,"status":"success","data":null}`))
	}))
	defer server.Close()

	rows, err := fetchDebtsterTrainees(context.Background(), server.Client(), server.URL, "2026-09-10", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("got %#v, want a non-nil empty slice", rows)
	}
}

func TestApplyDebtsterTraineeChanges(t *testing.T) {
	rows := []reportRow{{OfficeID: "12"}, {OfficeID: "18"}, {OfficeID: "24"}}
	vacancies := []debtsterVacancyReport{
		{ID: 12, TraineesCount: 5},
		{ID: 24, TraineesCount: 1},
	}
	baselines := map[int]int{12: 3, 18: 4, 24: 3}

	applyDebtsterTraineeChanges(rows, vacancies, baselines)

	if rows[0].TraineesCount != 5 || rows[0].TraineesCountChange != 2 {
		t.Fatalf("positive change = %#v, want count 5 and change 2", rows[0])
	}
	if rows[1].TraineesCount != 4 || rows[1].TraineesCountChange != 0 {
		t.Fatalf("cached fallback = %#v, want count 4 and no change", rows[1])
	}
	if rows[2].TraineesCount != 1 || rows[2].TraineesCountChange != -2 {
		t.Fatalf("negative change = %#v, want count 1 and change -2", rows[2])
	}
}

func TestAppendMissingDebtsterVacancyRowsDoesNotChangeExistingRows(t *testing.T) {
	rows := []reportRow{{ID: "saved-row", OfficeID: "12", OfficeName: "Сохранённое название", OpenVacancies: 7}}
	vacancies := []debtsterVacancyReport{{ID: 12, RP: "РП Алматы"}, {ID: 18, RP: "РП Астана", StaffPositionsCount: 40}}

	rows = appendMissingDebtsterVacancyRows(rows, vacancies, 16)
	applyDebtsterVacancies(rows, vacancies)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].ID != "saved-row" || rows[0].OfficeName != "Сохранённое название" || rows[0].OpenVacancies != 7 {
		t.Fatalf("existing row was changed: %#v", rows[0])
	}
	if rows[1].ID != "" || rows[1].OfficeID != "18" || rows[1].OfficeName != "РП Астана" || rows[1].StaffPositionsCount != 40 {
		t.Fatalf("missing read-only row was not appended: %#v", rows[1])
	}
}

func TestFetchDebtsterDepartmentsExplainsHTMLResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Login</title>`))
	}))
	defer server.Close()

	_, err := fetchDebtsterDepartments(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "returned HTML instead of JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckDebtsterAPIStillChecksVacanciesWhenDepartmentsFail(t *testing.T) {
	vacanciesCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == debtsterVacanciesPath {
			vacanciesCalled = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"error_code":0,"status":"success","data":[]}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Login</title>`))
	}))
	defer server.Close()

	_, _, err := CheckDebtsterAPI(context.Background(), server.URL)
	if err == nil || !vacanciesCalled || !strings.Contains(err.Error(), "vacancies: ok") {
		t.Fatalf("unexpected result: called=%v err=%v", vacanciesCalled, err)
	}
}
