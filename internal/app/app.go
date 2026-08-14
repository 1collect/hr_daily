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

func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("POST /api/auth/login", a.login)
	m.HandleFunc("POST /api/auth/logout", a.logout)
	m.HandleFunc("GET /api/auth/me", a.me)
	m.HandleFunc("GET /api/users", a.users)
	m.HandleFunc("POST /api/users", a.createUser)
	m.HandleFunc("PUT /api/users/{id}", a.updateUser)
	m.HandleFunc("POST /api/users/{id}/plans", a.createUserPlan)
	m.HandleFunc("DELETE /api/users/{id}/plans/{date}", a.deleteFutureUserPlan)
	m.HandleFunc("POST /api/users/{id}/main-office-plans", a.createMainOfficeUserPlan)
	m.HandleFunc("DELETE /api/users/{id}/main-office-plans/{date}", a.deleteFutureMainOfficeUserPlan)
	m.HandleFunc("DELETE /api/users/{id}", a.deleteUser)
	m.HandleFunc("GET /api/bootstrap", a.bootstrap)
	m.HandleFunc("GET /api/daily-plans", a.dailyPlans)
	m.HandleFunc("PUT /api/daily-plans", a.updateDailyPlans)
	m.HandleFunc("GET /api/report-access", a.reportAccessUsers)
	m.HandleFunc("POST /api/report-access", a.openReportAccess)
	m.HandleFunc("DELETE /api/report-access", a.closeReportAccess)
	m.HandleFunc("PUT /api/report/rows/{id}", a.updateRow)
	m.HandleFunc("POST /api/reports/{id}/complete", a.completeReport)
	m.HandleFunc("GET /api/reports/export", a.exportPeriod)
	m.HandleFunc("GET /api/offices", a.offices)
	m.HandleFunc("POST /api/offices", a.createOffice)
	m.HandleFunc("PUT /api/offices/order", a.reorderOffices)
	m.HandleFunc("PUT /api/offices/{id}", a.updateOffice)
	m.HandleFunc("GET /api/main-offices", a.mainOffices)
	m.HandleFunc("POST /api/main-offices", a.createMainOffice)
	m.HandleFunc("PUT /api/main-offices/order", a.reorderMainOffices)
	m.HandleFunc("PUT /api/main-offices/{id}", a.updateMainOffice)
	m.HandleFunc("GET /api/main-office/bootstrap", a.mainOfficeBootstrap)
	m.HandleFunc("PUT /api/main-office/report/rows/{id}", a.updateMainOfficeRow)
	m.HandleFunc("GET /ws/reports", a.reportWebsocket)
	m.HandleFunc("/", a.staticFile)
	return requestLog(recoverer(a.auth(m)))
}

func (a *App) staticFile(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
		http.NotFound(w, r)
		return
	}
	// Frontend assets keep stable names (app.js, ui.css), so caching them can
	// leave a browser running an older UI against a newer API after deployment.
	w.Header().Set("Cache-Control", "no-store")
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
	PlannedReserve        int                           `json:"plannedReserve"`
	InvitedCandidates     int                           `json:"invitedCandidates"`
	InterviewedCandidates int                           `json:"interviewedCandidates"`
	Interns               int                           `json:"interns"`
	ReserveCandidates     int                           `json:"reserveCandidates"`
	DismissedWorkers      int                           `json:"dismissedWorkers"`
	Efficiency            float64                       `json:"efficiency"`
	EfficiencyPlan        int                           `json:"efficiencyPlan"`
	HiredWorkers          []hiredWorker                 `json:"hiredWorkers"`
	HiredDetails          []hiredDetail                 `json:"hiredDetails,omitempty"`
	People                map[string][]string           `json:"people"`
	PeopleDetails         map[string][]hiredDetail      `json:"peopleDetails,omitempty"`
	Contributions         map[string][]cellContribution `json:"contributions,omitempty"`
}

type hiredDetail struct {
	FullName    string `json:"fullName"`
	Position    string `json:"position"`
	Responsible string `json:"responsible"`
}

