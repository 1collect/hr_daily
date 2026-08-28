package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/xuri/excelize/v2"
)

type exportSummaryRow struct {
	Office                                                                                        string
	DebtsterDepartmentID                                                                          int
	OpenVacancies, Invited, Interviewed, Interns, PlannedReserve, Reserve, Hired, Plan, TotalPlan int
	Responsible                                                                                   string
}

type employeeExportSection struct {
	ID, Name string
	Rows     []exportSummaryRow
}

type exportKindConfig struct {
	Kind, Sheet, Unit, Reports, Rows, Offices, OfficeID, NameSnapshot, SortSnapshot string
	Hired, People, Shared, PlanTable                                                string
}

type exportStyles struct {
	header, body, total, employeeTitle, efficiencyBody, efficiencyTotal int
}

var exportKinds = map[string]exportKindConfig{
	"rp": {
		Kind: "rp", Sheet: "РП", Unit: "РП", Reports: "reports", Rows: "report_rows", Offices: "offices",
		OfficeID: "office_id", NameSnapshot: "office_name_snapshot", SortSnapshot: "office_sort_order_snapshot",
		Hired: "hired_workers", People: "report_row_people", Shared: "daily_office_shared", PlanTable: "employee_efficiency_plans",
	},
	"main_office": {
		Kind: "main_office", Sheet: "ГО", Unit: "Компания", Reports: "main_office_reports", Rows: "main_office_report_rows", Offices: "main_offices",
		OfficeID: "main_office_id", NameSnapshot: "main_office_name_snapshot", SortSnapshot: "main_office_sort_order_snapshot",
		Hired: "main_office_hired_workers", People: "main_office_report_row_people", Shared: "main_office_daily_shared", PlanTable: "main_office_employee_efficiency_plans",
	},
}

