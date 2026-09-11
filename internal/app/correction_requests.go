package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type correctionRowChange struct {
	RowID string `json:"rowId"`
	UnitName string `json:"unitName"`
	Before rowInput `json:"before"`
	After rowInput `json:"after"`
}

type correctionRequest struct {
	ID string `json:"id"`
	ReportDate string `json:"reportDate"`
	ReportType string `json:"reportType"`
	UserID string `json:"userId"`
	EmployeeName string `json:"employeeName"`
	Username string `json:"username"`
	Note string `json:"note"`
	Changes []correctionRowChange `json:"changes"`
	Status string `json:"status"`
	ReviewNote string `json:"reviewNote"`
	ReviewerName string `json:"reviewerName"`
	CreatedAt time.Time `json:"createdAt"`
	ReviewedAt *time.Time `json:"reviewedAt,omitempty"`
}

type requestedCorrectionRow struct { RowID string `json:"rowId"`; Values rowInput `json:"values"` }
type createCorrectionRequestInput struct {
	ReportDate string `json:"reportDate"`
	ReportType string `json:"reportType"`
	Note string `json:"note"`
	Rows []requestedCorrectionRow `json:"rows"`
}
type reviewCorrectionRequestInput struct { Action string `json:"action"`; ReviewNote string `json:"reviewNote"` }

func validateCorrectionRequestInput(input createCorrectionRequestInput) string {
	if msg := validatePastReportDate(strings.TrimSpace(input.ReportDate)); msg != "" { return msg }
	if input.ReportType != "rp" && input.ReportType != "main_office" { return "Выберите тип отчёта" }
	n := len([]rune(strings.TrimSpace(input.Note)))
	if n < 5 { return "Опишите причину запроса минимум в 5 символах" }
	if n > 1000 { return "Примечание не должно превышать 1000 символов" }
	if len(input.Rows) == 0 { return "Добавьте хотя бы одно изменение" }
	return ""
}

func normalizeCorrectionValues(in rowInput) rowInput {
	in.People = cleanPeople(in.People)
	for category := range peopleCategories { if _, ok := in.People[category]; !ok { in.People[category] = []string{} } }
	in.InvitedCandidates = len(in.People["invited_candidates"])
	in.InterviewedCandidates = len(in.People["interviewed_candidates"])
	in.Interns = len(in.People["interns"])
	in.ReserveCandidates = len(in.People["reserve_candidates"])
	in.DismissedWorkers = len(in.People["dismissed_workers"])
	in.HiredWorkers = cleanHiredWorkers(in.HiredWorkers)
	in.ResponsibleIDs = []string{}
	return in
}

func (a *App) correctionRequests(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	q, err := a.db.Query(r.Context(), `SELECT cr.id,cr.report_date::text,cr.report_type,cr.user_id::text,
		COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username),u.username,
		cr.note,cr.changes,cr.status,cr.review_note,
		COALESCE(NULLIF(trim(concat_ws(' ',re.last_name,re.first_name,re.middle_name)),''),ru.username,''),cr.created_at,cr.reviewed_at
		FROM correction_requests cr JOIN users u ON u.id=cr.user_id LEFT JOIN employees e ON e.id=u.employee_id
		LEFT JOIN users ru ON ru.id=cr.reviewed_by_user_id LEFT JOIN employees re ON re.id=ru.employee_id
		WHERE ($1::boolean OR cr.user_id=$2) ORDER BY CASE WHEN cr.status='pending' THEN 0 ELSE 1 END,cr.created_at DESC LIMIT 500`, isManager(claims), claims.UserID)
	if err != nil { serverError(w, err); return }
	defer q.Close()
	items := []correctionRequest{}
	for q.Next() {
		var item correctionRequest; var changes []byte
		if err = q.Scan(&item.ID,&item.ReportDate,&item.ReportType,&item.UserID,&item.EmployeeName,&item.Username,&item.Note,&changes,&item.Status,&item.ReviewNote,&item.ReviewerName,&item.CreatedAt,&item.ReviewedAt); err != nil { serverError(w, err); return }
		if err = json.Unmarshal(changes, &item.Changes); err != nil { serverError(w, err); return }
		items = append(items, item)
	}
	if err = q.Err(); err != nil { serverError(w, err); return }
	jsonOut(w, http.StatusOK, items)
}

