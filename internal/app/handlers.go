package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"
)

func (a *App) history(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if !validDate(from) || !validDate(to) {
		problem(w, 400, "Укажите период в формате YYYY-MM-DD")
		return
	}
	q, err := a.db.Query(r.Context(), `SELECT rp.id,rp.report_date::text,rp.status,COALESCE((SELECT sum(invitation_threshold) FROM report_rows WHERE report_id=rp.id),0),COALESCE((SELECT sum(invited_candidates) FROM report_rows WHERE report_id=rp.id),0),COALESCE((SELECT sum(interns) FROM report_rows WHERE report_id=rp.id),0),COALESCE((SELECT count(*) FROM hired_workers hw JOIN report_rows rr ON rr.id=hw.report_row_id WHERE rr.report_id=rp.id),0) FROM reports rp WHERE rp.report_date BETWEEN $1 AND $2 ORDER BY rp.report_date DESC`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []map[string]any{}
	for q.Next() {
		var id, d, s string
		var threshold, invited, interns, hires int
		if err = q.Scan(&id, &d, &s, &threshold, &invited, &interns, &hires); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "date": d, "status": s, "threshold": threshold, "invited": invited, "interns": interns, "hires": hires})
	}
	jsonOut(w, 200, out)
}

func (a *App) exportReports(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if !validDate(from) {
		problem(w, 400, "Некорректная дата")
		return
	}
	if to == "" {
		to = from
	}
	if !validDate(to) {
		problem(w, 400, "Некорректная дата окончания")
		return
	}
	q, err := a.db.Query(r.Context(), `SELECT rp.report_date::text,o.name,rr.open_vacancies,rr.invitation_threshold,rr.invited_candidates,rr.interview_plan,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,count(DISTINCT hw.id),rr.dismissed_workers,COALESCE(string_agg(DISTINCT concat_ws(' ',e.last_name,e.first_name,e.middle_name),', '),''),rr.efficiency FROM reports rp JOIN report_rows rr ON rr.report_id=rp.id JOIN offices o ON o.id=rr.office_id LEFT JOIN hired_workers hw ON hw.report_row_id=rr.id LEFT JOIN report_row_responsibles re ON re.report_row_id=rr.id LEFT JOIN employees e ON e.id=re.employee_id WHERE rp.report_date BETWEEN $1 AND $2 GROUP BY rp.report_date,o.id,o.name,o.sort_order,rr.id ORDER BY rp.report_date,o.sort_order`, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	f := excelize.NewFile()
	sheet := "Отчёт"
	f.SetSheetName("Sheet1", sheet)
	headers := []string{"Дата", "РП", "Количество открытых вакансий", "Минимальный порог приглашенных кандидатов", "Количество приглашенных кандидатов", "План на прошедших собеседование", "Количество прошедших собеседование", "Количество кандидатов на стажировке", "Количество кандидатов в резерве", "Количество принятых работников", "Количество уволенных работников", "Ответственный работник", "Эффективность работников, %"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	row := 2
	for q.Next() {
		var vals [13]any
		var d, o string
		var a1, a2, a3, a4, a5, a6, a7, hires, dismiss int
		var resp string
		var eff float64
		if err = q.Scan(&d, &o, &a1, &a2, &a3, &a4, &a5, &a6, &a7, &hires, &dismiss, &resp, &eff); err != nil {
			serverError(w, err)
			return
		}
		vals = [13]any{d, o, a1, a2, a3, a4, a5, a6, a7, hires, dismiss, resp, eff}
		for i, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			f.SetCellValue(sheet, cell, v)
		}
		row++
	}
	style, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"2563EB"}, Pattern: 1}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "center"}})
	f.SetCellStyle(sheet, "A1", "M1", style)
	f.SetRowHeight(sheet, 1, 48)
	f.SetColWidth(sheet, "A", "A", 13)
	f.SetColWidth(sheet, "B", "B", 30)
	f.SetColWidth(sheet, "C", "M", 19)
	f.AutoFilter(sheet, "A1:M1", nil)
	buf, err := f.WriteToBuffer()
	if err != nil {
		serverError(w, err)
		return
	}
	name := fmt.Sprintf("HR_%s_%s.xlsx", from, to)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Write(buf.Bytes())
}