type hiredWorker struct {
	FullName string `json:"fullName"`
	Position string `json:"position"`
}

type cellContribution struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

func Efficiency(interviewed, plan int) float64 {
	if plan <= 0 {
		return 0
	}
	return float64(interviewed) * 100 / float64(plan)
}

func (a *App) bootstrap(w http.ResponseWriter, r *http.Request) {
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
			_ = a.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id=$1 AND role='employee' AND active AND NOT system)`, employeeID).Scan(&active)
			if !active {
				problem(w, 404, "Сотрудник не найден")
				return
			}
		}
		plan, err := a.efficiencyPlanTotal(ctx, date, employeeID)
		if err != nil {
			serverError(w, err)
			return
		}
		rows, err := a.loadAggregateRows(ctx, date, employeeID, plan)
		if err != nil {
			serverError(w, err)
			return
		}
		var accessCount int
		_ = a.db.QueryRow(ctx, `SELECT count(*) FROM report_access_grants WHERE report_date=$1 AND expires_at>now()`, date).Scan(&accessCount)
		jsonOut(w, 200, map[string]any{"report": map[string]any{"id": "", "date": date, "status": "read_only", "editable": false, "accessCount": accessCount}, "rows": rows, "plan": plan, "totals": totals(rows, plan)})
		return
	}
	var reportID, status string
	err := a.db.QueryRow(ctx, `INSERT INTO reports(report_date,owner_user_id,owner_name_snapshot)
		SELECT $1,u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username)
		FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$2
		ON CONFLICT(report_date,owner_user_id) WHERE owner_user_id IS NOT NULL DO UPDATE SET report_date=EXCLUDED.report_date
		RETURNING id,status`, date, claims.UserID).Scan(&reportID, &status)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = a.db.Exec(ctx, `INSERT INTO report_rows(report_id,office_id,office_name_snapshot,office_sort_order_snapshot) SELECT $1,id,name,sort_order FROM offices WHERE active ON CONFLICT DO NOTHING`, reportID)
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
	plan, err := a.efficiencyPlanTotal(ctx, date, claims.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	editable := status == "draft" && a.hasReportEditAccess(ctx, claims.UserID, date)
	jsonOut(w, 200, map[string]any{"report": map[string]any{"id": reportID, "date": date, "status": status, "editable": editable}, "rows": rows, "plan": plan, "totals": totals(rows, plan)})
}

func (a *App) efficiencyPlanTotal(ctx context.Context, date, ownerID string) (int, error) {
	var plan int
	err := a.db.QueryRow(ctx, `SELECT COALESCE(sum(COALESCE(
		(SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=u.id AND d.report_date=$1::date AND d.report_type='rp'),
		(SELECT p.plan_count FROM employee_efficiency_plans p WHERE p.user_id=u.id AND p.effective_from<=$1::date ORDER BY p.effective_from DESC LIMIT 1),0)),0)::int
	FROM users u
	WHERE u.role='employee' AND u.active AND NOT u.system AND ($2='' OR u.id::text=$2)`, date, ownerID).Scan(&plan)
	return plan, err
}

func (a *App) applySharedVacancies(ctx context.Context, date string, rows []reportRow) error {
	q, err := a.db.Query(ctx, `SELECT office_id,open_vacancies,planned_reserve FROM daily_office_shared WHERE report_date=$1`, date)
	if err != nil {
		return err
	}
	defer q.Close()
	type sharedValues struct{ openVacancies, plannedReserve int }
	values := map[string]sharedValues{}
	for q.Next() {
		var id string
		var value sharedValues
		if err = q.Scan(&id, &value.openVacancies, &value.plannedReserve); err != nil {
			return err
		}
		values[id] = value
	}
	for i := range rows {
		value := values[rows[i].OfficeID]
		rows[i].OpenVacancies = value.openVacancies
		rows[i].PlannedReserve = value.plannedReserve
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

func (a *App) loadAggregateRows(ctx context.Context, date, ownerID string, plan int) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `WITH relevant_reports AS (
		SELECT rp.* FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
	), office_scope AS (
		SELECT o.id,
		COALESCE((SELECT NULLIF(rr2.office_name_snapshot,'') FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id ORDER BY rp2.created_at LIMIT 1),o.name) AS name,
		COALESCE((SELECT NULLIF(rr2.office_sort_order_snapshot,0) FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id ORDER BY rp2.created_at LIMIT 1),o.sort_order) AS sort_order
		FROM offices o WHERE o.active OR EXISTS (SELECT 1 FROM report_rows rr2 JOIN relevant_reports rp2 ON rp2.id=rr2.report_id WHERE rr2.office_id=o.id)
	), hc AS (SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
		SELECT o.id,o.name,o.sort_order,
		COALESCE(ds.open_vacancies,0),COALESCE(ds.planned_reserve,0),COALESCE(sum(rr.invited_candidates),0),
		COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),
		COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(rr.dismissed_workers),0),COALESCE(sum(hc.n),0)
		FROM office_scope o
		LEFT JOIN relevant_reports rp ON true
		LEFT JOIN report_rows rr ON rr.office_id=o.id AND rr.report_id=rp.id
		LEFT JOIN hc ON hc.report_row_id=rr.id
		LEFT JOIN daily_office_shared ds ON ds.report_date=$1 AND ds.office_id=o.id
		GROUP BY o.id,o.name,o.sort_order,ds.open_vacancies,ds.planned_reserve ORDER BY o.sort_order,o.name`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []reportRow{}
	for q.Next() {
		var x reportRow
		var hires int
		if err = q.Scan(&x.OfficeID, &x.OfficeName, &x.SortOrder, &x.OpenVacancies, &x.PlannedReserve, &x.InvitedCandidates, &x.InterviewedCandidates, &x.Interns, &x.ReserveCandidates, &x.DismissedWorkers, &hires); err != nil {
			return nil, err
		}
		x.EfficiencyPlan = plan
		x.Efficiency = Efficiency(x.InterviewedCandidates, x.EfficiencyPlan)
		x.HiredWorkers = make([]hiredWorker, hires)
		x.HiredDetails = []hiredDetail{}
		x.People = map[string][]string{}
		x.PeopleDetails = map[string][]hiredDetail{}
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
		SELECT rr.office_id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username),
		rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0),
		COALESCE(hc.n,0),rr.dismissed_workers
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id JOIN report_rows rr ON rr.report_id=rp.id
		LEFT JOIN hc ON hc.report_row_id=rr.id WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
		ORDER BY e.last_name,e.first_name`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer details.Close()
	for details.Next() {
		var officeID, name string
		var invited, interviewed, interns, reserve, plan, hires, dismissed int
		if err = details.Scan(&officeID, &name, &invited, &interviewed, &interns, &reserve, &plan, &hires, &dismissed); err != nil {
			return nil, err
		}
		row := byOffice[officeID]
		if row == nil {
			continue
		}
		add := func(key string, value float64) {
			row.Contributions[key] = append(row.Contributions[key], cellContribution{Name: name, Value: value})
		}
		add("invitedCandidates", float64(invited))
		add("interviewedCandidates", float64(interviewed))
		add("interns", float64(interns))
		add("reserveCandidates", float64(reserve))
		add("hiredWorkers", float64(hires))
		add("dismissedWorkers", float64(dismissed))
		add("efficiency", Efficiency(interviewed, plan))
	}
	if err = details.Err(); err != nil {
		return nil, err
	}
	hiredRows, err := a.db.Query(ctx, `SELECT rr.office_id,hw.full_name,hw.position,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id JOIN report_rows rr ON rr.report_id=rp.id
		JOIN hired_workers hw ON hw.report_row_id=rr.id
		WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
		ORDER BY e.last_name,e.first_name,hw.created_at`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer hiredRows.Close()
	for hiredRows.Next() {
		var officeID, fullName, position, responsible string
		if err = hiredRows.Scan(&officeID, &fullName, &position, &responsible); err != nil {
			return nil, err
		}
		if row := byOffice[officeID]; row != nil {
			row.HiredDetails = append(row.HiredDetails, hiredDetail{FullName: fullName, Position: position, Responsible: responsible})
		}
	}
	if err = hiredRows.Err(); err != nil {
		return nil, err
	}
	peopleRows, err := a.db.Query(ctx, `SELECT rr.office_id,p.category,p.full_name,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
		FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		LEFT JOIN employees e ON e.id=u.employee_id JOIN report_rows rr ON rr.report_id=rp.id
		JOIN report_row_people p ON p.report_row_id=rr.id
		WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
		ORDER BY e.last_name,e.first_name,p.created_at,p.id`, date, ownerID)
	if err != nil {
		return nil, err
	}
	defer peopleRows.Close()
	for peopleRows.Next() {
		var officeID, category, fullName, responsible string
		if err = peopleRows.Scan(&officeID, &category, &fullName, &responsible); err != nil {
			return nil, err
		}
		if row := byOffice[officeID]; row != nil {
			row.PeopleDetails[category] = append(row.PeopleDetails[category], hiredDetail{FullName: fullName, Responsible: responsible})
		}
	}
	if err = peopleRows.Err(); err != nil {
		return nil, err
	}
	shared, err := a.db.Query(ctx, `SELECT ds.office_id,CASE WHEN u.active THEN COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(ds.updated_by_name_snapshot,''),u.username) ELSE '' END,ds.open_vacancies,ds.planned_reserve FROM daily_office_shared ds LEFT JOIN users u ON u.id=ds.updated_by_user_id LEFT JOIN employees e ON e.id=u.employee_id WHERE ds.report_date=$1`, date)
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
		if name == "" {
			name = "Общее значение"
		}
		if row := byOffice[officeID]; row != nil {
			row.Contributions["openVacancies"] = []cellContribution{{Name: name, Value: float64(openVacancies)}}
			row.Contributions["plannedReserve"] = []cellContribution{{Name: name, Value: float64(plannedReserve)}}
		}
	}
	return out, shared.Err()
}

