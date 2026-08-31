package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	debtsterDepartmentsPath = "/api/v1/list/departments/hr-report-submitters"
	debtsterVacanciesPath   = "/api/v1/report/vacancies"
	debtsterReportsFrom     = "2026-08-28"
)

func usesDebtsterDepartments(date string) bool {
	return date >= debtsterReportsFrom
}

func shouldSyncDebtsterDepartments(date, today string) bool {
	return date == today && usesDebtsterDepartments(date)
}

// CheckDebtsterAPI verifies that the configured Debtster endpoint is reachable
// and returns a valid department list without changing application data.
func CheckDebtsterAPI(ctx context.Context, baseURL string) (int, int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	departments, departmentsErr := fetchDebtsterDepartments(ctx, client, baseURL)
	vacancies, vacanciesErr := fetchDebtsterVacancies(ctx, client, baseURL, localToday())
	if departmentsErr != nil || vacanciesErr != nil {
		departmentsStatus, vacanciesStatus := fmt.Sprintf("ok (%d received)", len(departments)), fmt.Sprintf("ok (%d received)", len(vacancies))
		if departmentsErr != nil {
			departmentsStatus = departmentsErr.Error()
		}
		if vacanciesErr != nil {
			vacanciesStatus = vacanciesErr.Error()
		}
		return len(departments), len(vacancies), fmt.Errorf("departments: %s; vacancies: %s", departmentsStatus, vacanciesStatus)
	}
	return len(departments), len(vacancies), nil
}

type debtsterDepartment struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	SortOrder   int    `json:"-"`
}

type debtsterPlannedDismissal struct {
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	MiddleName string `json:"middle_name"`
}

type debtsterVacancyReport struct {
	ID                     int                        `json:"id"`
	RP                     string                     `json:"rp"`
	StaffPositionsCount    int                        `json:"staff_positions_count"`
	ActiveEmployeesCount   int                        `json:"active_employees_count"`
	VacantPositionsCount   int                        `json:"vacant_positions_count"`
	TraineesCount          int                        `json:"trainees_count"`
	RecruitmentCount       int                        `json:"recruitment_count"`
	PlannedDismissalsCount int                        `json:"planned_dismissals_count"`
	PlannedDismissals      []debtsterPlannedDismissal `json:"planned_dismissals"`
}

func fetchDebtsterVacancies(ctx context.Context, client *http.Client, baseURL, date string) ([]debtsterVacancyReport, error) {
	endpoint := strings.TrimRight(baseURL, "/") + debtsterVacanciesPath + "?report_date=" + url.QueryEscape(date)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create vacancies request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request vacancies: %w", err)
	}
	defer resp.Body.Close()
	responseURL := resp.Request.URL.String()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("request vacancies: HTTP %d from %s", resp.StatusCode, responseURL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read vacancies from %s: %w", responseURL, err)
	}
	if len(body) > 2<<20 {
		return nil, fmt.Errorf("decode vacancies from %s: response is larger than 2 MiB", responseURL)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		return nil, fmt.Errorf("decode vacancies: %s returned HTML instead of JSON (content-type %q)", responseURL, resp.Header.Get("Content-Type"))
	}
	var payload struct {
		ErrorCode int                     `json:"error_code"`
		Status    string                  `json:"status"`
		Message   string                  `json:"message"`
		Data      []debtsterVacancyReport `json:"data"`
	}
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode vacancies from %s: %w", responseURL, err)
	}
	if payload.ErrorCode != 0 || (payload.Status != "" && payload.Status != "success") {
		return nil, fmt.Errorf("request vacancies: Debtster error %d: %s", payload.ErrorCode, strings.TrimSpace(payload.Message))
	}
	seen := make(map[int]struct{}, len(payload.Data))
	out := make([]debtsterVacancyReport, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.ID <= 0 || item.StaffPositionsCount < 0 || item.ActiveEmployeesCount < 0 || item.VacantPositionsCount < 0 || item.TraineesCount < 0 || item.RecruitmentCount < 0 || item.PlannedDismissalsCount < 0 {
			return nil, fmt.Errorf("decode vacancies: invalid department id=%d", item.ID)
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		for index := range item.PlannedDismissals {
			item.PlannedDismissals[index].FirstName = strings.TrimSpace(item.PlannedDismissals[index].FirstName)
			item.PlannedDismissals[index].LastName = strings.TrimSpace(item.PlannedDismissals[index].LastName)
			item.PlannedDismissals[index].MiddleName = strings.TrimSpace(item.PlannedDismissals[index].MiddleName)
		}
		out = append(out, item)
	}
	return out, nil
}

