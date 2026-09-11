package app

import (
	"context"
	"net/http"
	"strings"
)

type reportResponsibleUser struct {
	ID         string `json:"id"`
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	MiddleName string `json:"middleName"`
	Assigned   bool   `json:"assigned"`
}

type reportResponsiblesInput struct {
	Date       string   `json:"date"`
	EndDate    string   `json:"endDate"`
	Scope      string   `json:"scope"`
	ReportType string   `json:"reportType"`
	UnitID     string   `json:"unitId"`
	UserIDs    []string `json:"userIds"`
}

func responsiblePeriod(input reportResponsiblesInput) (string, *string, bool) {
	if !validDate(input.Date) {
		return "", nil, false
	}
	switch input.Scope {
	case "", "today":
		end := input.Date
		return input.Date, &end, true
	case "period":
		if !validDate(input.EndDate) || input.EndDate < input.Date {
			return "", nil, false
		}
		end := input.EndDate
		return input.Date, &end, true
	case "forever":
		return input.Date, nil, true
	default:
		return "", nil, false
	}
}

func validReportUnitType(value string) bool {
	return value == "rp" || value == "main_office"
}

func (a *App) reportResponsibles(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	reportType := strings.TrimSpace(r.URL.Query().Get("reportType"))
	unitID := strings.TrimSpace(r.URL.Query().Get("unitId"))
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if !validDate(date) || !validReportUnitType(reportType) || unitID == "" {
		problem(w, 422, "Не указан вид отчёта или подразделение")
		return
	}
	q, err := a.db.Query(r.Context(), `SELECT u.id,e.first_name,e.last_name,e.middle_name,EXISTS(
		SELECT 1 FROM report_unit_responsibles r
		WHERE r.user_id=u.id AND r.report_type=$2 AND r.unit_id=$3
		  AND r.assigned_from <= $1 AND (r.assigned_to IS NULL OR r.assigned_to >= $1)
	)
		FROM users u JOIN employees e ON e.id=u.employee_id
		WHERE u.role='employee' AND u.active AND NOT u.system
		ORDER BY e.last_name,e.first_name,e.middle_name`, date, reportType, unitID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []reportResponsibleUser{}
	for q.Next() {
		var item reportResponsibleUser
		if err = q.Scan(&item.ID, &item.FirstName, &item.LastName, &item.MiddleName, &item.Assigned); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, item)
	}
	if err = q.Err(); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, 200, out)
}