func (a *App) loadRows(ctx context.Context, reportID string) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `SELECT rr.id,o.id,COALESCE(NULLIF(rr.office_name_snapshot,''),o.name),COALESCE(NULLIF(rr.office_sort_order_snapshot,0),o.sort_order),rr.open_vacancies,rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,rr.dismissed_workers,rr.efficiency,
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0)
		FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id JOIN offices o ON o.id=rr.office_id WHERE rr.report_id=$1 ORDER BY COALESCE(NULLIF(rr.office_sort_order_snapshot,0),o.sort_order),COALESCE(NULLIF(rr.office_name_snapshot,''),o.name)`, reportID)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []reportRow{}
	for q.Next() {
		var x reportRow
		if err = q.Scan(&x.ID, &x.OfficeID, &x.OfficeName, &x.SortOrder, &x.OpenVacancies, &x.InvitedCandidates, &x.InterviewedCandidates, &x.Interns, &x.ReserveCandidates, &x.DismissedWorkers, &x.Efficiency, &x.EfficiencyPlan); err != nil {
			return nil, err
		}
		x.Efficiency = Efficiency(x.InterviewedCandidates, x.EfficiencyPlan)
		x.HiredWorkers = []hiredWorker{}
		x.People = map[string][]string{}
		out = append(out, x)
	}
	for i := range out {
		hr, err := a.db.Query(ctx, `SELECT full_name,position FROM hired_workers WHERE report_row_id=$1 ORDER BY created_at,id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for hr.Next() {
			var worker hiredWorker
			_ = hr.Scan(&worker.FullName, &worker.Position)
			out[i].HiredWorkers = append(out[i].HiredWorkers, worker)
		}
		hr.Close()
		people, err := a.db.Query(ctx, `SELECT category,full_name FROM report_row_people WHERE report_row_id=$1 ORDER BY category,created_at,id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for people.Next() {
			var category, name string
			if err = people.Scan(&category, &name); err != nil {
				people.Close()
				return nil, err
			}
			out[i].People[category] = append(out[i].People[category], name)
		}
		if err = people.Err(); err != nil {
			people.Close()
			return nil, err
		}
		people.Close()
	}
	return out, q.Err()
}

