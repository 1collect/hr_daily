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
	ReportType string   `json:"reportType"`
	UnitID     string   `json:"unitId"`
	UserIDs    []string `json:"userIds"`
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
	if !validReportUnitType(reportType) || unitID == "" {
		problem(w, 422, "Не указан вид отчёта или подразделение")
		return
	}
	q, err := a.db.Query(r.Context(), `SELECT u.id,e.first_name,e.last_name,e.middle_name,(r.user_id IS NOT NULL)
		FROM users u JOIN employees e ON e.id=u.employee_id
		LEFT JOIN report_unit_responsibles r ON r.user_id=u.id AND r.report_type=$1 AND r.unit_id=$2
		WHERE u.role='employee' AND u.active AND NOT u.system
		ORDER BY e.last_name,e.first_name,e.middle_name`, reportType, unitID)
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
	input.UserIDs = uniqueUserIDs(input.UserIDs)
	if !validReportUnitType(input.ReportType) || input.UnitID == "" {
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
	if _, err = tx.Exec(ctx, `DELETE FROM report_unit_responsibles WHERE report_type=$1 AND unit_id=$2`, input.ReportType, input.UnitID); err != nil {
		serverError(w, err)
		return
	}
	for _, userID := range input.UserIDs {
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id::text=$1 AND role='employee' AND active AND NOT system)`, userID).Scan(&valid); err != nil || !valid {
			problem(w, 422, "Один из выбранных сотрудников недоступен")
			return
		}
		if _, err = tx.Exec(ctx, `INSERT INTO report_unit_responsibles(report_type,unit_id,user_id,assigned_by_user_id) VALUES($1,$2,$3,$4)`, input.ReportType, input.UnitID, userID, claims.UserID); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.log(ctx, "report_responsibles.updated", input.ReportType, input.UnitID)
	jsonOut(w, 200, map[string]any{"ok": true, "count": len(input.UserIDs)})
}

func (a *App) applyResponsibleCounts(ctx context.Context, reportType string, rows []reportRow) error {
	q, err := a.db.Query(ctx, `SELECT unit_id,count(*)::int FROM report_unit_responsibles r
		JOIN users u ON u.id=r.user_id AND u.role='employee' AND u.active AND NOT u.system
		WHERE report_type=$1 GROUP BY unit_id`, reportType)
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

func (a *App) filterAssignedRows(ctx context.Context, reportType, userID string, rows []reportRow) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `SELECT unit_id FROM report_unit_responsibles WHERE report_type=$1 AND user_id=$2`, reportType, userID)
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
