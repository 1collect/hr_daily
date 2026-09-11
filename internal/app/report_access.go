package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type reportAccessUser struct {
	ID          string     `json:"id"`
	FirstName   string     `json:"firstName"`
	LastName    string     `json:"lastName"`
	MiddleName  string     `json:"middleName"`
	AccessUntil *time.Time `json:"accessUntil,omitempty"`
}

type reportAccessInput struct {
	Date    string   `json:"date"`
	UserIDs []string `json:"userIds"`
}

func uniqueUserIDs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func validatePastReportDate(date string) string {
	if !validDate(date) {
		return "Дата должна иметь формат YYYY-MM-DD"
	}
	if weekendDate(date) {
		return "Доступ к отчётам за субботу и воскресенье открыть нельзя"
	}
	if date >= localToday() {
		return "Доступ можно открыть только к прошедшим дням"
	}
	return ""
}

func (a *App) reportAccessUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if msg := validatePastReportDate(date); msg != "" {
		problem(w, 422, msg)
		return
	}
	q, err := a.db.Query(r.Context(), `SELECT u.id,e.first_name,e.last_name,e.middle_name,
		CASE WHEN g.expires_at>now() THEN g.expires_at END
		FROM users u JOIN employees e ON e.id=u.employee_id
		LEFT JOIN report_access_grants g ON g.user_id=u.id AND g.report_date=$1
		WHERE u.role='employee' AND u.active AND NOT u.system
		ORDER BY e.last_name,e.first_name,e.middle_name`, date)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []reportAccessUser{}
	for q.Next() {
		var x reportAccessUser
		if err = q.Scan(&x.ID, &x.FirstName, &x.LastName, &x.MiddleName, &x.AccessUntil); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, x)
	}
	jsonOut(w, 200, out)
}

