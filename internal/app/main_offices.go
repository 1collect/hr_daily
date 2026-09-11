package app

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (a *App) mainOffices(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	q, err := a.db.Query(r.Context(), `SELECT id,name,sort_order,active FROM main_offices ORDER BY sort_order,name`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []office{}
	for q.Next() {
		var item office
		if err = q.Scan(&item.ID, &item.Name, &item.SortOrder, &item.Active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, item)
	}
	jsonOut(w, 200, out)
}

func (a *App) createMainOffice(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	var item office
	if !decode(w, r, &item) {
		return
	}
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		problem(w, 422, "Название компании обязательно")
		return
	}
	err := a.db.QueryRow(r.Context(), `INSERT INTO main_offices(name,sort_order) VALUES($1,COALESCE((SELECT max(sort_order)+1 FROM main_offices),1)) RETURNING id,sort_order`, item.Name).Scan(&item.ID, &item.SortOrder)
	if err != nil {
		problem(w, 409, "Такая компания уже существует")
		return
	}
	item.Active = true
	a.log(r.Context(), "main_office.created", "main_office", item.ID)
	jsonOut(w, 201, item)
}

func (a *App) updateMainOffice(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	var item office
	if !decode(w, r, &item) {
		return
	}
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		problem(w, 422, "Название компании обязательно")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE main_offices SET name=$2,active=$3,updated_at=now() WHERE id=$1`, r.PathValue("id"), item.Name, item.Active)
	if err != nil || tag.RowsAffected() == 0 {
		problem(w, 422, "Не удалось обновить компанию")
		return
	}
	a.log(r.Context(), "main_office.updated", "main_office", r.PathValue("id"))
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (a *App) reorderMainOffices(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	var input struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &input) {
		return
	}
	var total int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM main_offices`).Scan(&total); err != nil {
		serverError(w, err)
		return
	}
	seen := make(map[string]struct{}, len(input.IDs))
	for _, id := range input.IDs {
		seen[id] = struct{}{}
	}
	if len(input.IDs) != total || len(seen) != total {
		problem(w, 422, "Передан неполный порядок компаний")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	for index, id := range input.IDs {
		tag, updateErr := tx.Exec(r.Context(), `UPDATE main_offices SET sort_order=$2,updated_at=now() WHERE id=$1`, id, index+1)
		if updateErr != nil || tag.RowsAffected() != 1 {
			problem(w, 422, "Не удалось сохранить порядок компаний")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.log(r.Context(), "main_office.reordered", "main_office", strings.Join(input.IDs, ","))
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (a *App) mainOfficeBootstrap(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if !validDate(date) {
		problem(w, 400, "Дата должна иметь формат YYYY-MM-DD")
		return
	}
	if weekendDate(date) {
		problem(w, 422, "Отчёты за субботу и воскресенье недоступны")
		return
	}
	ctx := r.Context()
	claims := claimsFrom(ctx)
	if isManager(claims) {
		employeeID := strings.TrimSpace(r.URL.Query().Get("employeeId"))
		if employeeID != "" {
			var active bool
			_ = a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='employee' AND active AND NOT system)`, employeeID).Scan(&active)
			if !active {
				problem(w, 404, "Сотрудник не найден")
				return
			}
		}
		plans, err := a.candidatePlanTotals(ctx, date, employeeID, "main_office")
		if err != nil {
			serverError(w, err)
			return
		}
		rows, err := a.loadMainOfficeAggregateRows(ctx, date, employeeID, plans)
		if err != nil {
			serverError(w, err)
			return
		}
		if err = a.applyResponsibleCounts(ctx, date, "main_office", rows); err != nil {
			serverError(w, err)
			return
		}
		var accessCount int
		_ = a.db.QueryRow(ctx, `SELECT count(*) FROM report_access_grants WHERE report_date=$1 AND expires_at>now()`, date).Scan(&accessCount)
		applyCandidatePlans(rows, plans)
		jsonOut(w, 200, map[string]any{"report": map[string]any{"id": "", "date": date, "status": "read_only", "editable": false, "accessCount": accessCount}, "rows": rows, "plan": plans.Hired, "invitationPlan": plans.Invited, "hiringPlan": plans.Hired, "totals": totals(rows, plans)})
		return
	}
	var reportID string
	err := a.db.QueryRow(ctx, `INSERT INTO main_office_reports(report_date,owner_user_id,owner_name_snapshot)
		SELECT $1,u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username)
		FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$2
		ON CONFLICT(report_date,owner_user_id) DO UPDATE SET report_date=EXCLUDED.report_date RETURNING id`, date, claims.UserID).Scan(&reportID)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = a.db.Exec(ctx, `INSERT INTO main_office_report_rows(report_id,main_office_id,main_office_name_snapshot,main_office_sort_order_snapshot)
		SELECT $1,o.id,o.name,o.sort_order FROM main_offices o
		JOIN report_unit_responsibles a ON a.assigned_from<=$3 AND (a.assigned_to IS NULL OR a.assigned_to>=$3) AND a.report_type='main_office' AND a.unit_id=o.id::text AND a.user_id=$2
		WHERE o.active ON CONFLICT DO NOTHING`, reportID, claims.UserID, date)
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.loadMainOfficeRows(ctx, reportID, date)
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err = a.filterAssignedRows(ctx, date, "main_office", claims.UserID, rows)
	if err != nil {
		serverError(w, err)
		return
	}
	plans, err := a.candidatePlanTotals(ctx, date, claims.UserID, "main_office")
	if err != nil {
		serverError(w, err)
		return
	}
	applyCandidatePlans(rows, plans)
	editable := a.hasReportEditAccess(ctx, claims.UserID, date)
	previousFirstColumnHasData, err := a.mainOfficePreviousFirstColumnHasData(ctx, reportID, claims.UserID, date)
	if err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, 200, map[string]any{"report": map[string]any{"id": reportID, "date": date, "status": "draft", "editable": editable, "previousFirstColumnHasData": previousFirstColumnHasData}, "rows": rows, "plan": plans.Hired, "invitationPlan": plans.Invited, "hiringPlan": plans.Hired, "totals": totals(rows, plans)})
}

func (a *App) mainOfficePreviousFirstColumnHasData(ctx context.Context, reportID, userID, date string) (bool, error) {
	previousDate, ok := priorBusinessDate(date)
	if !ok {
		return false, nil
	}
	var hasData bool
	err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM main_office_report_rows target
		JOIN main_office_daily_shared source ON source.report_date=$4 AND source.main_office_id=target.main_office_id
		WHERE target.report_id=$1 AND source.open_vacancies<>0
		AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$2
		AND a.report_type='main_office' AND a.unit_id=target.main_office_id::text AND a.assigned_from<=$3
		AND (a.assigned_to IS NULL OR a.assigned_to>=$3)))`, reportID, userID, date, previousDate).Scan(&hasData)
	return hasData, err
}

func (a *App) mainOfficeEfficiencyPlanTotal(ctx context.Context, date, ownerID string) (int, error) {
	var plan int
	err := a.db.QueryRow(ctx, `SELECT COALESCE(sum(COALESCE(
		(SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=u.id AND d.report_date=$1::date AND d.report_type='main_office'),
		(SELECT p.plan_count FROM main_office_employee_efficiency_plans p WHERE p.user_id=u.id AND p.effective_from<=$1::date ORDER BY p.effective_from DESC LIMIT 1),0)),0)::int FROM users u
	WHERE u.role='employee' AND u.active AND NOT u.system AND ($2='' OR u.id::text=$2)`, date, ownerID).Scan(&plan)
	return plan, err
}

func (a *App) loadMainOfficeRows(ctx context.Context, reportID, date string) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `SELECT rr.id,o.id,COALESCE(NULLIF(rr.main_office_name_snapshot,''),o.name),COALESCE(NULLIF(rr.main_office_sort_order_snapshot,0),o.sort_order),COALESCE(ds.open_vacancies,0),COALESCE(ds.planned_reserve,0),rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,rr.dismissed_workers
		FROM main_office_report_rows rr JOIN main_offices o ON o.id=rr.main_office_id
		LEFT JOIN main_office_daily_shared ds ON ds.report_date=$2 AND ds.main_office_id=o.id
		WHERE rr.report_id=$1 ORDER BY COALESCE(NULLIF(rr.main_office_sort_order_snapshot,0),o.sort_order),COALESCE(NULLIF(rr.main_office_name_snapshot,''),o.name)`, reportID, date)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	rows := []reportRow{}
	for q.Next() {
		var row reportRow
		if err = q.Scan(&row.ID, &row.OfficeID, &row.OfficeName, &row.SortOrder, &row.OpenVacancies, &row.PlannedReserve, &row.InvitedCandidates, &row.InterviewedCandidates, &row.Interns, &row.ReserveCandidates, &row.DismissedWorkers); err != nil {
			return nil, err
		}
		row.HiredWorkers = []hiredWorker{}
		row.People = map[string][]string{}
		rows = append(rows, row)
	}
	if err = q.Err(); err != nil {
		return nil, err
	}
	for index := range rows {
		hired, queryErr := a.db.Query(ctx, `SELECT full_name,position FROM main_office_hired_workers WHERE report_row_id=$1 ORDER BY created_at,id`, rows[index].ID)
		if queryErr != nil {
			return nil, queryErr
		}
		for hired.Next() {
			var worker hiredWorker
			if err = hired.Scan(&worker.FullName, &worker.Position); err != nil {
				hired.Close()
				return nil, err
			}
			rows[index].HiredWorkers = append(rows[index].HiredWorkers, worker)
		}
		hired.Close()
		people, queryErr := a.db.Query(ctx, `SELECT category,full_name FROM main_office_report_row_people WHERE report_row_id=$1 ORDER BY category,created_at,id`, rows[index].ID)
		if queryErr != nil {
			return nil, queryErr
		}
		for people.Next() {
			var category, name string
			if err = people.Scan(&category, &name); err != nil {
				people.Close()
				return nil, err
			}
			rows[index].People[category] = append(rows[index].People[category], name)
		}
		people.Close()
	}
	return rows, nil
}