func (a *App) employees(w http.ResponseWriter, r *http.Request) {
	search := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	q, err := a.db.Query(r.Context(), `SELECT id,first_name,last_name,middle_name,active FROM employees WHERE concat_ws(' ',last_name,first_name,middle_name) ILIKE $1 ORDER BY last_name,first_name LIMIT 500`, search)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []employee{}
	for q.Next() {
		var e employee
		if err = q.Scan(&e.ID, &e.FirstName, &e.LastName, &e.MiddleName, &e.Active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, e)
	}
	jsonOut(w, 200, out)
}
func (a *App) createEmployee(w http.ResponseWriter, r *http.Request) {
	var e employee
	if !decode(w, r, &e) {
		return
	}
	if strings.TrimSpace(e.FirstName) == "" || strings.TrimSpace(e.LastName) == "" {
		problem(w, 422, "Имя и фамилия обязательны")
		return
	}
	err := a.db.QueryRow(r.Context(), `INSERT INTO employees(first_name,last_name,middle_name) VALUES($1,$2,$3) RETURNING id`, strings.TrimSpace(e.FirstName), strings.TrimSpace(e.LastName), strings.TrimSpace(e.MiddleName)).Scan(&e.ID)
	if err != nil {
		problem(w, 409, "Такой сотрудник уже существует")
		return
	}
	e.Active = true
	a.log(r.Context(), "employee.created", "employee", e.ID)
	jsonOut(w, 201, e)
}
func (a *App) updateEmployee(w http.ResponseWriter, r *http.Request) {
	var e employee
	if !decode(w, r, &e) {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE employees SET first_name=$2,last_name=$3,middle_name=$4,active=$5 WHERE id=$1`, r.PathValue("id"), strings.TrimSpace(e.FirstName), strings.TrimSpace(e.LastName), strings.TrimSpace(e.MiddleName), e.Active)
	if err != nil || tag.RowsAffected() == 0 {
		problem(w, 422, "Не удалось обновить сотрудника")
		return
	}
	a.log(r.Context(), "employee.updated", "employee", r.PathValue("id"))
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) deleteEmployee(w http.ResponseWriter, r *http.Request) {
	tag, err := a.db.Exec(r.Context(), `UPDATE employees SET active=false WHERE id=$1`, r.PathValue("id"))
	if err != nil || tag.RowsAffected() == 0 {
		problem(w, 404, "Сотрудник не найден")
		return
	}
	a.log(r.Context(), "employee.archived", "employee", r.PathValue("id"))
	jsonOut(w, 200, map[string]bool{"ok": true})
}

type office struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
	Active    bool   `json:"active"`
}

func (a *App) offices(w http.ResponseWriter, r *http.Request) {
	if _,ok:=requireManager(w,r);!ok{return}
	q, err := a.db.Query(r.Context(), `SELECT id,name,sort_order,active FROM offices ORDER BY sort_order,name`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []office{}
	for q.Next() {
		var o office
		_ = q.Scan(&o.ID, &o.Name, &o.SortOrder, &o.Active)
		out = append(out, o)
	}
	jsonOut(w, 200, out)
}
func (a *App) createOffice(w http.ResponseWriter, r *http.Request) {
	if _,ok:=requireManager(w,r);!ok{return}
	var o office
	if !decode(w, r, &o) {
		return
	}
	if strings.TrimSpace(o.Name) == "" {
		problem(w, 422, "Название обязательно")
		return
	}
	err := a.db.QueryRow(r.Context(), `INSERT INTO offices(name,sort_order) VALUES($1,$2) RETURNING id`, strings.TrimSpace(o.Name), o.SortOrder).Scan(&o.ID)
	if err != nil {
		problem(w, 409, "Такое РП уже существует")
		return
	}
	o.Active = true
	a.log(r.Context(), "office.created", "office", o.ID)
	jsonOut(w, 201, o)
}
func (a *App) updateOffice(w http.ResponseWriter, r *http.Request) {
	if _,ok:=requireManager(w,r);!ok{return}
	var o office
	if !decode(w, r, &o) {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE offices SET name=$2,sort_order=$3,active=$4,updated_at=now() WHERE id=$1`, r.PathValue("id"), strings.TrimSpace(o.Name), o.SortOrder, o.Active)
	if err != nil || tag.RowsAffected() == 0 {
		problem(w, 422, "Не удалось обновить РП")
		return
	}
	a.log(r.Context(), "office.updated", "office", r.PathValue("id"))
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	if _,ok:=requireManager(w,r);!ok{return}
	var v int
	_ = a.db.QueryRow(r.Context(), `SELECT value::int FROM settings WHERE key='invitation_threshold'`).Scan(&v)
	jsonOut(w, 200, map[string]int{"invitationThreshold": v})
}
func (a *App) updateSettings(w http.ResponseWriter, r *http.Request) {
	if _,ok:=requireManager(w,r);!ok{return}
	var x struct {
		InvitationThreshold int `json:"invitationThreshold"`
	}
	if !decode(w, r, &x) {
		return
	}
	if x.InvitationThreshold < 0 {
		problem(w, 422, "Норматив не может быть отрицательным")
		return
	}
	_, err := a.db.Exec(r.Context(), `UPDATE settings SET value=$1,updated_at=now() WHERE key='invitation_threshold'`, strconv.Itoa(x.InvitationThreshold))
	if err != nil {
		serverError(w, err)
		return
	}
	a.log(r.Context(), "settings.updated", "settings", "invitation_threshold")
	jsonOut(w, 200, x)
}