func (a *App) updateReportResponsibles(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireManager(w, r)
	if !ok {
		return
	}
	var input reportResponsiblesInput
	if !decode(w, r, &input) {
		return
	}
	input.ReportType = strings.TrimSpace(input.ReportType)
	input.UnitID = strings.TrimSpace(input.UnitID)
	input.Date = strings.TrimSpace(input.Date)
	input.EndDate = strings.TrimSpace(input.EndDate)
	input.Scope = strings.TrimSpace(input.Scope)
	input.UserIDs = uniqueUserIDs(input.UserIDs)
	periodFrom, periodTo, validPeriod := responsiblePeriod(input)
	if !validPeriod || !validReportUnitType(input.ReportType) || input.UnitID == "" {
		problem(w, 422, "Не указан вид отчёта или подразделение")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var unitExists bool
	if input.ReportType == "main_office" {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM main_offices WHERE id::text=$1)`, input.UnitID).Scan(&unitExists)
	} else {
		err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM offices WHERE COALESCE(debtster_department_id::text,id::text)=$1
			UNION ALL SELECT 1 FROM report_rows WHERE debtster_department_id::text=$1
		)`, input.UnitID).Scan(&unitExists)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !unitExists {
		problem(w, 404, "Подразделение не найдено")
		return
	}
	// Replace only the requested window. Parts of older assignments outside it
	// are recreated so changing a week does not erase assignments before or after it.
	if _, err = tx.Exec(ctx, `WITH affected AS (
		DELETE FROM report_unit_responsibles
		WHERE report_type=$1 AND unit_id=$2
		  AND assigned_from <= COALESCE($4::date, 'infinity'::date)
		  AND (assigned_to IS NULL OR assigned_to >= $3::date)
		RETURNING user_id,assigned_by_user_id,created_at,assigned_from,assigned_to
	), preserved AS (
		SELECT user_id,assigned_by_user_id,created_at,assigned_from,$3::date - 1 AS assigned_to
		FROM affected WHERE assigned_from < $3::date
		UNION ALL
		SELECT user_id,assigned_by_user_id,created_at,$4::date + 1 AS assigned_from,assigned_to
		FROM affected WHERE $4::date IS NOT NULL AND (assigned_to IS NULL OR assigned_to > $4::date)
	)
	INSERT INTO report_unit_responsibles(report_type,unit_id,user_id,assigned_by_user_id,created_at,assigned_from,assigned_to)
	SELECT $1,$2,user_id,assigned_by_user_id,created_at,assigned_from,assigned_to FROM preserved`, input.ReportType, input.UnitID, periodFrom, periodTo); err != nil {
		serverError(w, err)
		return
	}
	for _, userID := range input.UserIDs {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id::text=$1 AND role='employee' AND active AND NOT system)`, userID).Scan(&valid); err != nil || !valid {
			problem(w, 422, "Один из выбранных сотрудников недоступен")
			return
		}
		if _, err = tx.Exec(ctx, `INSERT INTO report_unit_responsibles(assigned_from,assigned_to,report_type,unit_id,user_id,assigned_by_user_id) VALUES($1,$2,$3,$4,$5,$6)`, periodFrom, periodTo, input.ReportType, input.UnitID, userID, claims.UserID); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.log(ctx, "report_responsibles.updated", input.ReportType, input.UnitID+":"+periodFrom)
	jsonOut(w, 200, map[string]any{"ok": true, "count": len(input.UserIDs)})
}

func (a *App) applyResponsibleCounts(ctx context.Context, date, reportType string, rows []reportRow) error {
	q, err := a.db.Query(ctx, `SELECT unit_id,count(DISTINCT r.user_id)::int FROM report_unit_responsibles r
		JOIN users u ON u.id=r.user_id AND u.role='employee' AND u.active AND NOT u.system
		WHERE assigned_from <= $1 AND (assigned_to IS NULL OR assigned_to >= $1) AND report_type=$2 GROUP BY unit_id`, date, reportType)
	if err != nil {
		return err
	}
	defer q.Close()
	counts := map[string]int{}
	for q.Next() {
		var id string
		var count int
		if err = q.Scan(&id, &count); err != nil {
			return err
		}
		counts[id] = count
	}
	for index := range rows {
		rows[index].ResponsibleCount = counts[rows[index].OfficeID]
	}
	return q.Err()
}

func (a *App) filterAssignedRows(ctx context.Context, date, reportType, userID string, rows []reportRow) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `SELECT DISTINCT unit_id FROM report_unit_responsibles WHERE assigned_from <= $1 AND (assigned_to IS NULL OR assigned_to >= $1) AND report_type=$2 AND user_id=$3`, date, reportType, userID)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	allowed := map[string]bool{}
	for q.Next() {
		var id string
		if err = q.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = true
	}
	filtered := make([]reportRow, 0, len(rows))
	for _, row := range rows {
		if allowed[row.OfficeID] {
			filtered = append(filtered, row)
		}
	}
	return filtered, q.Err()
}

func (a *App) markAssignedRows(ctx context.Context, date, reportType, userID string, rows []reportRow) error {
	q, err := a.db.Query(ctx, `SELECT DISTINCT unit_id FROM report_unit_responsibles WHERE assigned_from <= $1 AND (assigned_to IS NULL OR assigned_to >= $1) AND report_type=$2 AND user_id=$3`, date, reportType, userID)
	if err != nil {
		return err
	}
	defer q.Close()
	assigned := map[string]bool{}
	for q.Next() {
		var id string
		if err = q.Scan(&id); err != nil {
			return err
		}
		assigned[id] = true
	}
	if err = q.Err(); err != nil {
		return err
	}
	applyAssignedFlags(rows, assigned)
	return nil
}

func applyAssignedFlags(rows []reportRow, assigned map[string]bool) {
	for index := range rows {
		rows[index].Assigned = assigned[rows[index].OfficeID]
	}
}
