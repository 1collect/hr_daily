package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	DatabaseURL string
	DebtsterAPI string
	HTTPAddr    string
	StaticDir   string
	AppSecret   string
	SuperLogin  string
	SuperPass   string
}

type App struct {
	db          *pgxpool.Pool
	debtsterAPI string
	httpClient  *http.Client
	static      string
	secret      []byte
	progress    *progressHub
	reports     *reportHub
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

	a := &App{db: db, debtsterAPI: strings.TrimRight(cfg.DebtsterAPI, "/"), httpClient: &http.Client{Timeout: 10 * time.Second}, static: cfg.StaticDir, secret: []byte(cfg.AppSecret), progress: &progressHub{latest: map[string]any{}, clients: map[string]map[*websocket.Conn]struct{}{}}, reports: &reportHub{clients: map[string]map[*websocket.Conn]struct{}{}}}
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
	m.HandleFunc("PUT /api/users/{id}/candidate-plans", a.updateCandidatePlans)
	m.HandleFunc("DELETE /api/users/{id}", a.deleteUser)
	m.HandleFunc("GET /api/bootstrap", a.bootstrap)
	m.HandleFunc("GET /api/trainees", a.trainees)
	m.HandleFunc("GET /api/daily-plans", a.dailyPlans)
	m.HandleFunc("PUT /api/daily-plans", a.updateDailyPlans)
	m.HandleFunc("GET /api/report-access", a.reportAccessUsers)
	m.HandleFunc("POST /api/report-access", a.openReportAccess)
	m.HandleFunc("DELETE /api/report-access", a.closeReportAccess)
	m.HandleFunc("GET /api/report-responsibles", a.reportResponsibles)
	m.HandleFunc("PUT /api/report-responsibles", a.updateReportResponsibles)
	m.HandleFunc("PUT /api/report/rows/{id}", a.updateRow)
	m.HandleFunc("POST /api/reports/{id}/complete", a.completeReport)
	m.HandleFunc("GET /api/reports/export", a.exportPeriod)
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

func (a *App) trainees(w http.ResponseWriter, r *http.Request) {
	threshold := 55
	if value := r.URL.Query().Get("threshold"); value != "" {
		var err error
		threshold, err = strconv.Atoi(value)
		if err != nil || threshold < 1 || threshold > 100 {
			problem(w, http.StatusUnprocessableEntity, "Порог поиска должен быть от 1 до 100")
			return
		}
	}
	date := r.URL.Query().Get("report_date")
	if !validDate(date) {
		problem(w, http.StatusUnprocessableEntity, "Дата должна иметь формат YYYY-MM-DD")
		return
	}
	departmentID := 0
	if value := r.URL.Query().Get("department_id"); value != "" {
		var err error
		departmentID, err = strconv.Atoi(value)
		if err != nil || departmentID <= 0 {
			problem(w, http.StatusUnprocessableEntity, "department_id должен быть положительным целым числом")
			return
		}
	}
	rows, err := fetchDebtsterTrainees(r.Context(), a.httpClient, a.debtsterAPI, date, departmentID)
	if err != nil {
		serverError(w, err)
		return
	}
	if len(rows) > 0 {
		names, err := a.loadTraineeSearchNames(r.Context(), date)
		if err != nil {
			serverError(w, err)
			return
		}
		index := newTraineeNameIndex(names)
		cache := map[string][]traineeMatch{}
		for i := range rows {
			name := rows[i].FullName
			matches, ok := cache[name]
			if !ok {
				matches = index.search(name, threshold)
				cache[name] = matches
			}
			rows[i].Matches = matches
		}
	}
	jsonOut(w, http.StatusOK, rows)
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
	ID                     string                        `json:"id"`
	OfficeID               string                        `json:"officeId"`
	OfficeName             string                        `json:"officeName"`
	SortOrder              int                           `json:"sortOrder"`
	OpenVacancies          int                           `json:"openVacancies"`
	PlannedReserve         int                           `json:"plannedReserve"`
	InvitedCandidates      int                           `json:"invitedCandidates"`
	InterviewedCandidates  int                           `json:"interviewedCandidates"`
	Interns                int                           `json:"interns"`
	ReserveCandidates      int                           `json:"reserveCandidates"`
	DismissedWorkers       int                           `json:"dismissedWorkers"`
	StaffPositionsCount    int                           `json:"staffPositionsCount"`
	ActiveEmployeesCount   int                           `json:"activeEmployeesCount"`
	VacantPositionsCount   int                           `json:"vacantPositionsCount"`
	TraineesCount          int                           `json:"traineesCount"`
	TraineesCountChange    int                           `json:"traineesCountChange"`
	RecruitmentCount       int                           `json:"recruitmentCount"`
	PlannedDismissalsCount int                           `json:"plannedDismissalsCount"`
	PlannedDismissals      []debtsterPlannedDismissal    `json:"plannedDismissals"`
	Efficiency             float64                       `json:"efficiency"`
	EfficiencyPlan         int                           `json:"efficiencyPlan"`
	InvitationPlan         int                           `json:"invitationPlan"`
	HiringPlan             int                           `json:"hiringPlan"`
	InvitationEfficiency   float64                       `json:"invitationEfficiency"`
	HiringEfficiency       float64                       `json:"hiringEfficiency"`
	HiredWorkers           []hiredWorker                 `json:"hiredWorkers"`
	HiredDetails           []hiredDetail                 `json:"hiredDetails,omitempty"`
	People                 map[string][]string           `json:"people"`
	PeopleDetails          map[string][]hiredDetail      `json:"peopleDetails,omitempty"`
	Contributions          map[string][]cellContribution `json:"contributions,omitempty"`
	ResponsibleCount       int                           `json:"responsibleCount"`
	Assigned               bool                          `json:"assigned"`
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

func Efficiency(actual, plan int) float64 {
	if plan <= 0 {
		return 0
	}
	return float64(actual) * 100 / float64(plan)
}

type candidatePlans struct {
	Invited int `json:"invited"`
	Hired   int `json:"hired"`
}

func (a *App) candidatePlanTotals(ctx context.Context, date, ownerID, reportType string) (candidatePlans, error) {
	invitedTable, hiredTable := "employee_invitation_plans", "employee_efficiency_plans"
	if reportType == "main_office" {
		invitedTable, hiredTable = "main_office_employee_invitation_plans", "main_office_employee_efficiency_plans"
	}
	var plans candidatePlans
	query := `SELECT COALESCE(sum(COALESCE(d.invited_plan_count,
		(SELECT p.plan_count FROM ` + invitedTable + ` p WHERE p.user_id=u.id AND p.effective_from<=$1::date ORDER BY p.effective_from DESC LIMIT 1),0)),0)::int,
		COALESCE(sum(COALESCE(d.plan_count,
		(SELECT p.plan_count FROM ` + hiredTable + ` p WHERE p.user_id=u.id AND p.effective_from<=$1::date ORDER BY p.effective_from DESC LIMIT 1),0)),0)::int
		FROM users u LEFT JOIN daily_efficiency_plan_overrides d ON d.user_id=u.id AND d.report_date=$1::date AND d.report_type=$3
		WHERE u.role='employee' AND u.active AND NOT u.system AND ($2='' OR u.id::text=$2)`
	err := a.db.QueryRow(ctx, query, date, ownerID, reportType).Scan(&plans.Invited, &plans.Hired)
	return plans, err
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
	today := localToday()
	usesDebtster := usesDebtsterDepartments(date)
	shouldSyncDebtster := shouldSyncDebtsterDepartments(date, today)
	var departments []debtsterDepartment
	var vacancies []debtsterVacancyReport
	vacancies, vacanciesErr := fetchDebtsterVacancies(ctx, a.httpClient, a.debtsterAPI, date)
	if vacanciesErr != nil {
		log.Printf("read Debtster vacancies for %s: %v", date, vacanciesErr)
	}
	traineeBaselines, err := a.loadDebtsterTraineeBaselines(ctx, date, vacancies)
	if err != nil {
		serverError(w, err)
		return
	}
	if shouldSyncDebtster {
		departments, err = fetchDebtsterDepartments(ctx, a.httpClient, a.debtsterAPI)
		if err != nil {
			log.Printf("sync Debtster departments: %v", err)
		}
	}
	claims := claimsFrom(ctx)
	if isManager(claims) {
		if shouldSyncDebtster {
			var err error
			departments, err = a.syncDebtsterReportRows(ctx, date, departments)
			if err != nil {
				serverError(w, err)
				return
			}
		}
		employeeID := strings.TrimSpace(r.URL.Query().Get("employeeId"))
		if employeeID != "" {
			var active bool
			_ = a.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id=$1 AND role='employee' AND active AND NOT system)`, employeeID).Scan(&active)
			if !active {
				problem(w, 404, "Сотрудник не найден")
				return
			}
		}
		plans, err := a.candidatePlanTotals(ctx, date, employeeID, "rp")
		if err != nil {
			serverError(w, err)
			return
		}
		rows, err := a.loadAggregateRows(ctx, date, employeeID, plans)
		if err != nil {
			serverError(w, err)
			return
		}
		rows = appendMissingDebtsterRows(rows, departments, plans.Hired)
		rows = appendMissingDebtsterVacancyRows(rows, vacancies, plans.Hired)
		applyCandidatePlans(rows, plans)
		applyDebtsterVacancies(rows, vacancies)
		applyDebtsterTraineeChanges(rows, vacancies, traineeBaselines)
		if err = a.applyResponsibleCounts(ctx, date, "rp", rows); err != nil {
			serverError(w, err)
			return
		}
		var accessCount int
		_ = a.db.QueryRow(ctx, `SELECT count(*) FROM report_access_grants WHERE report_date=$1 AND expires_at>now()`, date).Scan(&accessCount)
		jsonOut(w, 200, map[string]any{"report": map[string]any{"id": "", "date": date, "status": "read_only", "editable": false, "accessCount": accessCount}, "rows": rows, "plan": plans.Hired, "invitationPlan": plans.Invited, "hiringPlan": plans.Hired, "totals": totals(rows, plans)})
		return
	}
	var reportID, status string
	err = a.db.QueryRow(ctx, `INSERT INTO reports(report_date,owner_user_id,owner_name_snapshot)
		SELECT $1,u.id,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),u.username)
		FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$2
		ON CONFLICT(report_date,owner_user_id) WHERE owner_user_id IS NOT NULL DO UPDATE SET report_date=EXCLUDED.report_date
		RETURNING id,status`, date, claims.UserID).Scan(&reportID, &status)
	if err != nil {
		serverError(w, err)
		return
	}
	if shouldSyncDebtster {
		if _, err = a.syncDebtsterReportRows(ctx, date, departments); err != nil {
			serverError(w, err)
			return
		}
	} else if !usesDebtster {
		_, err = a.db.Exec(ctx, `INSERT INTO report_rows(report_id,office_id,office_name_snapshot,office_sort_order_snapshot,debtster_department_id,debtster_department_name)
			SELECT $1,o.id,o.name,o.sort_order,o.debtster_department_id,o.debtster_department_name FROM offices o
			WHERE o.active ON CONFLICT DO NOTHING`, reportID)
		if err != nil {
			serverError(w, err)
			return
		}
	}
	rows, err := a.loadRows(ctx, reportID)
	if err != nil {
		serverError(w, err)
		return
	}
	rows = appendMissingDebtsterVacancyRows(rows, vacancies, 0)
	if err = a.markAssignedRows(ctx, date, "rp", claims.UserID, rows); err != nil {
		serverError(w, err)
		return
	}
	applyDebtsterVacancies(rows, vacancies)
	applyDebtsterTraineeChanges(rows, vacancies, traineeBaselines)
	plans, err := a.candidatePlanTotals(ctx, date, claims.UserID, "rp")
	if err != nil {
		serverError(w, err)
		return
	}
	editable := status == "draft" && a.hasReportEditAccess(ctx, claims.UserID, date)
	applyCandidatePlans(rows, plans)
	jsonOut(w, 200, map[string]any{"report": map[string]any{"id": reportID, "date": date, "status": status, "editable": editable}, "rows": rows, "plan": plans.Hired, "invitationPlan": plans.Invited, "hiringPlan": plans.Hired, "totals": totals(rows, plans)})
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

func localToday() string {
	loc, err := time.LoadLocation("Asia/Qyzylorda")
	if err != nil {
		return time.Now().Format("2006-01-02")
	}
	return time.Now().In(loc).Format("2006-01-02")
}

func (a *App) loadAggregateRows(ctx context.Context, date, ownerID string, plans candidatePlans) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `WITH relevant_reports AS (
		SELECT rp.* FROM reports rp JOIN users u ON u.id=rp.owner_user_id AND u.active AND NOT u.system
		WHERE rp.report_date=$1 AND ($2='' OR rp.owner_user_id::text=$2)
	), hc AS (SELECT report_row_id,count(*) AS n FROM hired_workers GROUP BY report_row_id)
		SELECT COALESCE(rr.debtster_department_id::text,rr.office_id::text),max(rr.office_name_snapshot),min(rr.office_sort_order_snapshot),
		COALESCE(max(rr.open_vacancies),0),COALESCE(max(rr.planned_reserve),0),COALESCE(sum(rr.invited_candidates),0),
		COALESCE(sum(rr.interviewed_candidates),0),COALESCE(sum(rr.interns),0),
		COALESCE(sum(rr.reserve_candidates),0),COALESCE(sum(rr.dismissed_workers),0),COALESCE(sum(hc.n),0)
		FROM relevant_reports rp JOIN report_rows rr ON rr.report_id=rp.id
		LEFT JOIN hc ON hc.report_row_id=rr.id
		GROUP BY COALESCE(rr.debtster_department_id::text,rr.office_id::text)
		ORDER BY min(rr.office_sort_order_snapshot),max(rr.office_name_snapshot)`, date, ownerID)
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
		x.EfficiencyPlan = plans.Hired
		x.HiringPlan = plans.Hired
		x.InvitationPlan = plans.Invited
		x.HiredWorkers = make([]hiredWorker, hires)
		x.HiredDetails = []hiredDetail{}
		x.People = map[string][]string{}
		x.PeopleDetails = map[string][]hiredDetail{}
		x.Contributions = map[string][]cellContribution{}
		x.Contributions["openVacancies"] = []cellContribution{{Name: "Общее значение", Value: float64(x.OpenVacancies)}}
		x.Contributions["plannedReserve"] = []cellContribution{{Name: "Общее значение", Value: float64(x.PlannedReserve)}}
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
		SELECT COALESCE(rr.debtster_department_id::text,rr.office_id::text),COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username),
		rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,
		COALESCE((SELECT d.invited_plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_invitation_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0),
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
		var invited, interviewed, interns, reserve, invitationPlan, hiringPlan, hires, dismissed int
		if err = details.Scan(&officeID, &name, &invited, &interviewed, &interns, &reserve, &invitationPlan, &hiringPlan, &hires, &dismissed); err != nil {
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
		add("invitationEfficiency", Efficiency(invited, invitationPlan))
		add("hiringEfficiency", Efficiency(hires, hiringPlan))
	}
	if err = details.Err(); err != nil {
		return nil, err
	}
	hiredRows, err := a.db.Query(ctx, `SELECT COALESCE(rr.debtster_department_id::text,rr.office_id::text),hw.full_name,hw.position,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
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
	peopleRows, err := a.db.Query(ctx, `SELECT COALESCE(rr.debtster_department_id::text,rr.office_id::text),p.category,p.full_name,COALESCE(NULLIF(trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),''),NULLIF(rp.owner_name_snapshot,''),u.username)
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
	return out, nil
}

func applyCandidatePlans(rows []reportRow, plans candidatePlans) {
	for index := range rows {
		rows[index].InvitationPlan = plans.Invited
		rows[index].HiringPlan = plans.Hired
		rows[index].EfficiencyPlan = plans.Hired
		rows[index].InvitationEfficiency = Efficiency(rows[index].InvitedCandidates, plans.Invited)
		rows[index].HiringEfficiency = Efficiency(len(rows[index].HiredWorkers), plans.Hired)
		rows[index].Efficiency = rows[index].HiringEfficiency
	}
}

func (a *App) loadRows(ctx context.Context, reportID string) ([]reportRow, error) {
	q, err := a.db.Query(ctx, `SELECT rr.id,COALESCE(rr.debtster_department_id::text,rr.office_id::text),rr.office_name_snapshot,rr.office_sort_order_snapshot,rr.open_vacancies,rr.planned_reserve,rr.invited_candidates,rr.interviewed_candidates,rr.interns,rr.reserve_candidates,rr.dismissed_workers,rr.efficiency,
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0)
		FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id WHERE rr.report_id=$1 ORDER BY rr.office_sort_order_snapshot,rr.office_name_snapshot`, reportID)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	out := []reportRow{}
	for q.Next() {
		var x reportRow
		if err = q.Scan(&x.ID, &x.OfficeID, &x.OfficeName, &x.SortOrder, &x.OpenVacancies, &x.PlannedReserve, &x.InvitedCandidates, &x.InterviewedCandidates, &x.Interns, &x.ReserveCandidates, &x.DismissedWorkers, &x.Efficiency, &x.EfficiencyPlan); err != nil {
			return nil, err
		}
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
	var invitationPlan, hiringPlan int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(rr.debtster_department_id::text,rr.office_id::text),rp.report_date::text,
		COALESCE((SELECT d.invited_plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_invitation_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0),
		COALESCE((SELECT d.plan_count FROM daily_efficiency_plan_overrides d WHERE d.user_id=rp.owner_user_id AND d.report_date=rp.report_date AND d.report_type='rp'),(SELECT plan_count FROM employee_efficiency_plans p WHERE p.user_id=rp.owner_user_id AND p.effective_from<=rp.report_date ORDER BY p.effective_from DESC LIMIT 1),0)
		FROM report_rows rr JOIN reports rp ON rp.id=rr.report_id
		WHERE rr.id=$1 AND rp.owner_user_id=$2 AND rp.status='draft'
		AND EXISTS(SELECT 1 FROM report_unit_responsibles a WHERE a.report_date=rp.report_date AND a.report_type='rp' AND a.unit_id=COALESCE(rr.debtster_department_id::text,rr.office_id::text) AND a.user_id=$2)
		AND (rp.report_date=$3 OR EXISTS(SELECT 1 FROM report_access_grants g WHERE g.report_date=rp.report_date AND g.user_id=$2 AND g.expires_at>now()))`, r.PathValue("id"), claims.UserID, localToday()).Scan(&officeID, &reportDate, &invitationPlan, &hiringPlan); err != nil {
		problem(w, 409, "Доступ к редактированию отчёта закрыт")
		return
	}
	cleanHires := cleanHiredWorkers(in.HiredWorkers)
	eff := Efficiency(len(cleanHires), hiringPlan)
	_, err = tx.Exec(ctx, `UPDATE report_rows target
		SET open_vacancies=$2,planned_reserve=$3,updated_at=now()
		FROM reports target_report,report_rows source
		WHERE source.id=$1 AND target.report_id=target_report.id AND target_report.report_date=$4
		AND ((source.debtster_department_id IS NOT NULL AND target.debtster_department_id=source.debtster_department_id)
		  OR (source.debtster_department_id IS NULL AND target.debtster_department_id IS NULL AND target.office_id=source.office_id))`,
		r.PathValue("id"), in.OpenVacancies, in.PlannedReserve, reportDate)
	if err != nil {
		serverError(w, err)
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE report_rows rr SET invited_candidates=$2,interviewed_candidates=$3,interns=$4,reserve_candidates=$5,dismissed_workers=$6,efficiency=$7,updated_at=now() FROM reports r WHERE rr.report_id=r.id AND rr.id=$1 AND r.status='draft' AND r.owner_user_id=$8 AND r.report_date::text=$9`, r.PathValue("id"), in.InvitedCandidates, in.InterviewedCandidates, in.Interns, in.ReserveCandidates, in.DismissedWorkers, eff, claims.UserID, reportDate)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		problem(w, 409, "Доступ к редактированию отчёта закрыт")
		return
	}
	_, _ = tx.Exec(ctx, `DELETE FROM hired_workers WHERE report_row_id=$1`, r.PathValue("id"))
	for _, worker := range cleanHires {
		if _, err = tx.Exec(ctx, `INSERT INTO hired_workers(report_row_id,full_name,position) VALUES($1,$2,$3)`, r.PathValue("id"), worker.FullName, worker.Position); err != nil {
			serverError(w, err)
			return
		}
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
	jsonOut(w, 200, map[string]any{"efficiency": eff, "invitationEfficiency": Efficiency(in.InvitedCandidates, invitationPlan), "hiringEfficiency": eff, "hiredCount": len(cleanHires)})
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

func totals(rows []reportRow, plans candidatePlans) map[string]any {
	t := map[string]any{"openVacancies": 0, "plannedReserve": 0, "invitedCandidates": 0, "interviewedCandidates": 0, "interns": 0, "reserveCandidates": 0, "hiredWorkers": 0, "dismissedWorkers": 0, "efficiencyPlan": plans.Hired, "invitationPlan": plans.Invited, "hiringPlan": plans.Hired}
	for _, x := range rows {
		t["openVacancies"] = t["openVacancies"].(int) + x.OpenVacancies
		t["plannedReserve"] = t["plannedReserve"].(int) + x.PlannedReserve
		t["invitedCandidates"] = t["invitedCandidates"].(int) + x.InvitedCandidates
		t["interviewedCandidates"] = t["interviewedCandidates"].(int) + x.InterviewedCandidates
		t["interns"] = t["interns"].(int) + x.Interns
		t["reserveCandidates"] = t["reserveCandidates"].(int) + x.ReserveCandidates
		t["hiredWorkers"] = t["hiredWorkers"].(int) + len(x.HiredWorkers)
		t["dismissedWorkers"] = t["dismissedWorkers"].(int) + x.DismissedWorkers
	}
	t["invitationEfficiency"] = Efficiency(t["invitedCandidates"].(int), plans.Invited)
	t["hiringEfficiency"] = Efficiency(t["hiredWorkers"].(int), plans.Hired)
	t["efficiency"] = t["hiringEfficiency"]
	return t
}