func applyDebtsterVacancies(rows []reportRow, vacancies []debtsterVacancyReport) {
	byID := make(map[string]debtsterVacancyReport, len(vacancies))
	for _, item := range vacancies {
		byID[strconv.Itoa(item.ID)] = item
	}
	for index := range rows {
		item, exists := byID[rows[index].OfficeID]
		if !exists {
			continue
		}
		rows[index].StaffPositionsCount = item.StaffPositionsCount
		rows[index].ActiveEmployeesCount = item.ActiveEmployeesCount
		rows[index].VacantPositionsCount = item.VacantPositionsCount
		rows[index].TraineesCount = item.TraineesCount
		rows[index].RecruitmentCount = item.RecruitmentCount
		rows[index].PlannedDismissalsCount = item.PlannedDismissalsCount
		rows[index].PlannedDismissals = item.PlannedDismissals
	}
}

// loadDebtsterTraineeBaselines keeps the first successfully received value for
// every department and report date. ON CONFLICT DO NOTHING makes that first
// value stable when several report requests arrive concurrently.
func (a *App) loadDebtsterTraineeBaselines(ctx context.Context, date string, vacancies []debtsterVacancyReport) (map[int]int, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin Debtster trainee cache: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, item := range vacancies {
		if _, err = tx.Exec(ctx, `INSERT INTO debtster_daily_trainees(report_date,debtster_department_id,trainees_count)
			VALUES ($1,$2,$3) ON CONFLICT(report_date,debtster_department_id) DO NOTHING`, date, item.ID, item.TraineesCount); err != nil {
			return nil, fmt.Errorf("save Debtster trainee cache for department %d: %w", item.ID, err)
		}
	}

	query, err := tx.Query(ctx, `SELECT debtster_department_id,trainees_count
		FROM debtster_daily_trainees WHERE report_date=$1`, date)
	if err != nil {
		return nil, fmt.Errorf("read Debtster trainee cache for %s: %w", date, err)
	}
	baselines := make(map[int]int)
	for query.Next() {
		var departmentID, trainees int
		if err = query.Scan(&departmentID, &trainees); err != nil {
			query.Close()
			return nil, fmt.Errorf("scan Debtster trainee cache for %s: %w", date, err)
		}
		baselines[departmentID] = trainees
	}
	if err = query.Err(); err != nil {
		query.Close()
		return nil, fmt.Errorf("read Debtster trainee cache for %s: %w", date, err)
	}
	query.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit Debtster trainee cache: %w", err)
	}
	return baselines, nil
}

func applyDebtsterTraineeChanges(rows []reportRow, vacancies []debtsterVacancyReport, baselines map[int]int) {
	current := make(map[int]int, len(vacancies))
	for _, item := range vacancies {
		current[item.ID] = item.TraineesCount
	}
	for index := range rows {
		departmentID, err := strconv.Atoi(rows[index].OfficeID)
		if err != nil {
			continue
		}
		baseline, cached := baselines[departmentID]
		value, received := current[departmentID]
		if received {
			rows[index].TraineesCount = value
			if cached {
				rows[index].TraineesCountChange = value - baseline
			}
			continue
		}
		if cached {
			rows[index].TraineesCount = baseline
			rows[index].TraineesCountChange = 0
		}
	}
}

func appendMissingDebtsterVacancyRows(rows []reportRow, vacancies []debtsterVacancyReport, plan int) []reportRow {
	existing := make(map[string]struct{}, len(rows))
	maxSortOrder := 0
	for index := range rows {
		existing[rows[index].OfficeID] = struct{}{}
		if rows[index].SortOrder > maxSortOrder {
			maxSortOrder = rows[index].SortOrder
		}
	}
	for _, vacancy := range vacancies {
		id := strconv.Itoa(vacancy.ID)
		if _, exists := existing[id]; exists {
			continue
		}
		maxSortOrder++
		rows = append(rows, reportRow{
			OfficeID:          id,
			OfficeName:        strings.TrimSpace(vacancy.RP),
			SortOrder:         maxSortOrder,
			EfficiencyPlan:    plan,
			HiredWorkers:      []hiredWorker{},
			HiredDetails:      []hiredDetail{},
			People:            map[string][]string{},
			PeopleDetails:     map[string][]hiredDetail{},
			Contributions:     map[string][]cellContribution{},
			PlannedDismissals: []debtsterPlannedDismissal{},
		})
		existing[id] = struct{}{}
	}
	return rows
}

