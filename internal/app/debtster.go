package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	debtsterDepartmentsPath = "/api/v1/list/departments/hr-report-submitters"
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
func CheckDebtsterAPI(ctx context.Context, baseURL string) (int, error) {
	departments, err := fetchDebtsterDepartments(ctx, &http.Client{Timeout: 10 * time.Second}, baseURL)
	if err != nil {
		return 0, err
	}
	return len(departments), nil
}

type debtsterDepartment struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	SortOrder   int    `json:"-"`
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("request departments: unexpected HTTP status %d", resp.StatusCode)
	}
	var payload struct {
		Data []debtsterDepartment `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err = decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode departments: %w", err)
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