type rowInput struct {
	OpenVacancies         int                 `json:"openVacancies"`
	PlannedReserve        int                 `json:"plannedReserve"`
	InvitedCandidates     int                 `json:"invitedCandidates"`
	InterviewedCandidates int                 `json:"interviewedCandidates"`
	Interns               int                 `json:"interns"`
	ReserveCandidates     int                 `json:"reserveCandidates"`
	DismissedWorkers      int                 `json:"dismissedWorkers"`
	ResponsibleIDs        []string            `json:"responsibleIds"`
	HiredWorkers          []hiredWorker       `json:"hiredWorkers"`
	People                map[string][]string `json:"people"`
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
	in.People = cleanPeople(in.People)
	if names, ok := in.People["invited_candidates"]; ok {
		in.InvitedCandidates = len(names)
	}
	if names, ok := in.People["interviewed_candidates"]; ok {
		in.InterviewedCandidates = len(names)
	}
	if names, ok := in.People["interns"]; ok {
		in.Interns = len(names)
	}
	if names, ok := in.People["reserve_candidates"]; ok {
		in.ReserveCandidates = len(names)
	}
	if names, ok := in.People["dismissed_workers"]; ok {
		in.DismissedWorkers = len(names)
	}
	var officeID, reportDate string
	var plan int
	if err = tx.QueryRow(ctx, `SELECT rr.office_id,rp.report_date::text,
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0)
		FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id
		WHERE rr.id=$1 AND rp.owner_user_id=$2 AND rp.status='draft'
		AND (rp.report_date=$3 OR EXISTS(SELECT 1 FROM report_access_grants g WHERE g.report_date=rp.report_date AND g.user_id=$2 AND g.expires_at>now()))`, r.PathValue("id"), claims.UserID, localToday()).Scan(&officeID, &reportDate, &plan); err != nil {
		problem(w, 409, "Доступ к редактированию отчёта закрыт")
		return
	}
	eff := Efficiency(in.InterviewedCandidates, plan)
	_, err = tx.Exec(ctx, `INSERT INTO daily_office_shared(report_date,office_id,open_vacancies,planned_reserve,updated_by_user_id,updated_by_name_snapshot)
		SELECT $1,$2,$3,$4,u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username)
		FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$5
		ON CONFLICT(report_date,office_id) DO UPDATE SET open_vacancies=EXCLUDED.open_vacancies,planned_reserve=EXCLUDED.planned_reserve,updated_by_user_id=EXCLUDED.updated_by_user_id,updated_by_name_snapshot=EXCLUDED.updated_by_name_snapshot,updated_at=now()`, reportDate, officeID, in.OpenVacancies, in.PlannedReserve, claims.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE report_rows rr SET open_vacancies=$2,invited_candidates=$3,interviewed_candidates=$4,interns=$5,reserve_candidates=$6,dismissed_workers=$7,efficiency=$8,updated_at=now() FROM reports r WHERE rr.report_id=r.id AND rr.id=$1 AND r.status='draft' AND r.owner_user_id=$9 AND r.report_date::text=$10`, r.PathValue("id"), 0, in.InvitedCandidates, in.InterviewedCandidates, in.Interns, in.ReserveCandidates, in.DismissedWorkers, eff, claims.UserID, reportDate)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		problem(w, 409, "Доступ к редактированию отчёта закрыт")
		return
	}
	_, _ = tx.Exec(ctx, `DELETE FROM report_row_responsibles WHERE report_row_id=$1`, r.PathValue("id"))
	for _, id := range in.ResponsibleIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO report_row_responsibles VALUES($1,$2) ON CONFLICT DO NOTHING`, r.PathValue("id"), id); err != nil {
			problem(w, 422, "Не найден выбранный сотрудник")
			return
		}
	}
	for category, names := range in.People {
		if _, err = tx.Exec(ctx, `DELETE FROM report_row_people WHERE report_row_id=$1 AND category=$2`, r.PathValue("id"), category); err != nil {
			serverError(w, err)
			return
		}
		for _, name := range names {
			if _, err = tx.Exec(ctx, `INSERT INTO report_row_people(report_row_id,category,full_name) VALUES($1,$2,$3)`, r.PathValue("id"), category, name); err != nil {
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
	a.reports.send(reportDate, map[string]any{"type": "report_updated", "date": reportDate, "officeId": officeID, "field": "openVacancies", "value": in.OpenVacancies, "updatedBy": claims.Username})
	a.reports.send(reportDate, map[string]any{"type": "report_updated", "date": reportDate, "officeId": officeID, "field": "plannedReserve", "value": in.PlannedReserve, "updatedBy": claims.Username})
	jsonOut(w, 200, map[string]any{"efficiency": eff})
}

func cleanHiredWorkers(input []hiredWorker) []hiredWorker {
	workers := make([]hiredWorker, 0, len(input))
	for _, worker := range input {
		worker.FullName = strings.TrimSpace(worker.FullName)
		worker.Position = strings.TrimSpace(worker.Position)
		if worker.FullName != "" {
			workers = append(workers, worker)
		}
	}
	return workers
}

func hasNegative(x rowInput) bool {
	return x.OpenVacancies < 0 || x.PlannedReserve < 0 || x.InvitedCandidates < 0 || x.InterviewedCandidates < 0 || x.Interns < 0 || x.ReserveCandidates < 0 || x.DismissedWorkers < 0
}
func nonEmpty(v []string) []string {
	o := []string{}
	for _, x := range v {
		if x = strings.TrimSpace(x); x != "" {
			o = append(o, x)
		}
	}
	return o
}

var peopleCategories = map[string]struct{}{
	"invited_candidates":     {},
	"interviewed_candidates": {},
	"interns":                {},
	"reserve_candidates":     {},
	"dismissed_workers":      {},
}

func cleanPeople(input map[string][]string) map[string][]string {
	out := make(map[string][]string, len(input))
	for category, names := range input {
		if _, ok := peopleCategories[category]; ok {
			out[category] = nonEmpty(names)
		}
	}
	return out
}

func (a *App) completeReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := claimsFrom(ctx)
	if claims.Role != "employee" {
		problem(w, 403, "Недостаточно прав")
		return
	}
	tag, err := a.db.Exec(ctx, `UPDATE reports rp SET status='completed',completed_at=now(),updated_at=now()
		WHERE rp.id=$1 AND rp.status='draft' AND rp.owner_user_id=$2
		AND (rp.report_date=$3 OR EXISTS(SELECT 1 FROM report_access_grants g WHERE g.report_date=rp.report_date AND g.user_id=$2 AND g.expires_at>now()))`, r.PathValue("id"), claims.UserID, localToday())
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		problem(w, 409, "Отчёт уже завершён или доступ к нему закрыт")
		return
	}
	_, _ = a.db.Exec(ctx, `INSERT INTO audit_log(action,entity_type,entity_id) VALUES('report.completed','report',$1)`, r.PathValue("id"))
	jsonOut(w, 200, map[string]string{"status": "completed"})
}

func totals(rows []reportRow, plan int) map[string]any {
	t := map[string]any{"openVacancies": 0, "plannedReserve": 0, "invitedCandidates": 0, "interviewedCandidates": 0, "interns": 0, "reserveCandidates": 0, "dismissedWorkers": 0, "efficiencyPlan": plan}
	for _, x := range rows {
		t["openVacancies"] = t["openVacancies"].(int) + x.OpenVacancies
		t["plannedReserve"] = t["plannedReserve"].(int) + x.PlannedReserve
		t["invitedCandidates"] = t["invitedCandidates"].(int) + x.InvitedCandidates
		t["interviewedCandidates"] = t["interviewedCandidates"].(int) + x.InterviewedCandidates
		t["interns"] = t["interns"].(int) + x.Interns
		t["reserveCandidates"] = t["reserveCandidates"].(int) + x.ReserveCandidates
		t["dismissedWorkers"] = t["dismissedWorkers"].(int) + x.DismissedWorkers
	}
	t["efficiency"] = Efficiency(t["interviewedCandidates"].(int), plan)
	return t
}