func (a *App) loadMainOfficeAggregateRows(ctx context.Context, date, ownerID string, plans candidatePlans) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `WITH relevant_reports AS (
		SELECT rp.* FROM main_office_reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
	), scope AS (
		SELECT o.id,
		COALESCE((SELECT NULLIF(rr2.main_office_name_snapshot,'') FROM main_office_report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.main_office_id=o.id ORDER BY rp2.created_at LIMIT 1),o.name) AS name,
		COALESCE((SELECT NULLIF(rr2.main_office_sort_order_snapshot,0) FROM main_office_report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.main_office_id=o.id ORDER BY rp2.created_at LIMIT 1),o.sort_order) AS sort_order
		FROM main_offices o WHERE o.active OR EXISTS(
			SELECT 1 FROM main_office_report_rows rr JOIN relevant_reports rp ON rp.id=rr.report_id WHERE rr.main_office_id=o.id)
	), hired AS (SELECT report_row_id,count(*) AS n FROM main_office_hired_workers GROUP BY report_row_id)
	SELECT o.id,o.name,o.sort_order,COALESCE(ds.open_vacancies,0),COALESCE(ds.planned_reserve,0),COALESCE(sum(rr.invited_candidates),0),COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(rr.dismissed_workers),0),COALESCE(sum(hired.n),0)
	FROM scope o LEFT JOIN relevant_reports rp ON true LEFT JOIN main_office_report_rows rr ON rr.report_id=rp.id AND rr.main_office_id=o.id
	LEFT JOIN hired ON hired.report_row_id=rr.id LEFT JOIN main_office_daily_shared ds ON ds.report_date=$1 AND ds.main_office_id=o.id
	GROUP BY o.id,o.name,o.sort_order,ds.open_vacancies,ds.planned_reserve ORDER BY o.sort_order,o.name`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	rows := []reportRow{}
	for q.Next() {
		var row reportRow
		var hired int
		if err = q.Scan(&row.OfficeID, &row.OfficeName, &row.SortOrder, &row.OpenVacancies, &row.PlannedReserve, &row.InvitedCandidates, &row.InterviewedCandidates, &row.Interns, &row.ReserveCandidates, &row.DismissedWorkers, &hired); err != nil {
			return nil, err
		}
		row.HiredWorkers = make([]hiredWorker, hired)
		row.HiredDetails = []hiredDetail{}
		row.People = map[string][]string{}
		row.PeopleDetails = map[string][]hiredDetail{}
		row.Contributions = map[string][]cellContribution{}
		row.EfficiencyPlan = plans.Hired
		row.InvitationPlan = plans.Invited
		row.HiringPlan = plans.Hired
		rows = append(rows, row)
	}
	if err = q.Err(); err != nil {
		return nil, err
	}
	byOffice := map[string]*reportRow{}
	for index := range rows {
		byOffice[rows[index].OfficeID] = &rows[index]
	}
	contributions, err := a.db.Query(ctx, `SELECT rr.main_office_id,COALESCE(NULLIF(rp.owner_name_snapshot,''),u.username),rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,rr.dismissed_workers,(SELECT count(*) FROM main_office_hired_workers h WHERE h.report_row_id=rr.id),
		COALESCE((SELECT d.invited_plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='main_office'),(SELECT plan_count FROM main_office_employee_invitation_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0),
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='main_office'),(SELECT plan_count FROM main_office_employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0)
		FROM main_office_reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system JOIN main_office_report_rows rr ON rr.report_id=rp.id
		WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2) ORDER BY rp.owner_name_snapshot`, date, ownerID)
	if err != nil {
		return nil, err
	}
	for contributions.Next() {
		var officeID, name string
		var invited, interviewed, interns, reserve, dismissed, hired, invitationPlan, hiringPlan int
		if err = contributions.Scan(&officeID, &name, &invited, &interviewed, &interns, &reserve, &dismissed, &hired, &invitationPlan, &hiringPlan); err != nil {
			contributions.Close()
			return nil, err
		}
		if row := byOffice[officeID]; row != nil {
			values := map[string]int{"invitedCandidates": invited, "interviewedCandidates": interviewed, "interns": interns, "reserveCandidates": reserve, "dismissedWorkers": dismissed, "hiredWorkers": hired}
			for key, value := range values {
				row.Contributions[key] = append(row.Contributions[key], cellContribution{Name: name, Value: float64(value)})
			}
			row.Contributions["invitationEfficiency"] = append(row.Contributions["invitationEfficiency"], cellContribution{Name: name, Value: Efficiency(invited, invitationPlan)})
			row.Contributions["hiringEfficiency"] = append(row.Contributions["hiringEfficiency"], cellContribution{Name: name, Value: Efficiency(hired, hiringPlan)})
		}
	}
	contributions.Close()
	if err = a.loadMainOfficeAggregateDetails(ctx, date, ownerID, byOffice); err != nil {
		return nil, err
	}
	shared, err := a.db.Query(ctx, `SELECT ds.main_office_id,COALESCE(NULLIF(ds.updated_by_name_snapshot,''),u.username,'Общее значение'),ds.open_vacancies,ds.planned_reserve FROM main_office_daily_shared ds LEFT JOIN users u ON u.id=ds.updated_by_user_id WHERE ds.report_date=$1`, date)
	if err != nil {
		return nil, err
	}
	defer shared.Close()
	for shared.Next() {
		var officeID, name string
		var openVacancies, plannedReserve int
		if err = shared.Scan(&officeID, &name, &openVacancies, &plannedReserve); err != nil {
			return nil, err
		}
		if row := byOffice[officeID]; row != nil {
			row.Contributions["openVacancies"] = []cellContribution{{Name: name, Value: float64(openVacancies)}}
			row.Contributions["plannedReserve"] = []cellContribution{{Name: name, Value: float64(plannedReserve)}}
		}
	}
	return rows, shared.Err()
}

