package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateAccessibleReportsAgainstPostgres(t *testing.T) {
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
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var userID, officeID string
	if err = tx.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('access-test-'||gen_random_uuid()::text,'test','employee') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO main_offices(name) VALUES('access-test-'||gen_random_uuid()::text) RETURNING id`).Scan(&officeID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO report_unit_responsibles(report_type,unit_id,user_id,assigned_from) VALUES('main_office',$1,$2,'2026-09-01')`, officeID, userID); err != nil {
		t.Fatal(err)
	}
	const date = "2026-09-09"
	vacancies := []debtsterVacancyReport{{ID: 990001, RP: "Original name"}}
	if err = createAccessibleReports(ctx, tx, date, userID, vacancies); err != nil {
		t.Fatal(err)
	}
	var rowID, reportID string
	if err = tx.QueryRow(ctx, `SELECT rr.id,r.id FROM report_rows rr JOIN reports r ON r.id=rr.report_id WHERE r.report_date=$1 AND r.owner_user_id=$2`, date, userID).Scan(&rowID, &reportID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM main_office_report_rows rr JOIN main_office_reports r ON r.id=rr.report_id WHERE r.report_date=$1 AND r.owner_user_id=$2`, date, userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("GO rows=%d: %v", count, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE report_rows SET invited_candidates=7 WHERE id=$1`, rowID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE reports SET status='completed',completed_at=now() WHERE id=$1`, reportID); err != nil {
		t.Fatal(err)
	}
	vacancies[0].RP = "Renamed upstream"
	vacancies = append(vacancies, debtsterVacancyReport{ID: 990002, RP: "New unit"})
	if err = createAccessibleReports(ctx, tx, date, userID, vacancies); err != nil {
		t.Fatal(err)
	}
	var name, status string
	if err = tx.QueryRow(ctx, `SELECT rr.invited_candidates,rr.office_name_snapshot,r.status FROM report_rows rr JOIN reports r ON r.id=rr.report_id WHERE rr.id=$1`, rowID).Scan(&count, &name, &status); err != nil || count != 7 || name != "Original name" || status != "draft" {
		t.Fatalf("existing row changed: %d %q %q %v", count, name, status, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM report_rows WHERE report_id=$1`, reportID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rows=%d: %v", count, err)
	}
	// The same path repairs an existing report that has no rows.
	if _, err = tx.Exec(ctx, `DELETE FROM report_rows WHERE report_id=$1`, reportID); err != nil {
		t.Fatal(err)
	}
	if err = createAccessibleReports(ctx, tx, date, userID, vacancies); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM report_rows WHERE report_id=$1`, reportID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("repaired rows=%d: %v", count, err)
	}
	// Optional notes and all operation types survive storage in PostgreSQL.
	edits := []correctionCandidateEdit{
		{Category: "invited_candidates", Kind: "removed", Before: &hiredWorker{FullName: "Removed"}},
		{Category: "invited_candidates", Kind: "changed", Before: &hiredWorker{FullName: "Before"}, After: &hiredWorker{FullName: "After"}},
		{Category: "invited_candidates", Kind: "added", After: &hiredWorker{FullName: "Added"}},
	}
	encoded, err := json.Marshal([]correctionRowChange{{RowID: rowID, Edits: edits}})
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err = tx.QueryRow(ctx, `INSERT INTO correction_requests(report_date,report_type,user_id,note,changes) VALUES($1,'rp',$2,'',$3::jsonb) RETURNING changes`, date, userID, encoded).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var changes []correctionRowChange
	if err = json.Unmarshal(stored, &changes); err != nil || len(changes) != 1 || len(changes[0].Edits) != 3 {
		t.Fatalf("lost stored operations: %s %v", stored, err)
	}

}