func (a *App) createCorrectionRequest(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	if claims.Role != "employee" { problem(w, 403, "Запрос на корректировку может отправить только сотрудник"); return }
	var input createCorrectionRequestInput
	if !decode(w, r, &input) { return }
	input.ReportDate, input.ReportType, input.Note = strings.TrimSpace(input.ReportDate), strings.TrimSpace(input.ReportType), strings.TrimSpace(input.Note)
	if msg := validateCorrectionRequestInput(input); msg != "" { problem(w, 422, msg); return }
	ctx := r.Context(); tx, err := a.db.Begin(ctx)
	if err != nil { serverError(w, err); return }; defer tx.Rollback(ctx)
	changes := make([]correctionRowChange,0,len(input.Rows)); seen := map[string]bool{}
	for _, requested := range input.Rows {
		requested.RowID = strings.TrimSpace(requested.RowID)
		if requested.RowID == "" || seen[requested.RowID] { problem(w,422,"В запросе есть некорректные строки"); return }; seen[requested.RowID] = true
		before, unitName, snapshotErr := correctionSnapshot(ctx,tx,input.ReportType,requested.RowID,claims.UserID,input.ReportDate)
		if snapshotErr != nil { problem(w,422,"Строка отчёта не найдена или не была назначена вам на выбранную дату"); return }
		after := normalizeCorrectionValues(requested.Values)
		if hasNegative(after) { problem(w,422,"Числовые значения не могут быть отрицательными"); return }
		beforeJSON,_ := json.Marshal(before); afterJSON,_ := json.Marshal(after)
		if !bytes.Equal(beforeJSON,afterJSON) { changes = append(changes,correctionRowChange{requested.RowID,unitName,before,after}) }
	}
	if len(changes)==0 { problem(w,422,"Измените хотя бы одно значение перед отправкой"); return }
	changesJSON,err := json.Marshal(changes); if err != nil { serverError(w,err); return }
	var id string
	err = tx.QueryRow(ctx,`INSERT INTO correction_requests(report_date,report_type,user_id,note,changes) VALUES($1,$2,$3,$4,$5::jsonb)
		ON CONFLICT(user_id,report_date,report_type) WHERE status='pending' DO NOTHING RETURNING id`,input.ReportDate,input.ReportType,claims.UserID,input.Note,changesJSON).Scan(&id)
	if err != nil {
		var pending bool; _ = tx.QueryRow(ctx,`SELECT EXISTS(SELECT 1 FROM correction_requests WHERE user_id=$1 AND report_date=$2 AND report_type=$3 AND status='pending')`,claims.UserID,input.ReportDate,input.ReportType).Scan(&pending)
		if pending { problem(w,409,"Запрос по этому отчёту уже ожидает рассмотрения"); return }; serverError(w,err); return
	}
	if err=tx.Commit(ctx); err != nil { serverError(w,err); return }
	a.log(ctx,"correction_request.created","correction_request",id)
	jsonOut(w,201,map[string]any{"id":id,"status":"pending"})
}

