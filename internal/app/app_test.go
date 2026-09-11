package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestEfficiency(t *testing.T) {
	tests := []struct {
		interviewed, plan int
		want              float64
	}{{0, 0, 0}, {3, 0, 0}, {4, 8, 50}, {3, 16, 18.75}}
	for _, tt := range tests {
		if got := Efficiency(tt.interviewed, tt.plan); got != tt.want {
			t.Fatalf("Efficiency(%d,%d)=%v, want %v", tt.interviewed, tt.plan, got, tt.want)
		}
	}
}

func TestPriorBusinessDate(t *testing.T) {
	tests := map[string]string{
		"2026-09-11": "2026-09-10",
		"2026-09-07": "2026-09-04",
	}
	for date, want := range tests {
		got, ok := priorBusinessDate(date)
		if !ok || got != want {
			t.Fatalf("priorBusinessDate(%q) = %q, %v; want %q, true", date, got, ok, want)
		}
	}
	if _, ok := priorBusinessDate("not-a-date"); ok {
		t.Fatal("invalid date accepted")
	}
}

func TestCleanHiredWorkersKeepsOptionalPosition(t *testing.T) {
	got := cleanHiredWorkers([]hiredWorker{
		{FullName: "  Иванов Иван  ", Position: "  Бухгалтер  "},
		{FullName: "   ", Position: "Не должна сохраниться"},
		{FullName: "Петров Пётр", Position: "   "},
	})
	if len(got) != 2 {
		t.Fatalf("got %d workers, want 2", len(got))
	}
	if got[0].FullName != "Иванов Иван" || got[0].Position != "Бухгалтер" {
		t.Fatalf("unexpected first worker: %#v", got[0])
	}
	if got[1].Position != "" {
		t.Fatalf("optional empty position was not normalized: %#v", got[1])
	}
}

func TestEmployeeCanBeCreatedWithoutPlan(t *testing.T) {
	in := userInput{Username: "employee", Password: "password", Role: "employee", FirstName: "Иван", LastName: "Иванов"}
	if got := validateUserInput(in, true); got != "" {
		t.Fatalf("employee without plan rejected: %s", got)
	}
}

func TestTotalsUsesPlanOnce(t *testing.T) {
	rows := []reportRow{{InterviewedCandidates: 2, PlannedReserve: 4, HiredWorkers: []hiredWorker{{FullName: "Первый"}}, EfficiencyPlan: 10}, {InterviewedCandidates: 3, PlannedReserve: 6, HiredWorkers: []hiredWorker{{FullName: "Второй"}}, EfficiencyPlan: 10}}
	got := totals(rows, candidatePlans{Invited: 8, Hired: 10})
	if got["efficiencyPlan"] != 10 || got["hiringEfficiency"] != 20.0 {
		t.Fatalf("unexpected totals: %#v", got)
	}
	if got["plannedReserve"] != 10 {
		t.Fatalf("planned reserve total=%v, want 10", got["plannedReserve"])
	}
	if got["hiredWorkers"] != 2 {
		t.Fatalf("hired workers total=%v, want 2", got["hiredWorkers"])
	}
}

func TestExportTotalUsesPeriodPlanOnce(t *testing.T) {
	f := excelize.NewFile()
	f.SetSheetName("Sheet1", "РП")
	rows := []exportSummaryRow{
		{Office: "РП 1", Invited: 4, Hired: 3, Plan: 10, TotalPlan: 10, InvitationPlan: 8, TotalInvitationPlan: 8},
		{Office: "РП 2", Invited: 2, Hired: 2, Plan: 10, TotalPlan: 10, InvitationPlan: 8, TotalInvitationPlan: 8},
	}
	writeExportSummarySheet(f, exportKinds["rp"], rows, nil, newExportStyles(f))
	if header, err := f.GetCellValue("РП", "F1"); err != nil || header != "Планируемый резерв" {
		t.Fatalf("planned reserve header=%q, err=%v", header, err)
	}
	if header, err := f.GetCellValue("РП", "H1"); err != nil || header != "Количество принятых работников" {
		t.Fatalf("hired workers header=%q, err=%v", header, err)
	}
	if header, _ := f.GetCellValue("РП", "J1"); header != "% исполнения плана по приглашенным кандидатам" {
		t.Fatalf("invitation efficiency header=%q", header)
	}
	if header, _ := f.GetCellValue("РП", "K1"); header != "% исполнения плана по принятым кандидатам" {
		t.Fatalf("hiring efficiency header=%q", header)
	}
	got, err := f.GetCellValue("РП", "K4")
	if err != nil {
		t.Fatal(err)
	}
	if got != "50.00" {
		t.Fatalf("export total efficiency=%q, want 50.00", got)
	}
}