func (a *App) exportPeriod(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	reportType := r.URL.Query().Get("reportType")
	if reportType == "" {
		reportType = "rp"
	}
	if !validDate(from) || !validDate(to) || from > to {
		problem(w, 400, "Укажите корректный период")
		return
	}
	if reportType != "rp" && reportType != "main_office" && reportType != "all" {
		problem(w, 400, "Укажите корректный тип отчёта")
		return
	}

	kinds := []exportKindConfig{}
	if reportType == "rp" || reportType == "all" {
		kinds = append(kinds, exportKinds["rp"])
	}
	if reportType == "main_office" || reportType == "all" {
		kinds = append(kinds, exportKinds["main_office"])
	}
	var debtsterSnapshot []debtsterVacancyReport
	if reportType == "rp" || reportType == "all" {
		var err error
		debtsterSnapshot, err = fetchDebtsterVacancies(r.Context(), a.httpClient, a.debtsterAPI, to)
		if err != nil {
			serverError(w, fmt.Errorf("read Debtster export snapshot for %s: %w", to, err))
			return
		}
	}

	f := excelize.NewFile()
	styles := newExportStyles(f)
	for index, kind := range kinds {
		rows, sections, err := a.loadExportData(r.Context(), kind, from, to)
		if err != nil {
			serverError(w, err)
			return
		}
		if kind.Kind == "rp" {
			applyDebtsterExportSnapshot(rows, sections, debtsterSnapshot)
		}
		if index == 0 {
			f.SetSheetName("Sheet1", kind.Sheet)
		} else {
			_, _ = f.NewSheet(kind.Sheet)
		}
		writeExportSummarySheet(f, kind, rows, sections, styles)
		if err = a.writeExportDetailSheets(r.Context(), f, kind, from, to, styles); err != nil {
			serverError(w, err)
			return
		}
	}
	f.SetActiveSheet(0)
	buf, err := f.WriteToBuffer()
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="HR_%s_%s.xlsx"`, from, to))
	_, _ = w.Write(buf.Bytes())
}

// applyDebtsterExportSnapshot replaces the RP values that are no longer entered
// manually with the Debtster snapshot for the export period's final date. The
// planned dismissal names are intentionally not copied to the export model.
func applyDebtsterExportSnapshot(rows []exportSummaryRow, sections []employeeExportSection, snapshot []debtsterVacancyReport) {
	byDepartment := make(map[int]debtsterVacancyReport, len(snapshot))
	byOfficeName := make(map[string]debtsterVacancyReport, len(snapshot))
	for _, item := range snapshot {
		byDepartment[item.ID] = item
		if name := normalizeExportOfficeName(item.RP); name != "" {
			byOfficeName[name] = item
		}
	}
	apply := func(items []exportSummaryRow) {
		for index := range items {
			item, exists := byDepartment[items[index].DebtsterDepartmentID]
			if !exists {
				item, exists = byOfficeName[normalizeExportOfficeName(items[index].Office)]
			}
			if !exists {
				items[index].OpenVacancies = 0
				items[index].Interns = 0
				items[index].PlannedReserve = 0
				continue
			}
			items[index].OpenVacancies = item.VacantPositionsCount
			items[index].Interns = item.TraineesCount
			items[index].PlannedReserve = item.PlannedDismissalsCount
		}
	}
	apply(rows)
	for index := range sections {
		apply(sections[index].Rows)
	}
}

func normalizeExportOfficeName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

func (a *App) loadExportData(ctx context.Context, kind exportKindConfig, from, to string) ([]exportSummaryRow, []employeeExportSection, error) {
	if kind.Kind == "rp" {
		return a.loadRPExportData(ctx, from, to)
	}
	officeScope := "o.active"
	if kind.Kind == "rp" {
		officeScope = "o.debtster_department_id IS NOT NULL"
	}
	relevant := fmt.Sprintf(`SELECT planned.*,
		sum(efficiency_plan) OVER () AS total_efficiency_plan,
		sum(efficiency_plan) OVER (PARTITION BY owner_user_id) AS owner_efficiency_plan
		FROM (SELECT rp.*,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username) AS owner_name,
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type=$3),
		(SELECT plan_count FROM %s p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0) AS efficiency_plan
		FROM %s rp JOIN users u ON u.id=rp.owner_user_id AND u.role='employee' AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id WHERE rp.report_date BETWEEN $1 AND $2) planned`, kind.PlanTable, kind.Reports)

	summaryQuery := fmt.Sprintf(`WITH relevant_reports AS (%s), office_scope AS (
		SELECT o.id,
		COALESCE((SELECT NULLIF(rr2.%s,'') FROM %s rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.%s=o.id ORDER BY rp2.report_date,rp2.created_at LIMIT 1),o.name) AS name,
		COALESCE((SELECT NULLIF(rr2.%s,0) FROM %s rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.%s=o.id ORDER BY rp2.report_date,rp2.created_at LIMIT 1),o.sort_order) AS sort_order
		FROM %s o WHERE %s OR EXISTS(SELECT 1 FROM %s rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.%s=o.id)
	), hc AS (SELECT report_row_id,count(*) AS n FROM %s GROUP BY report_row_id), latest_shared AS (
		SELECT DISTINCT ON (ds.%s) ds.%s AS office_id,ds.open_vacancies,ds.planned_reserve FROM %s ds
		WHERE ds.report_date BETWEEN $1 AND $2 ORDER BY ds.%s,ds.report_date DESC,ds.updated_at DESC
	), activity_responsibles AS (
		SELECT rr.%s AS office_id,rp.owner_name AS responsible FROM relevant_reports rp JOIN %s rr ON rr.report_id=rp.id
		LEFT JOIN hc ON hc.report_row_id=rr.id WHERE rr.invited_candidates>0 OR rr.interviewed_candidates>0 OR rr.interns>0 OR rr.reserve_candidates>0 OR COALESCE(hc.n,0)>0
	), office_responsibles AS (
		SELECT office_id,string_agg(responsible,', ' ORDER BY responsible) AS responsible FROM activity_responsibles GROUP BY office_id
	)
	SELECT o.name,COALESCE(lv.open_vacancies,0),COALESCE(sum(rr.invited_candidates),0),COALESCE(sum(rr.interviewed_candidates),0),
		COALESCE(sum(rr.interns),0),COALESCE(lv.planned_reserve,0),COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(hc.n),0),
		COALESCE(sum(rp.efficiency_plan) FILTER(WHERE rr.id IS NOT NULL),0),COALESCE(max(rp.total_efficiency_plan),0),COALESCE(resp.responsible,'')
	FROM office_scope o LEFT JOIN latest_shared lv ON lv.office_id=o.id LEFT JOIN relevant_reports rp ON true
	LEFT JOIN %s rr ON rr.report_id=rp.id AND rr.%s=o.id LEFT JOIN hc ON hc.report_row_id=rr.id
	LEFT JOIN office_responsibles resp ON resp.office_id=o.id
	GROUP BY o.id,o.name,o.sort_order,lv.open_vacancies,lv.planned_reserve,resp.responsible ORDER BY o.sort_order,o.name`,
		relevant, kind.NameSnapshot, kind.Rows, kind.OfficeID, kind.SortSnapshot, kind.Rows, kind.OfficeID,
		kind.Offices, officeScope, kind.Rows, kind.OfficeID, kind.Hired, kind.OfficeID, kind.OfficeID, kind.Shared, kind.OfficeID,
		kind.OfficeID, kind.Rows, kind.Rows, kind.OfficeID)
	query, err := a.db.Query(ctx, summaryQuery, from, to, kind.Kind)
	if err != nil {
		return nil, nil, err
	}
	rows := []exportSummaryRow{}
	for query.Next() {
		var item exportSummaryRow
		if err = query.Scan(&item.Office, &item.OpenVacancies, &item.Invited, &item.Interviewed, &item.Interns, &item.PlannedReserve, &item.Reserve, &item.Hired, &item.Plan, &item.TotalPlan, &item.Responsible); err != nil {
			query.Close()
			return nil, nil, err
		}
		rows = append(rows, item)
	}
	if err = query.Err(); err != nil {
		query.Close()
		return nil, nil, err
	}
	query.Close()

	employeeQuery := fmt.Sprintf(`WITH relevant_reports AS (%s), hc AS (
		SELECT report_row_id,count(*) AS n FROM %s GROUP BY report_row_id
	), latest_shared AS (
		SELECT DISTINCT ON (ds.%s) ds.%s AS office_id,ds.open_vacancies,ds.planned_reserve FROM %s ds
		WHERE ds.report_date BETWEEN $1 AND $2 ORDER BY ds.%s,ds.report_date DESC,ds.updated_at DESC
	)
	SELECT rp.owner_user_id::text,rp.owner_name,COALESCE(NULLIF(rr.%s,''),o.name),COALESCE(lv.open_vacancies,0),
		COALESCE(sum(rr.invited_candidates),0),COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),
		COALESCE(lv.planned_reserve,0),COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(hc.n),0),COALESCE(sum(rp.efficiency_plan),0),COALESCE(max(rp.owner_efficiency_plan),0)
	FROM relevant_reports rp JOIN %s rr ON rr.report_id=rp.id JOIN %s o ON o.id=rr.%s
	LEFT JOIN hc ON hc.report_row_id=rr.id LEFT JOIN latest_shared lv ON lv.office_id=rr.%s
	GROUP BY rp.owner_user_id,rp.owner_name,o.id,COALESCE(NULLIF(rr.%s,''),o.name),COALESCE(NULLIF(rr.%s,0),o.sort_order),lv.open_vacancies,lv.planned_reserve
	ORDER BY rp.owner_name,COALESCE(NULLIF(rr.%s,0),o.sort_order),COALESCE(NULLIF(rr.%s,''),o.name)`,
		relevant, kind.Hired, kind.OfficeID, kind.OfficeID, kind.Shared, kind.OfficeID, kind.NameSnapshot,
		kind.Rows, kind.Offices, kind.OfficeID, kind.OfficeID, kind.NameSnapshot, kind.SortSnapshot, kind.SortSnapshot, kind.NameSnapshot)
	employeeRows, err := a.db.Query(ctx, employeeQuery, from, to, kind.Kind)
	if err != nil {
		return nil, nil, err
	}
	sections := []employeeExportSection{}
	indexes := map[string]int{}
	for employeeRows.Next() {
		var employeeID, employeeName string
		var item exportSummaryRow
		if err = employeeRows.Scan(&employeeID, &employeeName, &item.Office, &item.OpenVacancies, &item.Invited, &item.Interviewed, &item.Interns, &item.PlannedReserve, &item.Reserve, &item.Hired, &item.Plan, &item.TotalPlan); err != nil {
			employeeRows.Close()
			return nil, nil, err
		}
		if item.Invited > 0 || item.Interviewed > 0 || item.Interns > 0 || item.Reserve > 0 || item.Hired > 0 {
			item.Responsible = employeeName
		}
		index, exists := indexes[employeeID]
		if !exists {
			index = len(sections)
			indexes[employeeID] = index
			sections = append(sections, employeeExportSection{ID: employeeID, Name: employeeName})
		}
		sections[index].Rows = append(sections[index].Rows, item)
	}
	if err = employeeRows.Err(); err != nil {
		employeeRows.Close()
		return nil, nil, err
	}
	employeeRows.Close()
	return rows, sections, nil
}

func (a *App) loadRPExportData(ctx context.Context, from, to string) ([]exportSummaryRow, []employeeExportSection, error) {
	relevant := `SELECT planned.*,
		sum(efficiency_plan) OVER () AS total_efficiency_plan,
		sum(efficiency_plan) OVER (PARTITION BY owner_user_id) AS owner_efficiency_plan
		FROM (SELECT rp.*,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username) AS owner_name,
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),
		(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0) AS efficiency_plan
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.role='employee' AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id WHERE rp.report_date BETWEEN $1 AND $2) planned`
	summary, err := a.db.Query(ctx, `WITH relevant_reports AS (`+relevant+`), hc AS (
		SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
		SELECT COALESCE(max(rr.debtster_department_id),0),max(rr.office_name_snapshot),max(rr.open_vacancies),sum(rr.invited_candidates),sum(rr.interviewed_candidates),
		sum(rr.interns),max(rr.planned_reserve),sum(rr.reserve_candidates),sum(COALESCE(hc.n,0)),
		sum(rp.efficiency_plan),max(rp.total_efficiency_plan),
		COALESCE(string_agg(DISTINCT rp.owner_name,', ' ORDER BY rp.owner_name) FILTER (WHERE rr.invited_candidates>0 OR rr.interviewed_candidates>0 OR rr.interns>0 OR rr.reserve_candidates>0 OR COALESCE(hc.n,0)>0),'')
		FROM relevant_reports rp JOIN report_rows rr ON rr.report_id=rp.id LEFT JOIN hc ON hc.report_row_id=rr.id
		GROUP BY COALESCE(rr.debtster_department_id::text,rr.office_id::text)
		ORDER BY min(rr.office_sort_order_snapshot),max(rr.office_name_snapshot)`, from, to)
	if err != nil {
		return nil, nil, err
	}
	rows := []exportSummaryRow{}
	for summary.Next() {
		var item exportSummaryRow
		if err = summary.Scan(&item.DebtsterDepartmentID, &item.Office, &item.OpenVacancies, &item.Invited, &item.Interviewed, &item.Interns, &item.PlannedReserve, &item.Reserve, &item.Hired, &item.Plan, &item.TotalPlan, &item.Responsible); err != nil {
			summary.Close()
			return nil, nil, err
		}
		rows = append(rows, item)
	}
	if err = summary.Err(); err != nil {
		summary.Close()
		return nil, nil, err
	}
	summary.Close()
	employeeRows, err := a.db.Query(ctx, `WITH relevant_reports AS (`+relevant+`), hc AS (
		SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
		SELECT rp.owner_user_id::text,rp.owner_name,COALESCE(max(rr.debtster_department_id),0),max(rr.office_name_snapshot),max(rr.open_vacancies),
		sum(rr.invited_candidates),sum(rr.interviewed_candidates),sum(rr.interns),max(rr.planned_reserve),
		sum(rr.reserve_candidates),sum(COALESCE(hc.n,0)),sum(rp.efficiency_plan),max(rp.owner_efficiency_plan)
		FROM relevant_reports rp JOIN report_rows rr ON rr.report_id=rp.id LEFT JOIN hc ON hc.report_row_id=rr.id
		GROUP BY rp.owner_user_id,rp.owner_name,COALESCE(rr.debtster_department_id::text,rr.office_id::text)
		ORDER BY rp.owner_name,min(rr.office_sort_order_snapshot),max(rr.office_name_snapshot)`, from, to)
	if err != nil {
		return nil, nil, err
	}
	sections := []employeeExportSection{}
	indexes := map[string]int{}
	for employeeRows.Next() {
		var employeeID, employeeName string
		var item exportSummaryRow
		if err = employeeRows.Scan(&employeeID, &employeeName, &item.DebtsterDepartmentID, &item.Office, &item.OpenVacancies, &item.Invited, &item.Interviewed, &item.Interns, &item.PlannedReserve, &item.Reserve, &item.Hired, &item.Plan, &item.TotalPlan); err != nil {
			employeeRows.Close()
			return nil, nil, err
		}
		if item.Invited > 0 || item.Interviewed > 0 || item.Interns > 0 || item.Reserve > 0 || item.Hired > 0 {
			item.Responsible = employeeName
		}
		index, exists := indexes[employeeID]
		if !exists {
			index = len(sections)
			indexes[employeeID] = index
			sections = append(sections, employeeExportSection{ID: employeeID, Name: employeeName})
		}
		sections[index].Rows = append(sections[index].Rows, item)
	}
	if err = employeeRows.Err(); err != nil {
		employeeRows.Close()
		return nil, nil, err
	}
	employeeRows.Close()
	return rows, sections, nil
}

func newExportStyles(f *excelize.File) exportStyles {
	borders := []excelize.Border{{Type: "left", Color: "000000", Style: 1}, {Type: "right", Color: "000000", Style: 1}, {Type: "top", Color: "000000", Style: 1}, {Type: "bottom", Color: "000000", Style: 1}}
	header, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Bold: true}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "center", Horizontal: "center"}, Border: borders})
	body, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11}, Border: borders})
	total, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Bold: true}, Border: borders})
	title, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 13, Bold: true}, Alignment: &excelize.Alignment{Vertical: "center"}})
	efficiencyBody, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11}, Border: borders, NumFmt: 2})
	efficiencyTotal, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Bold: true}, Border: borders, NumFmt: 2})
	return exportStyles{header: header, body: body, total: total, employeeTitle: title, efficiencyBody: efficiencyBody, efficiencyTotal: efficiencyTotal}
}

func writeExportSummarySheet(f *excelize.File, kind exportKindConfig, rows []exportSummaryRow, sections []employeeExportSection, styles exportStyles) {
	headers := []string{kind.Unit, "Количество открытых вакансий", "Количество приглашенных кандидатов", "Количество прошедших собеседование", "Количество кандидатов на стажировке", "Планируемый резерв", "Количество кандидатов в резерве", "Количество принятых работников", "Ответственный работник", "Эффективность работников, %"}
	writeTable := func(startRow int, tableRows []exportSummaryRow, responsibleTotal string) int {
		for column, header := range headers {
			cell, _ := excelize.CoordinatesToCellName(column+1, startRow)
			f.SetCellValue(kind.Sheet, cell, header)
		}
		f.SetCellStyle(kind.Sheet, fmt.Sprintf("A%d", startRow), fmt.Sprintf("J%d", startRow), styles.header)
		f.SetRowHeight(kind.Sheet, startRow, 60)
		total := exportSummaryRow{}
		if len(tableRows) > 0 {
			total.TotalPlan = tableRows[0].TotalPlan
		}
		for index, item := range tableRows {
			row := startRow + index + 1
			values := []any{item.Office, item.OpenVacancies, item.Invited, item.Interviewed, item.Interns, item.PlannedReserve, item.Reserve, item.Hired, item.Responsible, Efficiency(item.Interviewed, item.Plan)}
			for column, value := range values {
				cell, _ := excelize.CoordinatesToCellName(column+1, row)
				f.SetCellValue(kind.Sheet, cell, value)
			}
			total.OpenVacancies += item.OpenVacancies
			total.Invited += item.Invited
			total.Interviewed += item.Interviewed
			total.Interns += item.Interns
			total.PlannedReserve += item.PlannedReserve
			total.Reserve += item.Reserve
			total.Hired += item.Hired
		}
		if len(tableRows) > 0 {
			f.SetCellStyle(kind.Sheet, fmt.Sprintf("A%d", startRow+1), fmt.Sprintf("J%d", startRow+len(tableRows)), styles.body)
			f.SetCellStyle(kind.Sheet, fmt.Sprintf("J%d", startRow+1), fmt.Sprintf("J%d", startRow+len(tableRows)), styles.efficiencyBody)
		}
		totalRow := startRow + len(tableRows) + 1
		values := []any{"ИТОГО:", total.OpenVacancies, total.Invited, total.Interviewed, total.Interns, total.PlannedReserve, total.Reserve, total.Hired, responsibleTotal, Efficiency(total.Interviewed, total.TotalPlan)}
		for column, value := range values {
			cell, _ := excelize.CoordinatesToCellName(column+1, totalRow)
			f.SetCellValue(kind.Sheet, cell, value)
		}
		f.SetCellStyle(kind.Sheet, fmt.Sprintf("A%d", totalRow), fmt.Sprintf("J%d", totalRow), styles.total)
		f.SetCellStyle(kind.Sheet, fmt.Sprintf("J%d", totalRow), fmt.Sprintf("J%d", totalRow), styles.efficiencyTotal)
		return totalRow
	}

	lastRow := writeTable(1, rows, "")
	for _, section := range sections {
		titleRow := lastRow + 2
		f.SetCellValue(kind.Sheet, fmt.Sprintf("A%d", titleRow), section.Name)
		_ = f.MergeCell(kind.Sheet, fmt.Sprintf("A%d", titleRow), fmt.Sprintf("J%d", titleRow))
		f.SetCellStyle(kind.Sheet, fmt.Sprintf("A%d", titleRow), fmt.Sprintf("J%d", titleRow), styles.employeeTitle)
		f.SetRowHeight(kind.Sheet, titleRow, 24)
		lastRow = writeTable(titleRow+1, section.Rows, "")
	}
	widths := map[string]float64{"A": 27.57, "B": 17.29, "C": 15.71, "D": 18.71, "E": 14.86, "F": 16.43, "G": 15.57, "H": 13, "I": 14.86, "J": 15.43}
	for column, width := range widths {
		f.SetColWidth(kind.Sheet, column, column, width)
	}
}

func (a *App) writeExportDetailSheets(ctx context.Context, f *excelize.File, kind exportKindConfig, from, to string, styles exportStyles) error {
	hiredSheet := "Принятые " + kind.Sheet
	_, _ = f.NewSheet(hiredSheet)
	for index, header := range []string{kind.Unit, "Должность", "Сотрудник которого приняли", "Ответственный"} {
		cell, _ := excelize.CoordinatesToCellName(index+1, 1)
		f.SetCellValue(hiredSheet, cell, header)
	}
	f.SetCellStyle(hiredSheet, "A1", "D1", styles.header)
	f.SetRowHeight(hiredSheet, 1, 34)
	f.SetColWidth(hiredSheet, "A", "A", 30)
	f.SetColWidth(hiredSheet, "B", "B", 28)
	f.SetColWidth(hiredSheet, "C", "D", 34)
	_ = f.SetPanes(hiredSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = f.AutoFilter(hiredSheet, "A1:D1", nil)

	var hiresQuery string
	if kind.Kind == "rp" {
		hiresQuery = `SELECT rr.office_name_snapshot,hw.position,hw.full_name,
			COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
			FROM hired_workers hw JOIN report_rows rr ON rr.id=hw.report_row_id JOIN reports rp ON rp.id=rr.report_id
			JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system LEFT JOIN employees e ON e.id=u.employee_id
			WHERE rp.report_date BETWEEN $1 AND $2
			ORDER BY rr.office_sort_order_snapshot,e.last_name,e.first_name,e.middle_name,hw.created_at`
	} else {
		hiresQuery = fmt.Sprintf(`SELECT COALESCE(NULLIF(rr.%s,''),o.name),hw.position,hw.full_name,
			COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
			FROM %s hw JOIN %s rr ON rr.id=hw.report_row_id JOIN %s rp ON rp.id=rr.report_id
			JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system LEFT JOIN employees e ON e.id=u.employee_id
			JOIN %s o ON o.id=rr.%s WHERE rp.report_date BETWEEN $1 AND $2
			ORDER BY COALESCE(NULLIF(rr.%s,0),o.sort_order),e.last_name,e.first_name,e.middle_name,hw.created_at`,
			kind.NameSnapshot, kind.Hired, kind.Rows, kind.Reports, kind.Offices, kind.OfficeID, kind.SortSnapshot)
	}
	hires, err := a.db.Query(ctx, hiresQuery, from, to)
	if err != nil {
		return err
	}
	row := 2
	for hires.Next() {
		var office, position, hired, responsible string
		if err = hires.Scan(&office, &position, &hired, &responsible); err != nil {
			hires.Close()
			return err
		}
		for column, value := range []string{office, position, hired, responsible} {
			cell, _ := excelize.CoordinatesToCellName(column+1, row)
			f.SetCellValue(hiredSheet, cell, value)
		}
		row++
	}
	if err = hires.Err(); err != nil {
		hires.Close()
		return err
	}
	hires.Close()
	if row > 2 {
		f.SetCellStyle(hiredSheet, "A2", fmt.Sprintf("D%d", row-1), styles.body)
	}

	peopleSheet := "Списки ФИО " + kind.Sheet
	_, _ = f.NewSheet(peopleSheet)
	for index, header := range []string{"Дата", kind.Unit, "Показатель", "ФИО", "Ответственный"} {
		cell, _ := excelize.CoordinatesToCellName(index+1, 1)
		f.SetCellValue(peopleSheet, cell, header)
	}
	f.SetCellStyle(peopleSheet, "A1", "E1", styles.header)
	f.SetRowHeight(peopleSheet, 1, 34)
	f.SetColWidth(peopleSheet, "A", "A", 14)
	f.SetColWidth(peopleSheet, "B", "B", 30)
	f.SetColWidth(peopleSheet, "C", "C", 38)
	f.SetColWidth(peopleSheet, "D", "E", 34)
	_ = f.SetPanes(peopleSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = f.AutoFilter(peopleSheet, "A1:E1", nil)

	var peopleQuery string
	if kind.Kind == "rp" {
		peopleQuery = `SELECT to_char(rp.report_date,'DD.MM.YYYY'),rr.office_name_snapshot,p.category,p.full_name,
			COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
			FROM report_row_people p JOIN report_rows rr ON rr.id=p.report_row_id JOIN reports rp ON rp.id=rr.report_id
			JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system LEFT JOIN employees e ON e.id=u.employee_id
			WHERE rp.report_date BETWEEN $1 AND $2 AND p.category<>'dismissed_workers'
			ORDER BY rp.report_date,rr.office_sort_order_snapshot,p.category,e.last_name,e.first_name,p.created_at,p.id`
	} else {
		peopleQuery = fmt.Sprintf(`SELECT to_char(rp.report_date,'DD.MM.YYYY'),COALESCE(NULLIF(rr.%s,''),o.name),p.category,p.full_name,
			COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
			FROM %s p JOIN %s rr ON rr.id=p.report_row_id JOIN %s rp ON rp.id=rr.report_id
			JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system LEFT JOIN employees e ON e.id=u.employee_id
			JOIN %s o ON o.id=rr.%s WHERE rp.report_date BETWEEN $1 AND $2 AND p.category<>'dismissed_workers'
			ORDER BY rp.report_date,COALESCE(NULLIF(rr.%s,0),o.sort_order),p.category,e.last_name,e.first_name,p.created_at,p.id`,
			kind.NameSnapshot, kind.People, kind.Rows, kind.Reports, kind.Offices, kind.OfficeID, kind.SortSnapshot)
	}
	people, err := a.db.Query(ctx, peopleQuery, from, to)
	if err != nil {
		return err
	}
	labels := map[string]string{"invited_candidates": "Приглашённые кандидаты", "interviewed_candidates": "Прошедшие собеседование", "interns": "Кандидаты на стажировке", "reserve_candidates": "Кандидаты в резерве"}
	row = 2
	for people.Next() {
		var date, office, category, fullName, responsible string
		if err = people.Scan(&date, &office, &category, &fullName, &responsible); err != nil {
			people.Close()
			return err
		}
		for column, value := range []string{date, office, labels[category], fullName, responsible} {
			cell, _ := excelize.CoordinatesToCellName(column+1, row)
			f.SetCellValue(peopleSheet, cell, value)
		}
		row++
	}
	if err = people.Err(); err != nil {
		people.Close()
		return err
	}
	people.Close()
	if row > 2 {
		f.SetCellStyle(peopleSheet, "A2", fmt.Sprintf("E%d", row-1), styles.body)
	}
	return nil
}