func correctionSnapshot(ctx context.Context,tx pgx.Tx,reportType,rowID,userID,reportDate string)(rowInput,string,error){
	var in rowInput; var unitName string; var err error
	if reportType=="rp" {
		err=tx.QueryRow(ctx,`SELECT rr.open_vacancies,rr.planned_reserve,rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,rr.dismissed_workers,COALESCE(NULLIF(rr.office_name_snapshot,''),NULLIF(rr.debtster_department_name,''),'РП') FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id WHERE rr.id=$1 AND rp.owner_user_id=$2 AND rp.report_date=$3 AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$2 AND a.report_type='rp' AND a.unit_id=COALESCE(rr.debtster_department_id::text,rr.office_id::text) AND a.assigned_from<=rp.report_date AND (a.assigned_to IS NULL OR a.assigned_to>=rp.report_date))`,rowID,userID,reportDate).Scan(&in.OpenVacancies,&in.PlannedReserve,&in.InvitedCandidates,&in.InterviewedCandidates,&in.Interns,&in.ReserveCandidates,&in.DismissedWorkers,&unitName)
	} else {
		err=tx.QueryRow(ctx,`SELECT COALESCE(ds.open_vacancies,0),COALESCE(ds.planned_reserve,0),rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,rr.dismissed_workers,COALESCE(NULLIF(rr.main_office_name_snapshot,''),mo.name) FROM main_office_report_rows rr JOIN main_office_reports rp ON rp.id=rr.report_id JOIN main_offices mo ON mo.id=rr.main_office_id LEFT JOIN main_office_daily_shared ds ON ds.report_date=rp.report_date AND ds.main_office_id=rr.main_office_id WHERE rr.id=$1 AND rp.owner_user_id=$2 AND rp.report_date=$3 AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.user_id=$2 AND a.report_type='main_office' AND a.unit_id=rr.main_office_id::text AND a.assigned_from<=rp.report_date AND (a.assigned_to IS NULL OR a.assigned_to>=rp.report_date))`,rowID,userID,reportDate).Scan(&in.OpenVacancies,&in.PlannedReserve,&in.InvitedCandidates,&in.InterviewedCandidates,&in.Interns,&in.ReserveCandidates,&in.DismissedWorkers,&unitName)
	}
	if err != nil { return in,"",err }
	in.People=map[string][]string{}; peopleTable,hiredTable:="report_row_people","hired_workers"
	if reportType=="main_office" { peopleTable,hiredTable="main_office_report_row_people","main_office_hired_workers" }
	rows,err:=tx.Query(ctx,`SELECT category,full_name FROM `+peopleTable+` WHERE report_row_id=$1 ORDER BY created_at,id`,rowID); if err!=nil{return in,"",err}
	for rows.Next(){var category,name string;if err=rows.Scan(&category,&name);err!=nil{rows.Close();return in,"",err};in.People[category]=append(in.People[category],name)};rows.Close()
	hires,err:=tx.Query(ctx,`SELECT full_name,position FROM `+hiredTable+` WHERE report_row_id=$1 ORDER BY created_at,id`,rowID);if err!=nil{return in,"",err}
	for hires.Next(){var worker hiredWorker;if err=hires.Scan(&worker.FullName,&worker.Position);err!=nil{hires.Close();return in,"",err};in.HiredWorkers=append(in.HiredWorkers,worker)};hires.Close()
	return normalizeCorrectionValues(in),unitName,nil
}

func (a *App) reviewCorrectionRequest(w http.ResponseWriter,r *http.Request){
	claims,ok:=requireManager(w,r);if !ok{return};var input reviewCorrectionRequestInput;if !decode(w,r,&input){return}
	input.Action,input.ReviewNote=strings.TrimSpace(input.Action),strings.TrimSpace(input.ReviewNote)
	if input.Action!="approve"&&input.Action!="reject"{problem(w,422,"Неизвестное действие с запросом");return}
	if len([]rune(input.ReviewNote))>1000{problem(w,422,"Комментарий не должен превышать 1000 символов");return}
	if input.Action=="reject"&&len([]rune(input.ReviewNote))<3{problem(w,422,"Укажите причину отказа минимум в 3 символах");return}
	ctx:=r.Context();tx,err:=a.db.Begin(ctx);if err!=nil{serverError(w,err);return};defer tx.Rollback(ctx)
	var userID,reportDate,reportType,status string;var changesJSON []byte
	if err=tx.QueryRow(ctx,`SELECT user_id::text,report_date::text,report_type,status,changes FROM correction_requests WHERE id=$1 FOR UPDATE`,r.PathValue("id")).Scan(&userID,&reportDate,&reportType,&status,&changesJSON);err!=nil{problem(w,404,"Запрос не найден");return}
	if status!="pending"{problem(w,409,"Запрос уже рассмотрен");return};newStatus:="rejected"
	if input.Action=="approve"{var changes []correctionRowChange;if err=json.Unmarshal(changesJSON,&changes);err!=nil{serverError(w,err);return};plans,planErr:=a.candidatePlanTotals(ctx,reportDate,userID,reportType);if planErr!=nil{serverError(w,planErr);return};for _,change:=range changes{if err=applyCorrection(ctx,tx,reportType,userID,reportDate,claims.UserID,change,plans.Hired);err!=nil{problem(w,409,"Не удалось применить изменения: отчёт был изменён или удалён");return}};newStatus="approved"}
	if _,err=tx.Exec(ctx,`UPDATE correction_requests SET status=$2,review_note=$3,reviewed_by_user_id=$4,reviewed_at=now(),updated_at=now() WHERE id=$1`,r.PathValue("id"),newStatus,input.ReviewNote,claims.UserID);err!=nil{serverError(w,err);return}
	if _,err=tx.Exec(ctx,`INSERT INTO audit_log(actor,action,entity_type,entity_id,details) VALUES($1,$2,'correction_request',$3,jsonb_build_object('date',$4::text,'userId',$5::text,'reportType',$6::text))`,claims.Username,"correction_request."+newStatus,r.PathValue("id"),reportDate,userID,reportType);err!=nil{serverError(w,err);return}
	if err=tx.Commit(ctx);err!=nil{serverError(w,err);return};if newStatus=="approved"{a.reports.send(reportDate,map[string]any{"type":"report_updated","date":reportDate,"correctionRequestId":r.PathValue("id")})};jsonOut(w,200,map[string]any{"ok":true,"status":newStatus})
}

