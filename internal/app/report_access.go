package app

import (
	"context"
	"net/http"
	"strings"
	"time"
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
	if msg := validatePastReportDate(strings.TrimSpace(in.Date)); msg != "" {
		problem(w, 422, msg)
		return
	}
	if len(in.UserIDs) == 0 {
		problem(w, 422, "Выберите хотя бы одного сотрудника")
		return
	}
	ctx := r.Context()
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
		_, err = tx.Exec(ctx, `UPDATE reports SET status='draft',completed_at=NULL,updated_at=now() WHERE report_date=$1 AND owner_user_id=$2`, in.Date, userID)
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
