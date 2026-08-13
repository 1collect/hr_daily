package app

import (
	"net/http"
	"strings"
)

type dailyPlanRecord struct {
	UserID     string `json:"userId"`
	Name       string `json:"name"`
	Plan       int    `json:"plan"`
	Overridden bool   `json:"overridden"`
}

type dailyPlanInput struct {
	Date       string `json:"date"`
	ReportType string `json:"reportType"`
	Plans      []struct {
		UserID string `json:"userId"`
		Plan   int    `json:"plan"`
	} `json:"plans"`
}

func validDailyPlanType(value string) bool {
	return value == "rp" || value == "main_office"
}

func (a *App) dailyPlans(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	reportType := strings.TrimSpace(r.URL.Query().Get("reportType"))
	if !validDate(date) || weekendDate(date) || !validDailyPlanType(reportType) {
		problem(w, 422, "Укажите корректную дату и тип отчёта")
		return
	}
	baseTable := "employee_efficiency_plans"
	if reportType == "main_office" {
		baseTable = "main_office_employee_efficiency_plans"
	}
	query := `SELECT u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username),
		COALESCE(d.plan_count,(SELECT p.plan_count FROM ` + baseTable + ` p WHERE p.user_id=u.id AND p.effective_from<=$1::date ORDER BY p.effective_from DESC LIMIT 1),0),
		(d.user_id IS NOT NULL)
		FROM users u JOIN employees e ON e.id=u.employee_id
		LEFT JOIN daily_efficiency_plan_overrides d ON d.user_id=u.id AND d.report_date=$1 AND d.report_type=$2
		WHERE u.role='employee' AND u.active AND NOT u.system
		ORDER BY e.last_name,e.first_name,e.middle_name`
	rows, err := a.db.Query(r.Context(), query, date, reportType)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []dailyPlanRecord{}
	for rows.Next() {
		var item dailyPlanRecord
		if err = rows.Scan(&item.UserID, &item.Name, &item.Plan, &item.Overridden); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, 200, out)
}

func (a *App) updateDailyPlans(w http.ResponseWriter, r *http.Request) {
	claims, ok := requireManager(w, r)
	if !ok {
		return
	}
	var input dailyPlanInput
	if !decode(w, r, &input) {
		return
	}
	input.Date = strings.TrimSpace(input.Date)
	input.ReportType = strings.TrimSpace(input.ReportType)
	if !validDate(input.Date) || weekendDate(input.Date) || !validDailyPlanType(input.ReportType) || len(input.Plans) == 0 {
		problem(w, 422, "Укажите корректную дату, тип отчёта и планы")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	userIDs := make([]string, 0, len(input.Plans))
	seen := map[string]bool{}
	for _, plan := range input.Plans {
		plan.UserID = strings.TrimSpace(plan.UserID)
		if plan.UserID == "" || plan.Plan < 0 || seen[plan.UserID] {
			problem(w, 422, "Планы должны быть целыми неотрицательными числами")
			return
		}
		seen[plan.UserID] = true
		var valid bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id::text=$1 AND role='employee' AND active AND NOT system)`, plan.UserID).Scan(&valid); err != nil || !valid {
			problem(w, 422, "Один из сотрудников недоступен")
			return
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO daily_efficiency_plan_overrides(user_id,report_date,report_type,plan_count)
			VALUES($1,$2,$3,$4) ON CONFLICT(user_id,report_date,report_type)
			DO UPDATE SET plan_count=EXCLUDED.plan_count,updated_at=now()`, plan.UserID, input.Date, input.ReportType, plan.Plan)
		if err != nil {
			serverError(w, err)
			return
		}
		if input.ReportType == "rp" {
			_, err = tx.Exec(r.Context(), `UPDATE report_rows rr SET efficiency=CASE WHEN $3>0 THEN round(rr.interviewed_candidates*100.0/$3,2) ELSE 0 END,updated_at=now()
				FROM reports rp WHERE rp.id=rr.report_id AND rp.owner_user_id=$1 AND rp.report_date=$2`, plan.UserID, input.Date, plan.Plan)
			if err != nil {
				serverError(w, err)
				return
			}
		}
		userIDs = append(userIDs, plan.UserID)
		_, _ = tx.Exec(r.Context(), `INSERT INTO audit_log(actor,action,entity_type,entity_id,details)
			VALUES($1,'report.daily_plan.updated','user',$2,jsonb_build_object('date',$3::text,'reportType',$4::text,'plan',$5::int))`, claims.Username, plan.UserID, input.Date, input.ReportType, plan.Plan)
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.reports.send(input.Date, map[string]any{"type": "report_plan_changed", "date": input.Date, "reportType": input.ReportType, "userIds": userIDs})
	jsonOut(w, 200, map[string]bool{"ok": true})
}