func (a *App) openReportAccess(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireManager(w, r)
	if !ok {
		return
	}
	var in reportAccessInput
	if !decode(w, r, &in) {
		return
	}
	in.UserIDs = uniqueUserIDs(in.UserIDs)
	in.Date = strings.TrimSpace(in.Date)
	if msg := validatePastReportDate(strings.TrimSpace(in.Date)); msg != "" {
		problem(w, 422, msg)
		return
	}
	if len(in.UserIDs) == 0 {
		problem(w, 422, "Выберите хотя бы одного сотрудника")
		return
	}
	ctx := r.Context()
	var vacancies []debtsterVacancyReport
	if usesDebtsterDepartments(in.Date) {
		var fetchErr error
		vacancies, fetchErr = fetchDebtsterVacancies(ctx, a.httpClient, a.debtsterAPI, in.Date)
		if fetchErr != nil {
			serverError(w, fetchErr)
			return
		}
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	for _, userID := range in.UserIDs {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id::text=$1 AND role='employee' AND active AND NOT system)`, userID).Scan(&valid); err != nil || !valid {
			problem(w, 422, "Один из выбранных сотрудников недоступен")
			return
		}
		_, err = tx.Exec(ctx, `INSERT INTO report_access_grants(report_date,user_id,granted_by_user_id,expires_at)
			VALUES($1,$2,$3,now()+interval '24 hours')
			ON CONFLICT(report_date,user_id) DO UPDATE SET granted_by_user_id=EXCLUDED.granted_by_user_id,expires_at=EXCLUDED.expires_at,updated_at=now()`, in.Date, userID, claims.UserID)
		if err != nil {
			serverError(w, err)
			return
		}
		err = createAccessibleReports(ctx, tx, in.Date, userID, vacancies)
		if err != nil {
			serverError(w, err)
			return
		}
		_, _ = tx.Exec(ctx, `INSERT INTO audit_log(actor,action,entity_type,entity_id,details) VALUES($1,'report_access.opened','user',$2,jsonb_build_object('date',$3::text,'hours',24))`, claims.Username, userID, in.Date)
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.reports.send(in.Date, map[string]any{"type": "report_access_changed", "date": in.Date, "userIds": in.UserIDs})
	jsonOut(w, 200, map[string]any{"ok": true, "expiresInHours": 24})
}

// Create missing reports and real row IDs before granting access. Existing data
// and historical names are retained when access is opened again.
func createAccessibleReports(ctx context.Context, tx pgx.Tx, date, userID string, vacancies []debtsterVacancyReport) error {
	var reportID string
	err := tx.QueryRow(ctx, `INSERT INTO reports(report_date,owner_user_id,owner_name_snapshot)
		SELECT $1,u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username)
		FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$2
		ON CONFLICT(report_date,owner_user_id) WHERE owner_user_id IS NOT NULL
		DO UPDATE SET status='draft',completed_at=NULL,updated_at=now() RETURNING id`, date, userID).Scan(&reportID)
	if err != nil {
		return err
	}
	if usesDebtsterDepartments(date) {
		if err = insertMissingDebtsterRows(ctx, tx, reportID, vacancies); err != nil {
			return err
		}
	} else {
		if _, err = tx.Exec(ctx, `INSERT INTO report_rows(report_id,office_id,office_name_snapshot,office_sort_order_snapshot,debtster_department_id,debtster_department_name)
			SELECT $1,id,name,sort_order,debtster_department_id,debtster_department_name FROM offices WHERE active ON CONFLICT DO NOTHING`, reportID); err != nil {
			return err
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO main_office_reports(report_date,owner_user_id,owner_name_snapshot)
		SELECT $1,u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username)
		FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$2
		ON CONFLICT(report_date,owner_user_id) DO UPDATE SET report_date=EXCLUDED.report_date RETURNING id`, date, userID).Scan(&reportID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO main_office_report_rows(report_id,main_office_id,main_office_name_snapshot,main_office_sort_order_snapshot)
		SELECT $1,o.id,o.name,o.sort_order FROM main_offices o
		WHERE o.active AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$2 AND a.report_type='main_office'
		AND a.unit_id=o.id::text AND a.assigned_from<=$3 AND (a.assigned_to IS NULL OR a.assigned_to>=$3)) ON CONFLICT DO NOTHING`, reportID, userID, date)
	return err
}

func insertMissingDebtsterRows(ctx context.Context, tx pgx.Tx, reportID string, vacancies []debtsterVacancyReport) error {
	for index, department := range vacancies {
		if _, err := tx.Exec(ctx, `INSERT INTO report_rows(report_id,debtster_department_id,debtster_department_name,office_name_snapshot,office_sort_order_snapshot)
			VALUES($1,$2,$3,$3,$4) ON CONFLICT DO NOTHING`, reportID, department.ID, department.RP, index+1); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) closeReportAccess(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireManager(w, r)
	if !ok {
		return
	}
	var in reportAccessInput
	if !decode(w, r, &in) {
		return
	}
	in.UserIDs = uniqueUserIDs(in.UserIDs)
	if !validDate(in.Date) || len(in.UserIDs) == 0 {
		problem(w, 422, "Укажите дату и сотрудников")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var closed int64
	for _, userID := range in.UserIDs {
		tag, deleteErr := tx.Exec(ctx, `DELETE FROM report_access_grants WHERE report_date=$1 AND user_id::text=$2`, in.Date, userID)
		if deleteErr != nil {
			serverError(w, deleteErr)
			return
		}
		closed += tag.RowsAffected()
		if tag.RowsAffected() > 0 {
			_, _ = tx.Exec(ctx, `INSERT INTO audit_log(actor,action,entity_type,entity_id,details) VALUES($1,'report_access.closed','user',$2,jsonb_build_object('date',$3::text))`, claims.Username, userID, in.Date)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.reports.send(in.Date, map[string]any{"type": "report_access_changed", "date": in.Date, "userIds": in.UserIDs})
	jsonOut(w, 200, map[string]any{"ok": true, "closed": closed})
}

func (a *App) hasReportEditAccess(ctx context.Context, userID, date string) bool {
	if date == localToday() {
		return true
	}
	var allowed bool
	_ = a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM report_access_grants WHERE report_date=$1 AND user_id=$2 AND expires_at>now())`, date, userID).Scan(&allowed)
	return allowed
}