func (a *App) loadMainOfficeAggregateDetails(ctx context.Context, date, ownerID string, rows map[string]*reportRow) error {
	hired, err := a.db.Query(ctx, `SELECT rr.main_office_id,h.full_name,h.position,COALESCE(NULLIF(rp.owner_name_snapshot,''),u.username) FROM main_office_reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system JOIN main_office_report_rows rr ON rr.report_id=rp.id JOIN main_office_hired_workers h ON h.report_row_id=rr.id WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2) ORDER BY rp.owner_name_snapshot,h.created_at`, date, ownerID)
	if err != nil {
		return err
	}
	for hired.Next() {
		var officeID, name, position, responsible string
		if err = hired.Scan(&officeID, &name, &position, &responsible); err != nil {
			hired.Close()
			return err
		}
		if row := rows[officeID]; row != nil {
			row.HiredDetails = append(row.HiredDetails, hiredDetail{FullName: name, Position: position, Responsible: responsible})
		}
	}
	hired.Close()
	people, err := a.db.Query(ctx, `SELECT rr.main_office_id,p.category,p.full_name,COALESCE(NULLIF(rp.owner_name_snapshot,''),u.username) FROM main_office_reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system JOIN main_office_report_rows rr ON rr.report_id=rp.id JOIN main_office_report_row_people p ON p.report_row_id=rr.id WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2) ORDER BY rp.owner_name_snapshot,p.created_at`, date, ownerID)
	if err != nil {
		return err
	}
	defer people.Close()
	for people.Next() {
		var officeID, category, name, responsible string
		if err = people.Scan(&officeID, &category, &name, &responsible); err != nil {
			return err
		}
		if row := rows[officeID]; row != nil {
			row.PeopleDetails[category] = append(row.PeopleDetails[category], hiredDetail{FullName: name, Responsible: responsible})
		}
	}
	return people.Err()
}