func appendMissingDebtsterRows(rows []reportRow, departments []debtsterDepartment, plan int) []reportRow {
	existing := make(map[string]struct{}, len(rows))
	for index := range rows {
		existing[rows[index].OfficeID] = struct{}{}
	}
	for _, department := range departments {
		id := strconv.Itoa(department.ID)
		if _, exists := existing[id]; exists {
			continue
		}
		rows = append(rows, reportRow{
			OfficeID:       id,
			OfficeName:     department.DisplayName,
			SortOrder:      department.SortOrder,
			EfficiencyPlan: plan,
			HiredWorkers:   []hiredWorker{},
			HiredDetails:   []hiredDetail{},
			People:         map[string][]string{},
			PeopleDetails:  map[string][]hiredDetail{},
			Contributions:  map[string][]cellContribution{},
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].SortOrder == rows[j].SortOrder {
			return rows[i].OfficeName < rows[j].OfficeName
		}
		return rows[i].SortOrder < rows[j].SortOrder
	})
	return rows
}

func fetchDebtsterDepartments(ctx context.Context, client *http.Client, baseURL string) ([]debtsterDepartment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+debtsterDepartmentsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request departments: %w", err)
	}
	defer resp.Body.Close()
	responseURL := resp.Request.URL.String()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("request departments: HTTP %d from %s", resp.StatusCode, responseURL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read departments from %s: %w", responseURL, err)
	}
	if len(body) > 2<<20 {
		return nil, fmt.Errorf("decode departments from %s: response is larger than 2 MiB", responseURL)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		return nil, fmt.Errorf("decode departments: %s returned HTML instead of JSON (content-type %q)", responseURL, resp.Header.Get("Content-Type"))
	}
	var payload struct {
		Data []debtsterDepartment `json:"data"`
	}
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode departments from %s: %w", responseURL, err)
	}
	seen := make(map[int]struct{}, len(payload.Data))
	departments := make([]debtsterDepartment, 0, len(payload.Data))
	for _, department := range payload.Data {
		department.Name = strings.TrimSpace(department.Name)
		department.DisplayName = strings.TrimSpace(department.DisplayName)
		if department.ID <= 0 || department.Name == "" || department.DisplayName == "" {
			return nil, fmt.Errorf("decode departments: invalid department id=%d", department.ID)
		}
		if _, exists := seen[department.ID]; exists {
			continue
		}
		seen[department.ID] = struct{}{}
		departments = append(departments, department)
	}
	return departments, nil
}

func (a *App) syncDebtsterReportRows(ctx context.Context, date string, fetched []debtsterDepartment) ([]debtsterDepartment, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin department sync: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(48425245505)`); err != nil {
		return nil, fmt.Errorf("lock department sync: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (rr.debtster_department_id)
		rr.debtster_department_id,rr.debtster_department_name,rr.office_name_snapshot,rr.office_sort_order_snapshot
		FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id
		WHERE rp.report_date=$1 AND rr.debtster_department_id IS NOT NULL
		ORDER BY rr.debtster_department_id,rr.office_sort_order_snapshot,rr.updated_at DESC`, date)
	if err != nil {
		return nil, fmt.Errorf("read report departments: %w", err)
	}
	departments := make([]debtsterDepartment, 0, len(fetched))
	byID := make(map[int]int, len(fetched))
	for rows.Next() {
		var department debtsterDepartment
		if err = rows.Scan(&department.ID, &department.Name, &department.DisplayName, &department.SortOrder); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan report department: %w", err)
		}
		byID[department.ID] = len(departments)
		departments = append(departments, department)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read report departments: %w", err)
	}
	rows.Close()
	sort.SliceStable(departments, func(i, j int) bool { return departments[i].SortOrder < departments[j].SortOrder })
	byID = make(map[int]int, len(departments)+len(fetched))
	maxSortOrder := 0
	for index := range departments {
		byID[departments[index].ID] = index
		if departments[index].SortOrder > maxSortOrder {
			maxSortOrder = departments[index].SortOrder
		}
	}
	for _, department := range fetched {
		if index, exists := byID[department.ID]; exists {
			departments[index].Name = department.Name
			departments[index].DisplayName = department.DisplayName
			continue
		}
		maxSortOrder++
		department.SortOrder = maxSortOrder
		byID[department.ID] = len(departments)
		departments = append(departments, department)
	}
	for _, department := range departments {
		if _, err = tx.Exec(ctx, `INSERT INTO report_rows(report_id,office_id,office_name_snapshot,office_sort_order_snapshot,debtster_department_id,debtster_department_name)
			SELECT rp.id,NULL,$2,$3,$4,$5 FROM reports rp WHERE rp.report_date=$1
			ON CONFLICT (report_id,debtster_department_id) WHERE debtster_department_id IS NOT NULL
			DO UPDATE SET office_name_snapshot=EXCLUDED.office_name_snapshot,debtster_department_name=EXCLUDED.debtster_department_name,updated_at=now()`,
			date, department.DisplayName, department.SortOrder, department.ID, department.Name); err != nil {
			return nil, fmt.Errorf("save report department %d: %w", department.ID, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit department sync: %w", err)
	}
	return departments, nil
}
