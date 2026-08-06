package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	DatabaseURL string
	HTTPAddr    string
	StaticDir   string
	AppSecret   string
	SuperLogin  string
	SuperPass   string
}

type App struct {
	db       *pgxpool.Pool
	static   string
	secret   []byte
	progress *progressHub
	reports  *reportHub
}

type progressHub struct {
	mu      sync.RWMutex
	latest  map[string]any
	clients map[string]map[*websocket.Conn]struct{}
}

type reportHub struct {
	mu      sync.RWMutex
	clients map[string]map[*websocket.Conn]struct{}
}

func Run(ctx context.Context, cfg Config) error {
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = migrate(ctx, db); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	a := &App{db: db, static: cfg.StaticDir, secret: []byte(cfg.AppSecret), progress: &progressHub{latest: map[string]any{}, clients: map[string]map[*websocket.Conn]struct{}{}}, reports: &reportHub{clients: map[string]map[*websocket.Conn]struct{}{}}}
	if err = a.ensureSuperadmin(ctx, cfg.SuperLogin, cfg.SuperPass); err != nil {
		return fmt.Errorf("superadmin: %w", err)
	}
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { log.Printf("HR application listening on %s", cfg.HTTPAddr); errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err = <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func migrate(ctx context.Context, db *pgxpool.Pool) error {
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no migration files found")
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err = db.Exec(ctx, string(b)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("POST /api/auth/login", a.login)
	m.HandleFunc("POST /api/auth/logout", a.logout)
	m.HandleFunc("GET /api/auth/me", a.me)
	m.HandleFunc("GET /api/users", a.users)
	m.HandleFunc("POST /api/users", a.createUser)
	m.HandleFunc("PUT /api/users/{id}", a.updateUser)
	m.HandleFunc("DELETE /api/users/{id}", a.deleteUser)
	m.HandleFunc("GET /api/bootstrap", a.bootstrap)
	m.HandleFunc("PUT /api/report/rows/{id}", a.updateRow)
	m.HandleFunc("POST /api/reports/{id}/complete", a.completeReport)
	m.HandleFunc("GET /api/reports/export", a.exportPeriod)
	m.HandleFunc("GET /api/offices", a.offices)
	m.HandleFunc("POST /api/offices", a.createOffice)
	m.HandleFunc("PUT /api/offices/{id}", a.updateOffice)
	m.HandleFunc("GET /api/settings", a.settings)
	m.HandleFunc("PUT /api/settings", a.updateSettings)
	m.HandleFunc("GET /ws/reports", a.reportWebsocket)
	m.HandleFunc("/", a.staticFile)
	return requestLog(recoverer(a.auth(m)))
}

func (a *App) staticFile(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(a.static, filepath.Clean(r.URL.Path))
	if r.URL.Path == "/" {
		p = filepath.Join(a.static, "index.html")
	}
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		p = filepath.Join(a.static, "index.html")
	}
	http.ServeFile(w, r, p)
}

type employee struct {
	ID         string `json:"id"`
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	MiddleName string `json:"middleName"`
	Active     bool   `json:"active"`
}
type reportRow struct {
	ID                    string                        `json:"id"`
	OfficeID              string                        `json:"officeId"`
	OfficeName            string                        `json:"officeName"`
	SortOrder             int                           `json:"sortOrder"`
	OpenVacancies         int                           `json:"openVacancies"`
	InvitationThreshold   int                           `json:"invitationThreshold"`
	InvitedCandidates     int                           `json:"invitedCandidates"`
	InterviewPlan         int                           `json:"interviewPlan"`
	InterviewedCandidates int                           `json:"interviewedCandidates"`
	Interns               int                           `json:"interns"`
	ReserveCandidates     int                           `json:"reserveCandidates"`
	DismissedWorkers      int                           `json:"dismissedWorkers"`
	Efficiency            float64                       `json:"efficiency"`
	HiredWorkers          []string                      `json:"hiredWorkers"`
	HiredDetails          []hiredDetail                 `json:"hiredDetails,omitempty"`
	Contributions         map[string][]cellContribution `json:"contributions,omitempty"`
}

type hiredDetail struct {
	FullName    string `json:"fullName"`
	Responsible string `json:"responsible"`
}

type cellContribution struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

func Efficiency(interns, plan int) float64 {
	if plan <= 0 {
		return 0
	}
	return float64(interns) * 100 / float64(plan)
}

func (a *App) bootstrap(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if !validDate(date) {
		problem(w, 400, "Дата должна иметь формат YYYY-MM-DD")
		return
	}
	ctx := r.Context()
	claims := claimsFrom(ctx)
	var norm int
	_ = a.db.QueryRow(ctx, `SELECT value::int FROM settings WHERE key='invitation_threshold'`).Scan(&norm)
	if isManager(claims) {
		employeeID := strings.TrimSpace(r.URL.Query().Get("employeeId"))
		rows, err := a.loadAggregateRows(ctx, date, employeeID)
		if err != nil {
			serverError(w, err)
			return
		}
		jsonOut(w, 200, map[string]any{"report": map[string]any{"id": "", "date": date, "status": "read_only", "editable": false}, "rows": rows, "norm": norm, "totals": totals(rows)})
		return
	}
	var reportID, status string
	err := a.db.QueryRow(ctx, `INSERT INTO reports(report_date,owner_user_id) VALUES($1,$2) ON CONFLICT(report_date,owner_user_id) WHERE owner_user_id IS NOT NULL DO UPDATE SET report_date=EXCLUDED.report_date RETURNING id,status`, date, claims.UserID).Scan(&reportID, &status)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = a.db.Exec(ctx, `INSERT INTO report_rows(report_id,office_id) SELECT $1,id FROM offices WHERE active ON CONFLICT DO NOTHING`, reportID)
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.loadRows(ctx, reportID)
	if err != nil {
		serverError(w, err)
		return
	}
	if err = a.applySharedVacancies(ctx, date, rows); err != nil {
		serverError(w, err)
		return
	}
	editable := date == localToday() && status == "draft"
	jsonOut(w, 200, map[string]any{"report": map[string]any{"id": reportID, "date": date, "status": status, "editable": editable}, "rows": rows, "norm": norm, "totals": totals(rows)})
}

func (a *App) applySharedVacancies(ctx context.Context, date string, rows []reportRow) error {
	q, err := a.db.Query(ctx, `SELECT office_id,open_vacancies FROM daily_office_shared WHERE report_date=$1`, date)
	if err != nil {
		return err
	}
	defer q.Close()
	values := map[string]int{}
	for q.Next() {
		var id string
		var value int
		if err = q.Scan(&id, &value); err != nil {
			return err
		}
		values[id] = value
	}
	for i := range rows {
		rows[i].OpenVacancies = values[rows[i].OfficeID]
	}
	return q.Err()
}

func localToday() string {
	loc, err := time.LoadLocation("Asia/Qyzylorda")
	if err != nil {
		return time.Now().Format("2006-01-02")
	}
	return time.Now().In(loc).Format("2006-01-02")
}

func (a *App) loadAggregateRows(ctx context.Context, date, ownerID string) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `WITH hc AS (SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
		SELECT o.id,o.name,o.sort_order,
		COALESCE(ds.open_vacancies,0),COALESCE(sum(rr.invitation_threshold),0),COALESCE(sum(rr.invited_candidates),0),
		COALESCE(sum(rr.interview_plan),0),COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),
		COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(rr.dismissed_workers),0),COALESCE(sum(hc.n),0)
		FROM offices o
		LEFT JOIN reports rp ON rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
		LEFT JOIN users u ON u.id=rp.owner_user_id AND NOT u.system
		LEFT JOIN report_rows rr ON rr.office_id=o.id AND rr.report_id=rp.id AND u.id IS NOT NULL
		LEFT JOIN hc ON hc.report_row_id=rr.id
		LEFT JOIN daily_office_shared ds ON ds.report_date=$1 AND ds.office_id=o.id
		WHERE o.active GROUP BY o.id,o.name,o.sort_order,ds.open_vacancies ORDER BY o.sort_order,o.name`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []reportRow{}
	for q.Next() {
		var x reportRow
		var hires int
		if err = q.Scan(&x.OfficeID, &x.OfficeName, &x.SortOrder, &x.OpenVacancies, &x.InvitationThreshold, &x.InvitedCandidates, &x.InterviewPlan, &x.InterviewedCandidates, &x.Interns, &x.ReserveCandidates, &x.DismissedWorkers, &hires); err != nil {
			return nil, err
		}
		x.Efficiency = Efficiency(x.Interns, x.InterviewPlan)
		x.HiredWorkers = make([]string, hires)
		x.HiredDetails = []hiredDetail{}
		x.Contributions = map[string][]cellContribution{}
		out = append(out, x)
	}
	if err = q.Err(); err != nil {
		return nil, err
	}
	byOffice := map[string]*reportRow{}
	for i := range out {
		byOffice[out[i].OfficeID] = &out[i]
	}
	details, err := a.db.Query(ctx, `WITH hc AS (SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
		SELECT rr.office_id,concat_ws(' ',e.last_name,e.first_name,e.middle_name),rr.invitation_threshold,
		rr.invited_candidates,rr.interview_plan,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,
		COALESCE(hc.n,0),rr.dismissed_workers
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id JOIN report_rows rr ON rr.report_id=rp.id
		LEFT JOIN hc ON hc.report_row_id=rr.id WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
		ORDER BY e.last_name,e.first_name`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer details.Close()
	for details.Next() {
		var officeID, name string
		var plan, invited, interviewPlan, interviewed, interns, reserve, hires, dismissed int
		if err = details.Scan(&officeID, &name, &plan, &invited, &interviewPlan, &interviewed, &interns, &reserve, &hires, &dismissed); err != nil {
			return nil, err
		}
		row := byOffice[officeID]
		if row == nil {
			continue
		}
		add := func(key string, value float64) {
			row.Contributions[key] = append(row.Contributions[key], cellContribution{Name: name, Value: value})
		}
		add("invitationThreshold", float64(plan))
		add("invitedCandidates", float64(invited))
		add("interviewPlan", float64(interviewPlan))
		add("interviewedCandidates", float64(interviewed))
		add("interns", float64(interns))
		add("reserveCandidates", float64(reserve))
		add("hiredWorkers", float64(hires))
		add("dismissedWorkers", float64(dismissed))
		add("efficiency", Efficiency(interns, interviewPlan))
	}
	if err = details.Err(); err != nil {
		return nil, err
	}
	hiredRows, err := a.db.Query(ctx, `SELECT rr.office_id,hw.full_name,concat_ws(' ',e.last_name,e.first_name,e.middle_name)
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id JOIN report_rows rr ON rr.report_id=rp.id
		JOIN hired_workers hw ON hw.report_row_id=rr.id
		WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
		ORDER BY e.last_name,e.first_name,hw.created_at`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer hiredRows.Close()
	for hiredRows.Next() {
		var officeID, fullName, responsible string
		if err = hiredRows.Scan(&officeID, &fullName, &responsible); err != nil {
			return nil, err
		}
		if row := byOffice[officeID]; row != nil {
			row.HiredDetails = append(row.HiredDetails, hiredDetail{FullName: fullName, Responsible: responsible})
		}
	}
	if err = hiredRows.Err(); err != nil {
		return nil, err
	}
	shared, err := a.db.Query(ctx, `SELECT ds.office_id,concat_ws(' ',e.last_name,e.first_name,e.middle_name),ds.open_vacancies FROM daily_office_shared ds LEFT JOIN users u ON u.id=ds.updated_by_user_id LEFT JOIN employees e ON e.id=u.employee_id WHERE ds.report_date=$1`, date)
	if err != nil {
		return nil, err
	}
	defer shared.Close()
	for shared.Next() {
		var officeID, name string
		var value int
		if err = shared.Scan(&officeID, &name, &value); err != nil {
			return nil, err
		}
		if name == "" {
			name = "Общее значение"
		}
		if row := byOffice[officeID]; row != nil {
			row.Contributions["openVacancies"] = []cellContribution{{Name: name, Value: float64(value)}}
		}
	}
	return out, shared.Err()
}

func (a *App) loadRows(ctx context.Context, reportID string) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `SELECT rr.id,o.id,o.name,o.sort_order,rr.open_vacancies,rr.invitation_threshold,rr.invited_candidates,rr.interview_plan,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,rr.dismissed_workers,rr.efficiency FROM report_rows rr JOIN offices o ON o.id=rr.office_id WHERE rr.report_id=$1 ORDER BY o.sort_order,o.name`, reportID)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []reportRow{}
	for q.Next() {
		var x reportRow
		if err = q.Scan(&x.ID, &x.OfficeID, &x.OfficeName, &x.SortOrder, &x.OpenVacancies, &x.InvitationThreshold, &x.InvitedCandidates, &x.InterviewPlan, &x.InterviewedCandidates, &x.Interns, &x.ReserveCandidates, &x.DismissedWorkers, &x.Efficiency); err != nil {
			return nil, err
		}
		x.HiredWorkers = []string{}
		out = append(out, x)
	}
	for i := range out {
		hr, err := a.db.Query(ctx, `SELECT full_name FROM hired_workers WHERE report_row_id=$1 ORDER BY created_at,id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for hr.Next() {
			var n string
			_ = hr.Scan(&n)
			out[i].HiredWorkers = append(out[i].HiredWorkers, n)
		}
		hr.Close()
	}
	return out, q.Err()
}

type rowInput struct {
	OpenVacancies         int      `json:"openVacancies"`
	InvitationThreshold   int      `json:"invitationThreshold"`
	InvitedCandidates     int      `json:"invitedCandidates"`
	InterviewPlan         int      `json:"interviewPlan"`
	InterviewedCandidates int      `json:"interviewedCandidates"`
	Interns               int      `json:"interns"`
	ReserveCandidates     int      `json:"reserveCandidates"`
	DismissedWorkers      int      `json:"dismissedWorkers"`
	ResponsibleIDs        []string `json:"responsibleIds"`
	HiredWorkers          []string `json:"hiredWorkers"`
}

func (a *App) updateRow(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	if claims.Role != "employee" {
		problem(w, 403, "Агрегированный отчёт доступен только для просмотра")
		return
	}
	var in rowInput
	if !decode(w, r, &in) {
		return
	}
	if hasNegative(in) {
		problem(w, 422, "Числовые значения не могут быть отрицательными")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	eff := Efficiency(in.Interns, in.InterviewPlan)
	var officeID string
	if err = tx.QueryRow(ctx, `SELECT rr.office_id FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id WHERE rr.id=$1 AND rp.owner_user_id=$2 AND rp.report_date=$3 AND rp.status='draft'`, r.PathValue("id"), claims.UserID, localToday()).Scan(&officeID); err != nil {
		problem(w, 409, "Можно редактировать только свой отчёт за текущий день")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO daily_office_shared(report_date,office_id,open_vacancies,updated_by_user_id) VALUES($1,$2,$3,$4) ON CONFLICT(report_date,office_id) DO UPDATE SET open_vacancies=EXCLUDED.open_vacancies,updated_by_user_id=EXCLUDED.updated_by_user_id,updated_at=now()`, localToday(), officeID, in.OpenVacancies, claims.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE report_rows rr SET open_vacancies=$2,invitation_threshold=$3,invited_candidates=$4,interview_plan=$5,interviewed_candidates=$6,interns=$7,reserve_candidates=$8,dismissed_workers=$9,efficiency=$10,updated_at=now() FROM reports r WHERE rr.report_id=r.id AND rr.id=$1 AND r.status='draft' AND r.owner_user_id=$11 AND r.report_date=$12`, r.PathValue("id"), 0, in.InvitationThreshold, in.InvitedCandidates, in.InterviewPlan, in.InterviewedCandidates, in.Interns, in.ReserveCandidates, in.DismissedWorkers, eff, claims.UserID, localToday())
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		problem(w, 409, "Можно редактировать только свой отчёт за текущий день")
		return
	}
	_, _ = tx.Exec(ctx, `DELETE FROM report_row_responsibles WHERE report_row_id=$1`, r.PathValue("id"))
	for _, id := range in.ResponsibleIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO report_row_responsibles VALUES($1,$2) ON CONFLICT DO NOTHING`, r.PathValue("id"), id); err != nil {
			problem(w, 422, "Не найден выбранный сотрудник")
			return
		}
	}
	_, _ = tx.Exec(ctx, `DELETE FROM hired_workers WHERE report_row_id=$1`, r.PathValue("id"))
	for _, n := range in.HiredWorkers {
		n = strings.TrimSpace(n)
		if n != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO hired_workers(report_row_id,full_name) VALUES($1,$2)`, r.PathValue("id"), n); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_log(action,entity_type,entity_id,details) VALUES('report_row.updated','report_row',$1,jsonb_build_object('efficiency',$2::numeric))`, r.PathValue("id"), eff); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.reports.send(localToday(), map[string]any{"type": "report_updated", "date": localToday(), "officeId": officeID, "field": "openVacancies", "value": in.OpenVacancies, "updatedBy": claims.Username})
	jsonOut(w, 200, map[string]any{"efficiency": eff, "hiredCount": len(nonEmpty(in.HiredWorkers))})
}

func hasNegative(x rowInput) bool {
	return x.OpenVacancies < 0 || x.InvitationThreshold < 0 || x.InvitedCandidates < 0 || x.InterviewPlan < 0 || x.InterviewedCandidates < 0 || x.Interns < 0 || x.ReserveCandidates < 0 || x.DismissedWorkers < 0
}
func nonEmpty(v []string) []string {
	o := []string{}
	for _, x := range v {
		if strings.TrimSpace(x) != "" {
			o = append(o, x)
		}
	}
	return o
}

func (a *App) completeReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := claimsFrom(ctx)
	if claims.Role != "employee" {
		problem(w, 403, "Недостаточно прав")
		return
	}
	tag, err := a.db.Exec(ctx, `UPDATE reports SET status='completed',completed_at=now(),updated_at=now() WHERE id=$1 AND status='draft' AND owner_user_id=$2`, r.PathValue("id"), claims.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		problem(w, 409, "Отчёт уже завершён")
		return
	}
	_, _ = a.db.Exec(ctx, `INSERT INTO audit_log(action,entity_type,entity_id) VALUES('report.completed','report',$1)`, r.PathValue("id"))
	jsonOut(w, 200, map[string]string{"status": "completed"})
}