func (a *App) audit(w http.ResponseWriter, r *http.Request) {
	q, err := a.db.Query(r.Context(), `SELECT id,actor,action,entity_type,COALESCE(entity_id,''),details,created_at FROM audit_log ORDER BY id DESC LIMIT 300`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []map[string]any{}
	for q.Next() {
		var id int64
		var actor, action, typ, eid string
		var details []byte
		var at time.Time
		_ = q.Scan(&id, &actor, &action, &typ, &eid, &details, &at)
		var d any
		_ = json.Unmarshal(details, &d)
		out = append(out, map[string]any{"id": id, "actor": actor, "action": action, "entityType": typ, "entityId": eid, "details": d, "createdAt": at})
	}
	jsonOut(w, 200, out)
}

func (a *App) imports(w http.ResponseWriter, r *http.Request) {
	q, err := a.db.Query(r.Context(), `SELECT id,file_name,status,total_rows,processed_rows,imported_rows,errors,created_at,finished_at FROM import_jobs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []map[string]any{}
	for q.Next() {
		var id, name, status string
		var total, processed, imported int
		var errs []byte
		var created time.Time
		var finished *time.Time
		_ = q.Scan(&id, &name, &status, &total, &processed, &imported, &errs, &created, &finished)
		var e any
		_ = json.Unmarshal(errs, &e)
		out = append(out, map[string]any{"id": id, "fileName": name, "status": status, "totalRows": total, "processedRows": processed, "importedRows": imported, "errors": e, "createdAt": created, "finishedAt": finished})
	}
	jsonOut(w, 200, out)
}

func (a *App) employeeTemplate(w http.ResponseWriter, r *http.Request) {
	f := excelize.NewFile()
	s := "Сотрудники"
	f.SetSheetName("Sheet1", s)
	for i, h := range []string{"Имя", "Фамилия", "Отчество"} {
		c, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(s, c, h)
	}
	style, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"2563EB"}}})
	f.SetCellStyle(s, "A1", "C1", style)
	f.SetColWidth(s, "A", "C", 25)
	buf, _ := f.WriteToBuffer()
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="employees_template.xlsx"`)
	w.Write(buf.Bytes())
}

func (a *App) importEmployees(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(15 << 20); err != nil {
		problem(w, 400, "Файл слишком большой или повреждён")
		return
	}
	file, h, err := r.FormFile("file")
	if err != nil {
		problem(w, 400, "Выберите XLSX-файл")
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(h.Filename), ".xlsx") {
		problem(w, 422, "Допустим только формат .xlsx")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, 15<<20))
	if err != nil {
		serverError(w, err)
		return
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO import_jobs(file_name) VALUES($1) RETURNING id`, h.Filename).Scan(&id)
	if err != nil {
		serverError(w, err)
		return
	}
	go a.processImport(id, data)
	jsonOut(w, 202, map[string]string{"id": id})
}

type importError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

func (a *App) processImport(id string, data []byte) {
	ctx := context.Background()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		a.finishImport(ctx, id, "failed", 0, 0, []importError{{Row: 0, Message: "Не удалось открыть XLSX: " + err.Error()}})
		return
	}
	defer f.Close()
	s := f.GetSheetName(0)
	rows, err := f.GetRows(s)
	if err != nil || len(rows) == 0 {
		a.finishImport(ctx, id, "failed", 0, 0, []importError{{Row: 0, Message: "Лист пуст или повреждён"}})
		return
	}
	if len(rows[0]) != 3 || strings.TrimSpace(rows[0][0]) != "Имя" || strings.TrimSpace(rows[0][1]) != "Фамилия" || strings.TrimSpace(rows[0][2]) != "Отчество" {
		a.finishImport(ctx, id, "failed", 0, 0, []importError{{Row: 1, Message: "Ожидаются строго три колонки: Имя, Фамилия, Отчество"}})
		return
	}
	total := len(rows) - 1
	_, _ = a.db.Exec(ctx, `UPDATE import_jobs SET status='processing',total_rows=$2 WHERE id=$1`, id, total)
	a.progress.send(id, map[string]any{"id": id, "status": "processing", "total": total, "processed": 0, "imported": 0, "errors": []any{}})
	errs := []importError{}
	imported := 0
	for i := 1; i < len(rows); i++ {
		cells := rows[i]
		for len(cells) < 3 {
			cells = append(cells, "")
		}
		first, last, middle := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2])
		if first == "" || last == "" {
			errs = append(errs, importError{Row: i + 1, Message: "Имя и фамилия обязательны"})
		} else {
			tag, e := a.db.Exec(ctx, `INSERT INTO employees(first_name,last_name,middle_name) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, first, last, middle)
			if e != nil {
				errs = append(errs, importError{Row: i + 1, Message: e.Error()})
			} else if tag.RowsAffected() == 0 {
				errs = append(errs, importError{Row: i + 1, Message: "Сотрудник уже существует"})
			} else {
				imported++
			}
		}
		processed := i
		raw, _ := json.Marshal(errs)
		_, _ = a.db.Exec(ctx, `UPDATE import_jobs SET processed_rows=$2,imported_rows=$3,errors=$4 WHERE id=$1`, id, processed, imported, raw)
		a.progress.send(id, map[string]any{"id": id, "status": "processing", "total": total, "processed": processed, "imported": imported, "errors": errs})
	}
	a.finishImport(ctx, id, "completed", total, imported, errs)
	a.log(ctx, "employees.imported", "import", id)
}
func (a *App) finishImport(ctx context.Context, id, status string, total, imported int, errs []importError) {
	raw, _ := json.Marshal(errs)
	_, _ = a.db.Exec(ctx, `UPDATE import_jobs SET status=$2,total_rows=$3,processed_rows=$3,imported_rows=$4,errors=$5,finished_at=now() WHERE id=$1`, id, status, total, imported, raw)
	a.progress.send(id, map[string]any{"id": id, "status": status, "total": total, "processed": total, "imported": imported, "errors": errs})
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (a *App) importWebsocket(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	id := r.PathValue("id")
	a.progress.add(id, c)
	defer a.progress.remove(id, c)
	for {
		if _, _, err = c.ReadMessage(); err != nil {
			return
		}
	}
}
func (h *progressHub) add(id string, c *websocket.Conn) {
	h.mu.Lock()
	if h.clients[id] == nil {
		h.clients[id] = map[*websocket.Conn]struct{}{}
	}
	h.clients[id][c] = struct{}{}
	latest := h.latest[id]
	h.mu.Unlock()
	if latest != nil {
		_ = c.WriteJSON(latest)
	}
}
func (h *progressHub) remove(id string, c *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients[id], c)
	h.mu.Unlock()
	c.Close()
}
func (h *progressHub) send(id string, v any) {
	h.mu.Lock()
	h.latest[id] = v
	list := make([]*websocket.Conn, 0, len(h.clients[id]))
	for c := range h.clients[id] {
		list = append(list, c)
	}
	h.mu.Unlock()
	for _, c := range list {
		_ = c.WriteJSON(v)
	}
}

func (a *App) log(ctx context.Context, action, typ, id string) {
	_, _ = a.db.Exec(ctx, `INSERT INTO audit_log(action,entity_type,entity_id) VALUES($1,$2,$3)`, action, typ, id)
}
func validDate(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil }
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		problem(w, 400, "Некорректные данные: "+err.Error())
		return false
	}
	return true
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, map[string]string{"error": msg})
}
func serverError(w http.ResponseWriter, err error) {
	fmt.Printf("server error: %v\n", err)
	problem(w, 500, "Внутренняя ошибка сервера")
}
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if x := recover(); x != nil {
				problem(w, 500, "Внутренняя ошибка сервера")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}

var _ = pgx.ErrNoRows