func applyCorrection(ctx context.Context,tx pgx.Tx,reportType,userID,reportDate,reviewerID string,change correctionRowChange,hiringPlan int)error{
	in:=normalizeCorrectionValues(change.After);peopleTable,hiredTable:="report_row_people","hired_workers"
	if reportType=="rp"{var unitID string;if err:=tx.QueryRow(ctx,`SELECT COALESCE(rr.debtster_department_id::text,rr.office_id::text) FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id WHERE rr.id=$1 AND rp.owner_user_id=$2 AND rp.report_date=$3 FOR UPDATE`,change.RowID,userID,reportDate).Scan(&unitID);err!=nil{return err};if _,err:=tx.Exec(ctx,`UPDATE report_rows target SET open_vacancies=$2,planned_reserve=$3,updated_at=now() FROM reports target_report,report_rows source WHERE source.id=$1 AND target.report_id=target_report.id AND target_report.report_date=$4 AND ((source.debtster_department_id IS NOT NULL AND target.debtster_department_id=source.debtster_department_id) OR (source.debtster_department_id IS NULL AND target.debtster_department_id IS NULL AND target.office_id=source.office_id))`,change.RowID,in.OpenVacancies,in.PlannedReserve,reportDate);err!=nil{return err};if _,err:=tx.Exec(ctx,`UPDATE report_rows SET invited_candidates=$2,interviewed_candidates=$3,interns=$4,reserve_candidates=$5,dismissed_workers=$6,efficiency=$7,updated_at=now() WHERE id=$1`,change.RowID,in.InvitedCandidates,in.InterviewedCandidates,in.Interns,in.ReserveCandidates,in.DismissedWorkers,Efficiency(len(in.HiredWorkers),hiringPlan));err!=nil{return err}
	}else{peopleTable,hiredTable="main_office_report_row_people","main_office_hired_workers";var unitID string;if err:=tx.QueryRow(ctx,`SELECT rr.main_office_id::text FROM main_office_report_rows rr JOIN main_office_reports rp ON rp.id=rr.report_id WHERE rr.id=$1 AND rp.owner_user_id=$2 AND rp.report_date=$3 FOR UPDATE`,change.RowID,userID,reportDate).Scan(&unitID);err!=nil{return err};if _,err:=tx.Exec(ctx,`INSERT INTO main_office_daily_shared(report_date,main_office_id,open_vacancies,planned_reserve,updated_by_user_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(report_date,main_office_id) DO UPDATE SET open_vacancies=EXCLUDED.open_vacancies,planned_reserve=EXCLUDED.planned_reserve,updated_by_user_id=EXCLUDED.updated_by_user_id,updated_at=now()`,reportDate,unitID,in.OpenVacancies,in.PlannedReserve,reviewerID);err!=nil{return err};if _,err:=tx.Exec(ctx,`UPDATE main_office_report_rows SET invited_candidates=$2,interviewed_candidates=$3,interns=$4,reserve_candidates=$5,dismissed_workers=$6,updated_at=now() WHERE id=$1`,change.RowID,in.InvitedCandidates,in.InterviewedCandidates,in.Interns,in.ReserveCandidates,in.DismissedWorkers);err!=nil{return err}}
	if _,err:=tx.Exec(ctx,`DELETE FROM `+hiredTable+` WHERE report_row_id=$1`,change.RowID);err!=nil{return err};for _,worker:=range in.HiredWorkers{if _,err:=tx.Exec(ctx,`INSERT INTO `+hiredTable+`(report_row_id,full_name,position) VALUES($1,$2,$3)`,change.RowID,worker.FullName,worker.Position);err!=nil{return err}}
	if _,err:=tx.Exec(ctx,`DELETE FROM `+peopleTable+` WHERE report_row_id=$1`,change.RowID);err!=nil{return err};for category,names:=range in.People{for _,name:=range names{if _,err:=tx.Exec(ctx,`INSERT INTO `+peopleTable+`(report_row_id,category,full_name) VALUES($1,$2,$3)`,change.RowID,category,name);err!=nil{return err}}};return nil
}
