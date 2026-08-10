package app

import (
	"fmt"
	"net/http"

	"github.com/xuri/excelize/v2"
)

type exportSummaryRow struct {
	Office                                                                                                 string
	OpenVacancies, InvitationPlan, Invited, InterviewPlan, Interviewed, Interns, Reserve, Hired, Dismissed int
	Responsible                                                                                            string
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
		SELECT rp.* FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.role='employee' AND u.active AND NOT u.system
		WHERE rp.report_date BETWEEN $1 AND $2
	), office_scope AS (
		SELECT o.id,
		COALESCE((SELECT NULLIF(rr2.office_name_snapshot,'') FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id ORDER BY rp2.report_date,rp2.created_at LIMIT 1),o.name) AS name,
		COALESCE((SELECT NULLIF(rr2.office_sort_order_snapshot,0) FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id ORDER BY rp2.report_date,rp2.created_at LIMIT 1),o.sort_order) AS sort_order
		FROM offices o WHERE o.active OR EXISTS (SELECT 1 FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id)
	), latest_vacancy AS (
		SELECT DISTINCT ON (office_id) office_id,open_vacancies FROM daily_office_shared
		WHERE report_date BETWEEN $1 AND $2 ORDER BY office_id,report_date DESC,updated_at DESC
	), hc AS (SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
	SELECT o.name,COALESCE(lv.open_vacancies,0),COALESCE(sum(rr.invitation_threshold),0),
		COALESCE(sum(rr.invited_candidates),0),COALESCE(sum(rr.interview_plan),0),
		COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),
		COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(hc.n),0),COALESCE(sum(rr.dismissed_workers),0),
		COALESCE(string_agg(DISTINCT NULLIF(rp.owner_name_snapshot,''),', ') FILTER (WHERE rr.id IS NOT NULL),'')
	FROM office_scope o LEFT JOIN latest_vacancy lv ON lv.office_id=o.id
	LEFT JOIN relevant_reports rp ON true
	LEFT JOIN report_rows rr ON rr.report_id=rp.id AND rr.office_id=o.id
	LEFT JOIN hc ON hc.report_row_id=rr.id
	GROUP BY o.id,o.name,o.sort_order,lv.open_vacancies ORDER BY o.sort_order,o.name`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	rows := []exportSummaryRow{}
	for q.Next() {
		var x exportSummaryRow
		if err = q.Scan(&x.Office, &x.OpenVacancies, &x.InvitationPlan, &x.Invited, &x.InterviewPlan, &x.Interviewed, &x.Interns, &x.Reserve, &x.Hired, &x.Dismissed, &x.Responsible); err != nil {
			serverError(w, err)
			return
		}
		rows = append(rows, x)
	}
	if err = q.Err(); err != nil {
		serverError(w, err)
		return
	}
	f := excelize.NewFile()
	summary := "Лист1"
	f.SetSheetName("Sheet1", summary)
	headers := []string{" РП", "Количество открытых вакансий", "Минимальный порог приглашенных кандидатов", "Количество приглашенных кандидатов", "План на прошедших собеседование", "Количество прошедших собеседование", "Количество кандидатов на стажировке", "Количество кандидатов в резерве", "Количество принятых работников", "Количество уволенных работников", "Ответственный работник", "Эффективность работников"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(summary, cell, h)
	}
	total := exportSummaryRow{Office: "ИТОГО"}
	for i, x := range rows {
		values := []any{x.Office, x.OpenVacancies, x.InvitationPlan, x.Invited, x.InterviewPlan, x.Interviewed, x.Interns, x.Reserve, x.Hired, x.Dismissed, x.Responsible, Efficiency(x.Interns, x.InterviewPlan)}
		for col, v := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, i+2)
			f.SetCellValue(summary, cell, v)
		}
		total.OpenVacancies += x.OpenVacancies
		total.InvitationPlan += x.InvitationPlan
		total.Invited += x.Invited
		total.InterviewPlan += x.InterviewPlan
		total.Interviewed += x.Interviewed
		total.Interns += x.Interns
		total.Reserve += x.Reserve
		total.Hired += x.Hired
		total.Dismissed += x.Dismissed
	}
	totalRow := len(rows) + 2
	values := []any{"ИТОГО:", total.OpenVacancies, total.InvitationPlan, total.Invited, total.InterviewPlan, total.Interviewed, total.Interns, total.Reserve, total.Hired, total.Dismissed, "", Efficiency(total.Interns, total.InterviewPlan)}
	for col, v := range values {
		cell, _ := excelize.CoordinatesToCellName(col+1, totalRow)
		f.SetCellValue(summary, cell, v)
	}
	borders := []excelize.Border{{Type: "left", Color: "000000", Style: 1}, {Type: "right", Color: "000000", Style: 1}, {Type: "top", Color: "000000", Style: 1}, {Type: "bottom", Color: "000000", Style: 1}}
	headerStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Bold: true, Color: "000000"}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "center", Horizontal: "center"}, Border: borders})
	bodyStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Color: "000000"}, Border: borders})
	totalStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 11, Bold: true, Color: "000000"}, Border: borders})
	f.SetCellStyle(summary, "A1", "L1", headerStyle)
	if len(rows) > 0 {
		f.SetCellStyle(summary, "A2", fmt.Sprintf("L%d", totalRow-1), bodyStyle)
	}
	f.SetCellStyle(summary, fmt.Sprintf("A%d", totalRow), fmt.Sprintf("L%d", totalRow), totalStyle)
	f.SetRowHeight(summary, 1, 60)
	widths := map[string]float64{"A": 27.57, "B": 17.29, "C": 15.57, "D": 15.71, "E": 13, "F": 18.71, "G": 14.86, "H": 16.43, "I": 15.57, "J": 13, "K": 14.86, "L": 15.43}
	for col, width := range widths {
		f.SetColWidth(summary, col, col, width)
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
		COALESCE(NULLIF(rp.owner_name_snapshot,''),concat_ws(' ',e.last_name,e.first_name,e.middle_name))
		FROM hired_workers hw
		JOIN report_rows rr ON rr.id=hw.report_row_id
		JOIN reports rp ON rp.id=rr.report_id
		JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id
		JOIN offices o ON o.id=rr.office_id
		WHERE rp.report_date BETWEEN $1 AND $2
		ORDER BY COALESCE(NULLIF(rr.office_sort_order_snapshot,0),o.sort_order),rp.owner_name_snapshot,hw.created_at`, from, to)
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