func TestApplyDebtsterExportSnapshotUsesFinalDateCounts(t *testing.T) {
	rows := []exportSummaryRow{
		{DebtsterDepartmentID: 12, OpenVacancies: 90, Interns: 80, PlannedReserve: 70},
		{DebtsterDepartmentID: 18, OpenVacancies: 9, Interns: 8, PlannedReserve: 7},
		{Office: "  РП АЛМАТЫ ", OpenVacancies: 60, Interns: 50, PlannedReserve: 40},
	}
	sections := []employeeExportSection{{Rows: []exportSummaryRow{
		{DebtsterDepartmentID: 12, OpenVacancies: 60, Interns: 50, PlannedReserve: 40},
	}}}
	snapshot := []debtsterVacancyReport{{
		ID:                     12,
		RP:                     "РП Алматы",
		VacantPositionsCount:   3,
		TraineesCount:          4,
		PlannedDismissalsCount: 5,
		PlannedDismissals: []debtsterPlannedDismissal{{
			FirstName: "Не должен попасть в XLSX",
		}},
	}}

	applyDebtsterExportSnapshot(rows, sections, snapshot)

	for _, item := range []exportSummaryRow{rows[0], rows[2], sections[0].Rows[0]} {
		if item.OpenVacancies != 3 || item.Interns != 4 || item.PlannedReserve != 5 {
			t.Fatalf("Debtster values were not applied: %#v", item)
		}
	}
	if rows[1].OpenVacancies != 0 || rows[1].Interns != 0 || rows[1].PlannedReserve != 0 {
		t.Fatalf("stale stored values remained without a matching Debtster snapshot: %#v", rows[1])
	}
}

func TestCleanPeople(t *testing.T) {
	got := cleanPeople(map[string][]string{
		"invited_candidates": {"  Иванов Иван  ", " ", "Петров Пётр"},
		"unknown":            {"Не должен сохраниться"},
	})
	want := map[string][]string{"invited_candidates": {"Иванов Иван", "Петров Пётр"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanPeople()=%v, want %v", got, want)
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
	past := today.AddDate(0, 0, -1)
	for past.Weekday() == time.Saturday || past.Weekday() == time.Sunday {
		past = past.AddDate(0, 0, -1)
	}
	if got := validatePastReportDate(past.Format("2006-01-02")); got != "" {
		t.Fatalf("past date rejected: %s", got)
	}
	for _, value := range []string{localToday(), today.AddDate(0, 0, 1).Format("2006-01-02")} {
		if got := validatePastReportDate(value); got == "" {
			t.Fatalf("non-past date accepted: %s", value)
		}
	}
}

func TestWeekendDate(t *testing.T) {
	for _, value := range []string{"2026-08-08", "2026-08-09"} {
		if !weekendDate(value) {
			t.Fatalf("weekend accepted: %s", value)
		}
	}
	if weekendDate("2026-08-10") {
		t.Fatal("weekday rejected")
	}
}

func TestUniqueUserIDs(t *testing.T) {
	got := uniqueUserIDs([]string{" first ", "second", "first", "", "second"})
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("unexpected unique ids: %#v", got)
	}
}

func TestValidReportUnitType(t *testing.T) {
	for _, value := range []string{"rp", "main_office"} {
		if !validReportUnitType(value) {
			t.Fatalf("valid report unit type rejected: %s", value)
		}
	}
	for _, value := range []string{"", "main-office", "office"} {
		if validReportUnitType(value) {
			t.Fatalf("invalid report unit type accepted: %s", value)
		}
	}
}

func TestApplyAssignedFlagsKeepsAllRows(t *testing.T) {
	rows := []reportRow{{OfficeID: "12"}, {OfficeID: "18"}, {OfficeID: "24"}}
	applyAssignedFlags(rows, map[string]bool{"12": true, "24": true})
	if !rows[0].Assigned || rows[1].Assigned || !rows[2].Assigned {
		t.Fatalf("unexpected assigned flags: %#v", rows)
	}
	if len(rows) != 3 {
		t.Fatalf("rows were filtered: got %d, want 3", len(rows))
	}
}
