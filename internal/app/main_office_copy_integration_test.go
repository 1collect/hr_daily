package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hrreport/migrations"
)

func TestCopyPreviousMainOfficeReportAgainstPostgres(t *testing.T) {
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

	var userID, officeID, sourceReportID, targetReportID, sourceRowID, targetRowID string
	if err = db.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('copy-test-'||gen_random_uuid()::text,'test','employee') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO main_offices(name) VALUES('copy-test-'||gen_random_uuid()::text) RETURNING id`).Scan(&officeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM main_office_daily_shared WHERE main_office_id=$1`, officeID)
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
		_, _ = db.Exec(context.Background(), `DELETE FROM main_offices WHERE id=$1`, officeID)
	})
	if _, err = db.Exec(ctx, `INSERT INTO report_unit_responsibles(report_type,unit_id,user_id,assigned_from) VALUES('main_office',$1,$2,'2026-09-10')`, officeID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO report_access_grants(report_date,user_id,granted_by_user_id,expires_at) VALUES('2026-09-11',$1,$1,now()+interval '1 hour')`, userID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO main_office_reports(report_date,owner_user_id) VALUES('2026-09-10',$1) RETURNING id`, userID).Scan(&sourceReportID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO main_office_reports(report_date,owner_user_id) VALUES('2026-09-11',$1) RETURNING id`, userID).Scan(&targetReportID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO main_office_report_rows(report_id,main_office_id,invited_candidates,interviewed_candidates,interns,reserve_candidates,dismissed_workers) VALUES($1,$2,2,1,1,1,1) RETURNING id`, sourceReportID, officeID).Scan(&sourceRowID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `INSERT INTO main_office_report_rows(report_id,main_office_id,invited_candidates) VALUES($1,$2,9) RETURNING id`, targetReportID, officeID).Scan(&targetRowID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO main_office_hired_workers(report_row_id,full_name,position) VALUES($1,'Иванов Иван','Специалист')`, sourceRowID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO main_office_report_row_people(report_row_id,category,full_name) VALUES($1,'invited_candidates','Петров Пётр')`, sourceRowID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO main_office_daily_shared(report_date,main_office_id,open_vacancies,planned_reserve) VALUES('2026-09-10',$1,4,3)`, officeID); err != nil {
		t.Fatal(err)
	}

	app := &App{db: db, reports: &reportHub{}}
	previousHasData, err := app.mainOfficePreviousFirstColumnHasData(ctx, targetReportID, userID, "2026-09-11")
	if err != nil {
		t.Fatal(err)
	}
	if !previousHasData {
		t.Fatal("expected previous first column to have data")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/main-office/report/copy-previous", bytes.NewBufferString(`{"date":"2026-09-11"}`))
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, sessionClaims{UserID: userID, Username: "copy-test", Role: "employee"}))
	recorder := httptest.NewRecorder()
	app.copyPreviousMainOfficeReport(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var invited, hired, people, vacancies, reserve int
	if err = db.QueryRow(ctx, `SELECT invited_candidates FROM main_office_report_rows WHERE id=$1`, targetRowID).Scan(&invited); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM main_office_hired_workers WHERE report_row_id=$1`, targetRowID).Scan(&hired); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM main_office_report_row_people WHERE report_row_id=$1`, targetRowID).Scan(&people); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT open_vacancies,planned_reserve FROM main_office_daily_shared WHERE report_date='2026-09-11' AND main_office_id=$1`, officeID).Scan(&vacancies, &reserve); err != nil {
		t.Fatal(err)
	}
	if invited != 9 || hired != 0 || people != 0 || vacancies != 4 || reserve != 0 {
		t.Fatalf("copied values: invited=%d hired=%d people=%d vacancies=%d reserve=%d", invited, hired, people, vacancies, reserve)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/main-office/report/copy-previous", bytes.NewBufferString(`{"date":"2026-09-11"}`))
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, sessionClaims{UserID: userID, Username: "copy-test", Role: "employee"}))
	recorder = httptest.NewRecorder()
	app.copyPreviousMainOfficeReport(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("second copy status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if _, err = db.Exec(ctx, `UPDATE main_office_daily_shared SET open_vacancies=0 WHERE report_date='2026-09-10' AND main_office_id=$1`, officeID); err != nil {
		t.Fatal(err)
	}
	previousHasData, err = app.mainOfficePreviousFirstColumnHasData(ctx, targetReportID, userID, "2026-09-11")
	if err != nil {
		t.Fatal(err)
	}
	if previousHasData {
		t.Fatal("expected zero previous first column to be treated as empty")
	}
}
