package app

import (
	"fmt"
	"net/http"

	"github.com/xuri/excelize/v2"
)

type exportSummaryRow struct {
	Office                                                                                                 string
	OpenVacancies, InvitationPlan, Invited, InterviewPlan, Interviewed, Interns, Reserve, Hired, Dismissed int
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
	q, err := a.db.Query(r.Context(), `WITH latest_vacancy AS (
		SELECT DISTINCT ON (office_id) office_id,open_vacancies FROM daily_office_shared
		WHERE report_date BETWEEN $1 AND $2 ORDER BY office_id,report_date DESC,updated_at DESC
	), hc AS (SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
	SELECT o.name,COALESCE(lv.open_vacancies,0),COALESCE(sum(rr.invitation_threshold),0),
		COALESCE(sum(rr.invited_candidates),0),COALESCE(sum(rr.interview_plan),0),
		COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),
		COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(hc.n),0),COALESCE(sum(rr.dismissed_workers),0)
	FROM offices o LEFT JOIN latest_vacancy lv ON lv.office_id=o.id
	LEFT JOIN reports rp ON rp.report_date BETWEEN $1 AND $2
	LEFT JOIN users u ON u.id=rp.owner_user_id AND NOT u.system
	LEFT JOIN report_rows rr ON rr.report_id=rp.id AND rr.office_id=o.id AND u.id IS NOT NULL
	LEFT JOIN hc ON hc.report_row_id=rr.id WHERE o.active
	GROUP BY o.id,o.name,o.sort_order,lv.open_vacancies ORDER BY o.sort_order,o.name`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	rows := []exportSummaryRow{}
	for q.Next() {
		var x exportSummaryRow
		if err = q.Scan(&x.Office, &x.OpenVacancies, &x.InvitationPlan, &x.Invited, &x.InterviewPlan, &x.Interviewed, &x.Interns, &x.Reserve, &x.Hired, &x.Dismissed); err != nil {
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
	summary := "Сводный отчёт"
	f.SetSheetName("Sheet1", summary)
	headers := []string{"РП", "Количество открытых вакансий", "План приглашённых кандидатов", "Количество приглашённых кандидатов", "План на прошедших собеседование", "Количество прошедших собеседование", "Количество кандидатов на стажировке", "Количество кандидатов в резерве", "Количество принятых работников", "Количество уволенных работников", "Эффективность работников, %"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(summary, cell, h)
	}
	total := exportSummaryRow{Office: "ИТОГО"}
	for i, x := range rows {
		values := []any{x.Office, x.OpenVacancies, x.InvitationPlan, x.Invited, x.InterviewPlan, x.Interviewed, x.Interns, x.Reserve, x.Hired, x.Dismissed, Efficiency(x.Interns, x.InterviewPlan)}
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
	values := []any{total.Office, total.OpenVacancies, total.InvitationPlan, total.Invited, total.InterviewPlan, total.Interviewed, total.Interns, total.Reserve, total.Hired, total.Dismissed, Efficiency(total.Interns, total.InterviewPlan)}
	for col, v := range values {
		cell, _ := excelize.CoordinatesToCellName(col+1, totalRow)
		f.SetCellValue(summary, cell, v)
	}
	headerStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"2563EB"}}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "center", Horizontal: "center"}, Border: []excelize.Border{{Type: "bottom", Color: "1D4ED8", Style: 1}}})
	totalStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "17365D"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"E7F0FD"}}, Border: []excelize.Border{{Type: "top", Color: "8EABD3", Style: 2}}})
	percentStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 2})
	f.SetCellStyle(summary, "A1", "K1", headerStyle)
	f.SetCellStyle(summary, fmt.Sprintf("A%d", totalRow), fmt.Sprintf("K%d", totalRow), totalStyle)
	if len(rows) > 0 {
		f.SetCellStyle(summary, "K2", fmt.Sprintf("K%d", totalRow), percentStyle)
	}
	f.SetRowHeight(summary, 1, 46)
	f.SetColWidth(summary, "A", "A", 30)
	f.SetColWidth(summary, "B", "K", 18)
	_ = f.SetPanes(summary, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = f.AutoFilter(summary, "A1:K1", nil)
	detail := "Принятые сотрудники"
	_, _ = f.NewSheet(detail)
	detailHeaders := []string{"РП", "Сотрудник которого приняли", "Ответственный"}
	for i, h := range detailHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(detail, cell, h)
	}
	f.SetCellStyle(detail, "A1", "C1", headerStyle)
	f.SetRowHeight(detail, 1, 34)
	f.SetColWidth(detail, "A", "A", 30)
	f.SetColWidth(detail, "B", "C", 34)
	_ = f.SetPanes(detail, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = f.AutoFilter(detail, "A1:C1", nil)
	hires, err := a.db.Query(r.Context(), `SELECT o.name,hw.full_name,concat_ws(' ',e.last_name,e.first_name,e.middle_name) FROM hired_workers hw JOIN report_rows rr ON rr.id=hw.report_row_id JOIN reports rp ON rp.id=rr.report_id JOIN users u ON u.id=rp.owner_user_id AND NOT u.system LEFT JOIN employees e ON e.id=u.employee_id JOIN offices o ON o.id=rr.office_id WHERE rp.report_date BETWEEN $1 AND $2 ORDER BY o.sort_order,e.last_name,e.first_name,hw.created_at`, from, to)
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
		for col, v := range []string{office, hired, responsible} {
			cell, _ := excelize.CoordinatesToCellName(col+1, rowNumber)
			f.SetCellValue(detail, cell, v)
		}
		rowNumber++
	}
	if err = hires.Err(); err != nil {
		serverError(w, err)
		return
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