func totals(rows []reportRow) map[string]any {
	t := map[string]any{"openVacancies": 0, "invitationThreshold": 0, "invitedCandidates": 0, "interviewPlan": 0, "interviewedCandidates": 0, "interns": 0, "reserveCandidates": 0, "hiredWorkers": 0, "dismissedWorkers": 0}
	for _, x := range rows {
		t["openVacancies"] = t["openVacancies"].(int) + x.OpenVacancies
		t["invitationThreshold"] = t["invitationThreshold"].(int) + x.InvitationThreshold
		t["invitedCandidates"] = t["invitedCandidates"].(int) + x.InvitedCandidates
		t["interviewPlan"] = t["interviewPlan"].(int) + x.InterviewPlan
		t["interviewedCandidates"] = t["interviewedCandidates"].(int) + x.InterviewedCandidates
		t["interns"] = t["interns"].(int) + x.Interns
		t["reserveCandidates"] = t["reserveCandidates"].(int) + x.ReserveCandidates
		t["hiredWorkers"] = t["hiredWorkers"].(int) + len(x.HiredWorkers)
		t["dismissedWorkers"] = t["dismissedWorkers"].(int) + x.DismissedWorkers
	}
	t["efficiency"] = Efficiency(t["interns"].(int), t["interviewPlan"].(int))
	return t
}
