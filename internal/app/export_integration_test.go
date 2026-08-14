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
		if len(rows) != 0 || len(sections) != 0 {
			t.Fatalf("expected empty %s export, got %d rows and %d sections", kind.Kind, len(rows), len(sections))
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
