package app

import (
	"fmt"
	"net/http"

	"github.com/xuri/excelize/v2"
)

type exportSummaryRow struct {
	Office                                                                        string
	OpenVacancies, Invited, Interviewed, Interns, Reserve, Hired, Dismissed, Plan int
	Responsible                                                                   string
}

type employeeExportSection struct {
	ID, Name string
	Rows     []exportSummaryRow
}

func (a *App) exportPeriod(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if !validDate(from) || !validDate(to) || from > to {
		problem(w, 400, "Укажите корректный период")
		return
	}
	q, err := a.db.Query(r.Context(), `WITH relevant_reports AS (
		SELECT rp.*,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username) AS owner_name,
			COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0) AS efficiency_plan
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.role='employee' AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id
		WHERE rp.report_date BETWEEN $1 AND $2
	), office_scope AS (
		SELECT o.id,
		COALESCE((SELECT NULLIF(rr2.office_name_snapshot,'') FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id ORDER BY rp2.report_date,rp2.created_at LIMIT 1),o.name) AS name,
		COALESCE((SELECT NULLIF(rr2.office_sort_order_snapshot,0) FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id ORDER BY rp2.report_date,rp2.created_at LIMIT 1),o.sort_order) AS sort_order
		FROM offices o WHERE o.active OR EXISTS (SELECT 1 FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id)
	), hc AS (
		SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id
	), latest_vacancy AS (
		SELECT DISTINCT ON (ds.office_id) ds.office_id,ds.open_vacancies
		FROM daily_office_shared ds
		WHERE ds.report_date BETWEEN $1 AND $2
		ORDER BY ds.office_id,ds.report_date DESC,ds.updated_at DESC
	), activity_responsibles AS (
		SELECT rr.office_id,rp.owner_name AS responsible
		FROM relevant_reports rp
		JOIN report_rows rr ON rr.report_id=rp.id
		LEFT JOIN hc ON hc.report_row_id=rr.id
		WHERE rr.invited_candidates>0 OR rr.interviewed_candidates>0 OR rr.interns>0
			OR rr.reserve_candidates>0 OR rr.dismissed_workers>0 OR COALESCE(hc.n,0)>0
	), office_responsibles AS (
		SELECT office_id,string_agg(responsible,', ' ORDER BY responsible) AS responsible
		FROM activity_responsibles GROUP BY office_id
	)
	SELECT o.name,COALESCE(lv.open_vacancies,0),
		COALESCE(sum(rr.invited_candidates),0),
		COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),
		COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(hc.n),0),COALESCE(sum(rr.dismissed_workers),0),
		COALESCE(sum(rp.efficiency_plan) FILTER (WHERE rr.id IS NOT NULL),0),
		COALESCE(resp.responsible,'')
	FROM office_scope o LEFT JOIN latest_vacancy lv ON lv.office_id=o.id
	LEFT JOIN relevant_reports rp ON true
	LEFT JOIN report_rows rr ON rr.report_id=rp.id AND rr.office_id=o.id
	LEFT JOIN hc ON hc.report_row_id=rr.id
	LEFT JOIN office_responsibles resp ON resp.office_id=o.id
	GROUP BY o.id,o.name,o.sort_order,lv.open_vacancies,resp.responsible ORDER BY o.sort_order,o.name`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	rows := []exportSummaryRow{}
	for q.Next() {
		var x exportSummaryRow
		if err = q.Scan(&x.Office, &x.OpenVacancies, &x.Invited, &x.Interviewed, &x.Interns, &x.Reserve, &x.Hired, &x.Dismissed, &x.Plan, &x.Responsible); err != nil {
			serverError(w, err)
			return
		}
		rows = append(rows, x)
	}
	if err = q.Err(); err != nil {
		serverError(w, err)
		return
	}
	q.Close()

	employeeRows, err := a.db.Query(r.Context(), `WITH relevant_reports AS (
		SELECT rp.*,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username) AS owner_name,
			COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0) AS efficiency_plan
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.role='employee' AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id
		WHERE rp.report_date BETWEEN $1 AND $2
	), hc AS (
		SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id
	), latest_vacancy AS (
		SELECT DISTINCT ON (ds.office_id) ds.office_id,ds.open_vacancies
		FROM daily_office_shared ds
		WHERE ds.report_date BETWEEN $1 AND $2
		ORDER BY ds.office_id,ds.report_date DESC,ds.updated_at DESC
	)
	SELECT rp.owner_user_id::text,rp.owner_name,
		COALESCE(NULLIF(rr.office_name_snapshot,''),o.name),COALESCE(lv.open_vacancies,0),
		COALESCE(sum(rr.invited_candidates),0),COALESCE(sum(rr.interviewed_candidates),0),
		COALESCE(sum(rr.interns),0),COALESCE(sum(rr.reserve_candidates),0),
		COALESCE(sum(hc.n),0),COALESCE(sum(rr.dismissed_workers),0),
		COALESCE(sum(rp.efficiency_plan),0)
	FROM relevant_reports rp
	JOIN report_rows rr ON rr.report_id=rp.id
	JOIN offices o ON o.id=rr.office_id
	LEFT JOIN hc ON hc.report_row_id=rr.id
	LEFT JOIN latest_vacancy lv ON lv.office_id=rr.office_id
	GROUP BY rp.owner_user_id,rp.owner_name,o.id,
		COALESCE(NULLIF(rr.office_name_snapshot,''),o.name),
		COALESCE(NULLIF(rr.office_sort_order_snapshot,0),o.sort_order),lv.open_vacancies
	ORDER BY rp.owner_name,COALESCE(NULLIF(rr.office_sort_order_snapshot,0),o.sort_order),
		COALESCE(NULLIF(rr.office_name_snapshot,''),o.name)`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	sections := []employeeExportSection{}
	sectionIndexes := map[string]int{}
	for employeeRows.Next() {
		var employeeID, employeeName string
		var x exportSummaryRow
		if err = employeeRows.Scan(&employeeID, &employeeName, &x.Office, &x.OpenVacancies, &x.Invited, &x.Interviewed, &x.Interns, &x.Reserve, &x.Hired, &x.Dismissed, &x.Plan); err != nil {
			employeeRows.Close()
			serverError(w, err)
			return
		}
		if x.Invited > 0 || x.Interviewed > 0 || x.Interns > 0 || x.Reserve > 0 || x.Hired > 0 || x.Dismissed > 0 {
			x.Responsible = employeeName
		}
		index, exists := sectionIndexes[employeeID]
		if !exists {
			index = len(sections)
			sectionIndexes[employeeID] = index
			sections = append(sections, employeeExportSection{ID: employeeID, Name: employeeName})
		}
		sections[index].Rows = append(sections[index].Rows, x)
	}
	if err = employeeRows.Err(); err != nil {
		employeeRows.Close()
		serverError(w, err)
		return
	}
	employeeRows.Close()

	f := excelize.NewFile()
	summary := "Лист1"
	f.SetSheetName("Sheet1", summary)
	headers := []string{" РП", "Количество открытых вакансий", "Количество приглашенных кандидатов", "Количество прошедших собеседование", "Количество кандидатов на стажировке", "Количество кандидатов в резерве", "Количество принятых работников", "Количество уволенных работников", "Ответственный работник", "Эффективность работников"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(summary, cell, h)
	}
	total := exportSummaryRow{Office: "ИТОГО"}
	for i, x := range rows {
		values := []any{x.Office, x.OpenVacancies, x.Invited, x.Interviewed, x.Interns, x.Reserve, x.Hired, x.Dismissed, x.Responsible, Efficiency(x.Interviewed, x.Plan)}
		for col, v := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, i+2)
			f.SetCellValue(summary, cell, v)
		}
		total.OpenVacancies += x.OpenVacancies
		total.Invited += x.Invited
		total.Interviewed += x.Interviewed
		total.Interns += x.Interns
		total.Reserve += x.Reserve
		total.Hired += x.Hired
		total.Dismissed += x.Dismissed
		total.Plan += x.Plan
	}
	totalRow := len(rows) + 2
	values := []any{"ИТОГО:", total.OpenVacancies, total.Invited, total.Interviewed, total.Interns, total.Reserve, total.Hired, total.Dismissed, "", Efficiency(total.Interviewed, total.Plan)}
	for col, v := range values {
		cell, _ := excelize.CoordinatesToCellName(col+1, totalRow)
		f.SetCellValue(summary, cell, v)
	}
	borders := []excelize.Border{{Type: "left", Color: "000000", Style: 1}, {Type: "right", Color: "000000", Style: 1}, {Type: "top", Color: "000000", Style: 1}, {Type: "bottom", Color: "000000", Style: 1}}
	headerStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Bold: true, Color: "000000"}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "center", Horizontal: "center"}, Border: borders})
	bodyStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Color: "000000"}, Border: borders})
	totalStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Bold: true, Color: "000000"}, Border: borders})
	employeeTitleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 13, Bold: true, Color: "000000"}, Alignment: &excelize.Alignment{Vertical: "center"}})
	f.SetCellStyle(summary, "A1", "J1", headerStyle)
	if len(rows) > 0 {
		f.SetCellStyle(summary, "A2", fmt.Sprintf("J%d", totalRow-1), bodyStyle)
	}
	f.SetCellStyle(summary, fmt.Sprintf("A%d", totalRow), fmt.Sprintf("J%d", totalRow), totalStyle)
	f.SetRowHeight(summary, 1, 60)
	widths := map[string]float64{"A": 27.57, "B": 17.29, "C": 15.71, "D": 18.71, "E": 14.86, "F": 16.43, "G": 15.57, "H": 13, "I": 14.86, "J": 15.43}
	for col, width := range widths {
		f.SetColWidth(summary, col, col, width)
	}
	nextRow := totalRow + 2
	for _, section := range sections {
		f.SetCellValue(summary, fmt.Sprintf("A%d", nextRow), section.Name)
		_ = f.MergeCell(summary, fmt.Sprintf("A%d", nextRow), fmt.Sprintf("J%d", nextRow))
		f.SetCellStyle(summary, fmt.Sprintf("A%d", nextRow), fmt.Sprintf("J%d", nextRow), employeeTitleStyle)
		f.SetRowHeight(summary, nextRow, 24)
		headerRow := nextRow + 1
		for col, header := range headers {
			cell, _ := excelize.CoordinatesToCellName(col+1, headerRow)
			f.SetCellValue(summary, cell, header)
		}
		f.SetCellStyle(summary, fmt.Sprintf("A%d", headerRow), fmt.Sprintf("J%d", headerRow), headerStyle)
		f.SetRowHeight(summary, headerRow, 60)

		employeeTotal := exportSummaryRow{}
		for index, x := range section.Rows {
			rowNumber := headerRow + index + 1
			values := []any{x.Office, x.OpenVacancies, x.Invited, x.Interviewed, x.Interns, x.Reserve, x.Hired, x.Dismissed, x.Responsible, Efficiency(x.Interviewed, x.Plan)}
			for col, value := range values {
				cell, _ := excelize.CoordinatesToCellName(col+1, rowNumber)
				f.SetCellValue(summary, cell, value)
			}
			employeeTotal.OpenVacancies += x.OpenVacancies
			employeeTotal.Invited += x.Invited
			employeeTotal.Interviewed += x.Interviewed
			employeeTotal.Interns += x.Interns
			employeeTotal.Reserve += x.Reserve
			employeeTotal.Hired += x.Hired
			employeeTotal.Dismissed += x.Dismissed
			employeeTotal.Plan += x.Plan
		}
		lastDataRow := headerRow + len(section.Rows)
		if len(section.Rows) > 0 {
			f.SetCellStyle(summary, fmt.Sprintf("A%d", headerRow+1), fmt.Sprintf("J%d", lastDataRow), bodyStyle)
		}
		employeeTotalRow := lastDataRow + 1
		values := []any{"ИТОГО:", employeeTotal.OpenVacancies, employeeTotal.Invited, employeeTotal.Interviewed, employeeTotal.Interns, employeeTotal.Reserve, employeeTotal.Hired, employeeTotal.Dismissed, "", Efficiency(employeeTotal.Interviewed, employeeTotal.Plan)}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, employeeTotalRow)
			f.SetCellValue(summary, cell, value)
		}
		f.SetCellStyle(summary, fmt.Sprintf("A%d", employeeTotalRow), fmt.Sprintf("J%d", employeeTotalRow), totalStyle)
		nextRow = employeeTotalRow + 2
	}
	detail := "Принятые сотрудники"
	_, _ = f.NewSheet(detail)
	detailHeaders := []string{"РП", "Сотрудник которого приняли", "Ответственный"}
	for i, header := range detailHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(detail, cell, header)
	}
	f.SetCellStyle(detail, "A1", "C1", headerStyle)
	f.SetRowHeight(detail, 1, 34)
	f.SetColWidth(detail, "A", "A", 30)
	f.SetColWidth(detail, "B", "C", 34)
	_ = f.SetPanes(detail, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = f.AutoFilter(detail, "A1:C1", nil)

	hires, err := a.db.Query(r.Context(), `SELECT
		COALESCE(NULLIF(rr.office_name_snapshot,''),o.name),hw.full_name,
		COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
		FROM hired_workers hw
		JOIN report_rows rr ON rr.id=hw.report_row_id
		JOIN reports rp ON rp.id=rr.report_id
		JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id
		JOIN offices o ON o.id=rr.office_id
		WHERE rp.report_date BETWEEN $1 AND $2
		ORDER BY COALESCE(NULLIF(rr.office_sort_order_snapshot,0),o.sort_order),e.last_name,e.first_name,e.middle_name,hw.created_at`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer hires.Close()
	rowNumber := 2
	for hires.Next() {
		var office, hired, responsible string
		if err = hires.Scan(&office, &hired, &responsible); err != nil {
			serverError(w, err)
			return
		}
		for col, value := range []string{office, hired, responsible} {
			cell, _ := excelize.CoordinatesToCellName(col+1, rowNumber)
			f.SetCellValue(detail, cell, value)
		}
		rowNumber++
	}
	if err = hires.Err(); err != nil {
		serverError(w, err)
		return
	}
	if rowNumber > 2 {
		f.SetCellStyle(detail, "A2", fmt.Sprintf("C%d", rowNumber-1), bodyStyle)
	}
	peopleSheet := "Списки ФИО"
	_, _ = f.NewSheet(peopleSheet)
	peopleHeaders := []string{"Дата", "РП", "Показатель", "ФИО", "Ответственный"}
	for i, header := range peopleHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(peopleSheet, cell, header)
	}
	f.SetCellStyle(peopleSheet, "A1", "E1", headerStyle)
	f.SetRowHeight(peopleSheet, 1, 34)
	f.SetColWidth(peopleSheet, "A", "A", 14)
	f.SetColWidth(peopleSheet, "B", "B", 30)
	f.SetColWidth(peopleSheet, "C", "C", 38)
	f.SetColWidth(peopleSheet, "D", "E", 34)
	_ = f.SetPanes(peopleSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = f.AutoFilter(peopleSheet, "A1:E1", nil)

	peopleRows, err := a.db.Query(r.Context(), `SELECT to_char(rp.report_date,'DD.MM.YYYY'),
		COALESCE(NULLIF(rr.office_name_snapshot,''),o.name),p.category,p.full_name,
		COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
		FROM report_row_people p
		JOIN report_rows rr ON rr.id=p.report_row_id
		JOIN reports rp ON rp.id=rr.report_id
		JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id
		JOIN offices o ON o.id=rr.office_id
		WHERE rp.report_date BETWEEN $1 AND $2
		ORDER BY rp.report_date,COALESCE(NULLIF(rr.office_sort_order_snapshot,0),o.sort_order),p.category,e.last_name,e.first_name,p.created_at,p.id`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer peopleRows.Close()
	peopleRowNumber := 2
	categoryLabels := map[string]string{
		"invited_candidates":     "Приглашённые кандидаты",
		"interviewed_candidates": "Прошедшие собеседование",
		"interns":                "Кандидаты на стажировке",
		"reserve_candidates":     "Кандидаты в резерве",
		"dismissed_workers":      "Уволенные работники",
	}
	for peopleRows.Next() {
		var date, office, category, fullName, responsible string
		if err = peopleRows.Scan(&date, &office, &category, &fullName, &responsible); err != nil {
			serverError(w, err)
			return
		}
		values := []string{date, office, categoryLabels[category], fullName, responsible}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, peopleRowNumber)
			f.SetCellValue(peopleSheet, cell, value)
		}
		peopleRowNumber++
	}
	if err = peopleRows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if peopleRowNumber > 2 {
		f.SetCellStyle(peopleSheet, "A2", fmt.Sprintf("E%d", peopleRowNumber-1), bodyStyle)
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
