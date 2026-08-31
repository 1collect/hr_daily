package app

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"
	"hrreport/migrations"
)

func TestExportQueriesAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db}
	file := excelize.NewFile()
	styles := newExportStyles(file)
	for index, kind := range []exportKindConfig{exportKinds["rp"], exportKinds["main_office"]} {
		rows, sections, loadErr := a.loadExportData(ctx, kind, "2026-08-01", "2026-08-31")
		if loadErr != nil {
			t.Fatalf("load %s export: %v", kind.Kind, loadErr)
		}
		if index == 0 {
			file.SetSheetName("Sheet1", kind.Sheet)
		} else {
			_, _ = file.NewSheet(kind.Sheet)
		}
		writeExportSummarySheet(file, kind, rows, sections, styles)
		if detailErr := a.writeExportDetailSheets(ctx, file, kind, "2026-08-01", "2026-08-31", styles); detailErr != nil {
			t.Fatalf("load %s export details: %v", kind.Kind, detailErr)
		}
		hiredSheet := "Принятые " + kind.Sheet
		if header, _ := file.GetCellValue(hiredSheet, "B1"); header != "Должность" {
			t.Fatalf("%s B1 is %q, want Должность", hiredSheet, header)
		}
		if header, _ := file.GetCellValue(hiredSheet, "C1"); header != "Сотрудник которого приняли" {
			t.Fatalf("%s C1 is %q, want employee header", hiredSheet, header)
		}
		if header, _ := file.GetCellValue(kind.Sheet, "F1"); header != "Планируемый резерв" {
			t.Fatalf("%s F1 is %q, want Планируемый резерв", kind.Sheet, header)
		}
		if header, _ := file.GetCellValue(kind.Sheet, "H1"); header != "Количество принятых работников" {
			t.Fatalf("%s H1 is %q, want hired workers", kind.Sheet, header)
		}
	}
	wantSheets := []string{"РП", "Принятые РП", "Списки ФИО РП", "ГО", "Принятые ГО", "Списки ФИО ГО"}
	gotSheets := file.GetSheetList()
	if len(gotSheets) != len(wantSheets) {
		t.Fatalf("unexpected sheets: %v", gotSheets)
	}
	for index := range wantSheets {
		if gotSheets[index] != wantSheets[index] {
			t.Fatalf("sheet %d is %q, want %q", index, gotSheets[index], wantSheets[index])
		}
	}
}

func TestDebtsterDepartmentsAreStoredDirectlyInReports(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	var officesBefore int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM offices`).Scan(&officesBefore); err != nil {
		t.Fatal(err)
	}
	date := "2099-01-05"
	for _, username := range []string{"debtster-direct-one", "debtster-direct-two"} {
		if _, err = db.Exec(ctx, `WITH new_user AS (
			INSERT INTO users(username,password_hash,role) VALUES($1,'test','employee') RETURNING id)
			INSERT INTO reports(report_date,owner_user_id) SELECT $2,id FROM new_user`, username, date); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_, _ = db.Exec(ctx, `DELETE FROM reports WHERE report_date=$1`, date)
		_, _ = db.Exec(ctx, `DELETE FROM users WHERE username IN ('debtster-direct-one','debtster-direct-two')`)
	}()
	a := &App{db: db}
	departments, err := a.syncDebtsterReportRows(ctx, date, []debtsterDepartment{
		{ID: 12, Name: "rp_almaty", DisplayName: "РП Алматы"},
		{ID: 18, Name: "rp_astana", DisplayName: "РП Астана"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(departments) != 2 {
		t.Fatalf("got %d departments, want 2", len(departments))
	}
	departments, err = a.syncDebtsterReportRows(ctx, date, []debtsterDepartment{
		{ID: 12, Name: "rp_almaty", DisplayName: "РП Алматы новое имя"},
		{ID: 25, Name: "rp_shymkent", DisplayName: "РП Шымкент"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(departments) != 3 || departments[2].ID != 25 {
		t.Fatalf("departments were not retained/appended: %#v", departments)
	}
	var rows, legacyLinks, officesAfter int
	if err = db.QueryRow(ctx, `SELECT count(*),count(office_id) FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id WHERE rp.report_date=$1`, date).Scan(&rows, &legacyLinks); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM offices`).Scan(&officesAfter); err != nil {
		t.Fatal(err)
	}
	if rows != 6 || legacyLinks != 0 || officesAfter != officesBefore {
		t.Fatalf("rows=%d legacy links=%d offices before=%d after=%d", rows, legacyLinks, officesBefore, officesAfter)
	}
	var reportID string
	if err = db.QueryRow(ctx, `SELECT id FROM reports WHERE report_date=$1 ORDER BY id LIMIT 1`, date).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	personalRows, err := a.loadRows(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	aggregateRows, err := a.loadAggregateRows(ctx, date, "", candidatePlans{})
	if err != nil {
		t.Fatal(err)
	}
	if len(personalRows) != 3 || len(aggregateRows) != 3 || personalRows[0].OfficeID != "12" {
		t.Fatalf("personal=%#v aggregate=%#v", personalRows, aggregateRows)
	}
}