func (a *App) updateMainOfficeRow(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	if claims.Role != "employee" {
		problem(w, 403, "Сводный отчёт доступен только для просмотра")
		return
	}
	var input rowInput
	if !decode(w, r, &input) {
		return
	}
	if hasNegative(input) {
		problem(w, 422, "Числовые значения не могут быть отрицательными")
		return
	}
	input.People = cleanPeople(input.People)
	if names, ok := input.People["invited_candidates"]; ok {
		input.InvitedCandidates = len(names)
	}
	if names, ok := input.People["interviewed_candidates"]; ok {
		input.InterviewedCandidates = len(names)
	}
	if names, ok := input.People["interns"]; ok {
		input.Interns = len(names)
	}
	if names, ok := input.People["reserve_candidates"]; ok {
		input.ReserveCandidates = len(names)
	}
	if names, ok := input.People["dismissed_workers"]; ok {
		input.DismissedWorkers = len(names)
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var mainOfficeID, reportDate string
	var invitationPlan, hiringPlan int
	err = tx.QueryRow(ctx, `SELECT rr.main_office_id,rp.report_date::text,
		COALESCE((SELECT d.invited_plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='main_office'),(SELECT plan_count FROM main_office_employee_invitation_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0),
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='main_office'),(SELECT plan_count FROM main_office_employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0)
		FROM main_office_report_rows rr JOIN main_office_reports rp ON rp.id=rr.report_id WHERE rr.id=$1 AND rp.owner_user_id=$2
		AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.assigned_from<=rp.report_date AND (a.assigned_to IS NULL OR a.assigned_to>=rp.report_date) AND a.report_type='main_office' AND a.unit_id=rr.main_office_id::text AND a.user_id=$2)
		AND (rp.report_date=$3 OR EXISTS(SELECT 1 FROM report_access_grants g WHERE g.report_date=rp.report_date AND g.user_id=$2 AND g.expires_at>now()))`, r.PathValue("id"), claims.UserID, localToday()).Scan(&mainOfficeID, &reportDate, &invitationPlan, &hiringPlan)
	if err != nil {
		problem(w, 409, "Доступ к редактированию отчёта закрыт")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO main_office_daily_shared(report_date,main_office_id,open_vacancies,planned_reserve,updated_by_user_id,updated_by_name_snapshot) SELECT $1,$2,$3,$4,u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username) FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$5 ON CONFLICT(report_date,main_office_id) DO UPDATE SET open_vacancies=EXCLUDED.open_vacancies,planned_reserve=EXCLUDED.planned_reserve,updated_by_user_id=EXCLUDED.updated_by_user_id,updated_by_name_snapshot=EXCLUDED.updated_by_name_snapshot,updated_at=now()`, reportDate, mainOfficeID, input.OpenVacancies, input.PlannedReserve, claims.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = tx.Exec(ctx, `UPDATE main_office_report_rows SET open_vacancies=0,invited_candidates=$2,interviewed_candidates=$3,interns=$4,reserve_candidates=$5,dismissed_workers=$6,updated_at=now() WHERE id=$1`, r.PathValue("id"), input.InvitedCandidates, input.InterviewedCandidates, input.Interns, input.ReserveCandidates, input.DismissedWorkers)
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = tx.Exec(ctx, `DELETE FROM main_office_hired_workers WHERE report_row_id=$1`, r.PathValue("id"))
	cleanHires := cleanHiredWorkers(input.HiredWorkers)
	for _, worker := range cleanHires {
		if _, err = tx.Exec(ctx, `INSERT INTO main_office_hired_workers(report_row_id,full_name,position) VALUES($1,$2,$3)`, r.PathValue("id"), worker.FullName, worker.Position); err != nil {
			serverError(w, err)
			return
		}
	}
	for category, names := range input.People {
		if _, err = tx.Exec(ctx, `DELETE FROM main_office_report_row_people WHERE report_row_id=$1 AND category=$2`, r.PathValue("id"), category); err != nil {
			serverError(w, err)
			return
		}
		for _, name := range names {
			if _, err = tx.Exec(ctx, `INSERT INTO main_office_report_row_people(report_row_id,category,full_name) VALUES($1,$2,$3)`, r.PathValue("id"), category, name); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.log(ctx, "main_office_report_row.updated", "main_office_report_row", r.PathValue("id"))
	a.reports.send(reportDate, map[string]any{"type": "main_office_report_updated", "date": reportDate, "officeId": mainOfficeID, "field": "openVacancies", "value": input.OpenVacancies, "updatedBy": claims.Username})
	a.reports.send(reportDate, map[string]any{"type": "main_office_report_updated", "date": reportDate, "officeId": mainOfficeID, "field": "plannedReserve", "value": input.PlannedReserve, "updatedBy": claims.Username})
	hiringEfficiency := Efficiency(len(cleanHires), hiringPlan)
	jsonOut(w, 200, map[string]any{"efficiency": hiringEfficiency, "invitationEfficiency": Efficiency(input.InvitedCandidates, invitationPlan), "hiringEfficiency": hiringEfficiency, "hiredCount": len(cleanHires)})
}

type copyPreviousMainOfficeReportInput struct {
	Date string `json:"date"`
}

func priorBusinessDate(value string) (string, bool) {
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", false
	}
	for {
		date = date.AddDate(0, 0, -1)
		if date.Weekday() != time.Saturday && date.Weekday() != time.Sunday {
			return date.Format("2006-01-02"), true
		}
	}
}

func (a *App) copyPreviousMainOfficeReport(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	if claims.Role != "employee" {
		problem(w, 403, "Копирование доступно только сотруднику")
		return
	}
	var input copyPreviousMainOfficeReportInput
	if !decode(w, r, &input) {
		return
	}
	input.Date = strings.TrimSpace(input.Date)
	if !validDate(input.Date) || weekendDate(input.Date) {
		problem(w, 422, "Выберите рабочий день")
		return
	}
	if !a.hasReportEditAccess(r.Context(), claims.UserID, input.Date) {
		problem(w, 409, "Доступ к редактированию отчёта закрыт")
		return
	}
	previousDate, ok := priorBusinessDate(input.Date)
	if !ok {
		problem(w, 422, "Не удалось определить предыдущий рабочий день")
		return
	}

	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	var targetReportID string
	err = tx.QueryRow(ctx, `SELECT id FROM main_office_reports WHERE report_date=$1 AND owner_user_id=$2 FOR UPDATE`, input.Date, claims.UserID).Scan(&targetReportID)
	if err != nil {
		problem(w, 409, "Сначала откройте отчёт за выбранный день")
		return
	}
	var targetRows int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM main_office_report_rows rr
		WHERE rr.report_id=$1 AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$2
		AND a.report_type='main_office' AND a.unit_id=rr.main_office_id::text AND a.assigned_from<=$3
		AND (a.assigned_to IS NULL OR a.assigned_to>=$3))`, targetReportID, claims.UserID, input.Date).Scan(&targetRows)
	if err != nil {
		serverError(w, err)
		return
	}
	if targetRows == 0 {
		problem(w, 422, "На выбранный день нет назначенных компаний")
		return
	}
	var firstColumnEmpty bool
	err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM main_office_report_rows rr
		JOIN main_office_daily_shared ds ON ds.report_date=$3 AND ds.main_office_id=rr.main_office_id
		WHERE rr.report_id=$1 AND COALESCE(ds.open_vacancies,0)<>0
		AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$2
		AND a.report_type='main_office' AND a.unit_id=rr.main_office_id::text AND a.assigned_from<=$3
		AND (a.assigned_to IS NULL OR a.assigned_to>=$3)))`, targetReportID, claims.UserID, input.Date).Scan(&firstColumnEmpty)
	if err != nil {
		serverError(w, err)
		return
	}
	if !firstColumnEmpty {
		problem(w, 409, "Первая колонка текущего отчёта уже содержит данные")
		return
	}
	var sourceHasData bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM main_office_report_rows target
		JOIN main_office_daily_shared source ON source.report_date=$4 AND source.main_office_id=target.main_office_id
		WHERE target.report_id=$1 AND source.open_vacancies<>0
		AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$2
		AND a.report_type='main_office' AND a.unit_id=target.main_office_id::text AND a.assigned_from<=$3
		AND (a.assigned_to IS NULL OR a.assigned_to>=$3)))`, targetReportID, claims.UserID, input.Date, previousDate).Scan(&sourceHasData)
	if err != nil {
		serverError(w, err)
		return
	}
	if !sourceHasData {
		problem(w, 422, "В первой колонке за предыдущий рабочий день нет данных для копирования")
		return
	}

	tag, err := tx.Exec(ctx, `INSERT INTO main_office_daily_shared(report_date,main_office_id,open_vacancies,updated_by_user_id,updated_by_name_snapshot)
		SELECT $1,target.main_office_id,source.open_vacancies,u.id,
		COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username)
		FROM main_office_report_rows target
		JOIN main_office_daily_shared source ON source.report_date=$2 AND source.main_office_id=target.main_office_id
		JOIN users u ON u.id=$4 LEFT JOIN employees e ON e.id=u.employee_id
		WHERE target.report_id=$3 AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$4
		AND a.report_type='main_office' AND a.unit_id=target.main_office_id::text AND a.assigned_from<=$1
		AND (a.assigned_to IS NULL OR a.assigned_to>=$1))
		ON CONFLICT(report_date,main_office_id) DO UPDATE SET open_vacancies=EXCLUDED.open_vacancies,
		updated_by_user_id=EXCLUDED.updated_by_user_id,updated_by_name_snapshot=EXCLUDED.updated_by_name_snapshot,updated_at=now()`,
		input.Date, previousDate, targetReportID, claims.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.log(ctx, "main_office_report.first_column_copied_previous", "main_office_report", targetReportID)
	a.reports.send(input.Date, map[string]any{"type": "main_office_report_updated", "date": input.Date, "field": "copied"})
	jsonOut(w, 200, map[string]any{"ok": true, "sourceDate": previousDate, "copiedRows": tag.RowsAffected()})
}
