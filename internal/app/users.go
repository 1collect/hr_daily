package app

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type userRecord struct {
	ID           string `json:"id"`
	EmployeeID   string `json:"employeeId"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	MiddleName   string `json:"middleName"`
	Active       bool   `json:"active"`
	Plan         int    `json:"plan"`
	PlanFrom     string `json:"planFrom"`
	NextPlan     int    `json:"nextPlan"`
	NextPlanFrom string `json:"nextPlanFrom"`
}

type userInput struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Role       string `json:"role"`
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	MiddleName string `json:"middleName"`
	Active     bool   `json:"active"`
	Plan       int    `json:"plan"`
	PlanFrom   string `json:"planFrom"`
}

func (a *App) users(w http.ResponseWriter, r *http.Request) {
	c, ok := requireManager(w, r)
	if !ok {
		return
	}
	context := strings.TrimSpace(r.URL.Query().Get("context"))
	roleFilter := ""
	if c.Role != "superadmin" || context == "report" {
		roleFilter = "employee"
	}
	q, err := a.db.Query(r.Context(), `SELECT u.id,e.id,u.username,u.role,e.first_name,e.last_name,e.middle_name,u.active,
		COALESCE(p.plan_count,0),COALESCE(p.effective_from::text,''),COALESCE(np.plan_count,0),COALESCE(np.effective_from::text,'')
		FROM users u JOIN employees e ON e.id=u.employee_id
		LEFT JOIN LATERAL (SELECT plan_count,effective_from FROM employee_efficiency_plans WHERE user_id=u.id AND effective_from<=CURRENT_DATE ORDER BY effective_from DESC LIMIT 1) p ON true
		LEFT JOIN LATERAL (SELECT plan_count,effective_from FROM employee_efficiency_plans WHERE user_id=u.id AND effective_from>CURRENT_DATE ORDER BY effective_from ASC LIMIT 1) np ON true
		WHERE NOT u.system AND u.active AND ($1='' OR u.role=$1)
		ORDER BY e.last_name,e.first_name`, roleFilter)
	if err != nil {
		serverError(w, err)
		return
	}
	defer q.Close()
	out := []userRecord{}
	for q.Next() {
		var x userRecord
		if err = q.Scan(&x.ID, &x.EmployeeID, &x.Username, &x.Role, &x.FirstName, &x.LastName, &x.MiddleName, &x.Active, &x.Plan, &x.PlanFrom, &x.NextPlan, &x.NextPlanFrom); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, x)
	}
	jsonOut(w, 200, out)
}

func validateUserInput(in userInput, creating bool) string {
	in.Username = strings.TrimSpace(in.Username)
	if len([]rune(in.Username)) < 3 {
		return "Логин должен содержать минимум 3 символа"
	}
	if strings.TrimSpace(in.FirstName) == "" || strings.TrimSpace(in.LastName) == "" {
		return "Имя и фамилия обязательны"
	}
	if in.Role != "admin" && in.Role != "employee" {
		return "Недопустимая роль"
	}
	if creating && len(in.Password) < 8 {
		return "Пароль должен содержать минимум 8 символов"
	}
	if in.Password != "" && len(in.Password) < 8 {
		return "Пароль должен содержать минимум 8 символов"
	}
	if creating && in.Role == "employee" {
		if in.Plan < 0 {
			return "План не может быть отрицательным"
		}
		if in.Plan > 0 && !validDate(in.PlanFrom) {
			return "Укажите дату начала действия плана"
		}
	}
	return ""
}

func (a *App) createUser(w http.ResponseWriter, r *http.Request) {
	c, ok := requireManager(w, r)
	if !ok {
		return
	}
	var in userInput
	if !decode(w, r, &in) {
		return
	}
	if c.Role != "superadmin" {
		in.Role = "employee"
	}
	if msg := validateUserInput(in, true); msg != "" {
		problem(w, 422, msg)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		serverError(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var employeeID, userID string
	err = tx.QueryRow(r.Context(), `INSERT INTO employees(first_name,last_name,middle_name,active) VALUES($1,$2,$3,true) RETURNING id`, strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName), strings.TrimSpace(in.MiddleName)).Scan(&employeeID)
	if err != nil {
		problem(w, 409, "Такой сотрудник уже существует")
		return
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO users(employee_id,username,password_hash,role,active) VALUES($1,$2,$3,$4,true) RETURNING id`, employeeID, strings.TrimSpace(in.Username), string(hash), in.Role).Scan(&userID)
	if err != nil {
		problem(w, 409, "Такой логин уже используется")
		return
	}
	if in.Role == "employee" && in.Plan > 0 {
		if _, err = tx.Exec(r.Context(), `INSERT INTO employee_efficiency_plans(user_id,plan_count,effective_from) VALUES($1,$2,$3)`, userID, in.Plan, in.PlanFrom); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.log(r.Context(), "user.created", "user", userID)
	jsonOut(w, 201, userRecord{ID: userID, EmployeeID: employeeID, Username: strings.TrimSpace(in.Username), Role: in.Role, FirstName: strings.TrimSpace(in.FirstName), LastName: strings.TrimSpace(in.LastName), MiddleName: strings.TrimSpace(in.MiddleName), Active: true, Plan: in.Plan, PlanFrom: in.PlanFrom})
}

func (a *App) createUserPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	var in struct {
		Plan     int    `json:"plan"`
		PlanFrom string `json:"planFrom"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Plan < 0 {
		problem(w, 422, "План не может быть отрицательным")
		return
	}
	if !validDate(in.PlanFrom) {
		problem(w, 422, "Укажите корректную дату начала действия плана")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='employee' AND active AND NOT system)`, r.PathValue("id")).Scan(&allowed); err != nil {
		serverError(w, err)
		return
	}
	if !allowed {
		problem(w, 404, "Сотрудник не найден")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO employee_efficiency_plans(user_id,plan_count,effective_from) VALUES($1,$2,$3)
		ON CONFLICT(user_id,effective_from) DO UPDATE SET plan_count=EXCLUDED.plan_count`, r.PathValue("id"), in.Plan, in.PlanFrom)
	if err != nil {
		serverError(w, err)
		return
	}
	_, err = tx.Exec(ctx, `UPDATE report_rows rr SET efficiency=CASE WHEN $3>0 THEN round(rr.interviewed_candidates*100.0/$3,2) ELSE 0 END,updated_at=now()
		FROM reports rp WHERE rp.id=rr.report_id AND rp.owner_user_id=$1 AND rp.report_date>=$2
		AND NOT EXISTS (SELECT 1 FROM employee_efficiency_plans later WHERE later.user_id=$1 AND later.effective_from>$2 AND later.effective_from<=rp.report_date)`, r.PathValue("id"), in.PlanFrom, in.Plan)
	if err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	a.log(ctx, "user.plan.updated", "user", r.PathValue("id"))
	jsonOut(w, 200, in)
}

func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	c, ok := requireManager(w, r)
	if !ok {
		return
	}
	var in userInput
	if !decode(w, r, &in) {
		return
	}
	var employeeID, targetRole string
	var system, targetActive bool
	err := a.db.QueryRow(r.Context(), `SELECT COALESCE(employee_id::text,''),role,system,active FROM users WHERE id=$1`, r.PathValue("id")).Scan(&employeeID, &targetRole, &system, &targetActive)
	if err == pgx.ErrNoRows {
		problem(w, 404, "Пользователь не найден")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if system {
		problem(w, 403, "Системного суперадминистратора нельзя изменять")
		return
	}
	if !targetActive {
		problem(w, 404, "Пользователь не найден")
		return
	}
	if c.Role != "superadmin" && (targetRole != "employee" || in.Role != "employee") {
		problem(w, 403, "Администратор может управлять только сотрудниками")
		return
	}
	if msg := validateUserInput(in, false); msg != "" {
		problem(w, 422, msg)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `UPDATE employees SET first_name=$2,last_name=$3,middle_name=$4,active=$5 WHERE id=$1`, employeeID, strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName), strings.TrimSpace(in.MiddleName), in.Active)
	if err != nil {
		problem(w, 409, "Не удалось обновить ФИО")
		return
	}
	if in.Password != "" {
		hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if e != nil {
			serverError(w, e)
			return
		}
		_, err = tx.Exec(r.Context(), `UPDATE users SET username=$2,password_hash=$3,role=$4,active=$5,updated_at=now() WHERE id=$1`, r.PathValue("id"), strings.TrimSpace(in.Username), string(hash), in.Role, in.Active)
	} else {
		_, err = tx.Exec(r.Context(), `UPDATE users SET username=$2,role=$3,active=$4,updated_at=now() WHERE id=$1`, r.PathValue("id"), strings.TrimSpace(in.Username), in.Role, in.Active)
	}
	if err != nil {
		problem(w, 409, "Логин уже используется")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.log(r.Context(), "user.updated", "user", r.PathValue("id"))
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (a *App) deleteUser(w http.ResponseWriter, r *http.Request) {
	c, ok := requireManager(w, r)
	if !ok {
		return
	}
	if c.UserID == r.PathValue("id") {
		problem(w, 422, "Нельзя удалить собственную учётную запись")
		return
	}
	var employeeID, role string
	var system bool
	err := a.db.QueryRow(r.Context(), `SELECT COALESCE(employee_id::text,''),role,system FROM users WHERE id=$1`, r.PathValue("id")).Scan(&employeeID, &role, &system)
	if err == pgx.ErrNoRows {
		problem(w, 404, "Пользователь не найден")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if system {
		problem(w, 403, "Суперадминистратора нельзя удалить")
		return
	}
	if c.Role != "superadmin" && role != "employee" {
		problem(w, 403, "Недостаточно прав")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `UPDATE users SET active=false,updated_at=now() WHERE id=$1`, r.PathValue("id"))
	if err == nil && employeeID != "" {
		_, err = tx.Exec(r.Context(), `UPDATE employees SET active=false WHERE id=$1`, employeeID)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.log(r.Context(), "user.archived", "user", r.PathValue("id"))
	jsonOut(w, 200, map[string]bool{"ok": true})
}
