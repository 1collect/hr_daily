package app

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type traineeCorrectionDetail struct {
	FullName    string    `json:"fullName"`
	Category    string    `json:"category"`
	ReportDate  string    `json:"reportDate"`
	Department  string    `json:"department"`
	Responsible string    `json:"responsible"`
	AddedAt     time.Time `json:"addedAt"`
	ReportRowID string    `json:"reportRowId"`
}

type traineeCorrectionRequest struct {
	ID                string                  `json:"id"`
	ReportDate        string                  `json:"reportDate"`
	Department        string                  `json:"department"`
	ReporterID        int                     `json:"reporterId"`
	DebtsterTraineeID *int                    `json:"debtsterTraineeId,omitempty"`
	DebtsterFullName  string                  `json:"debtsterFullName"`
	DebtsterSource    string                  `json:"debtsterSource"`
	LocalRecord       traineeCorrectionDetail `json:"localRecord"`
	ProposedChange    map[string]any          `json:"proposedChange"`
	Note              string                  `json:"note"`
	Status            string                  `json:"status"`
	DebtsterNote      string                  `json:"debtsterNote"`
	RequestedBy       string                  `json:"requestedBy,omitempty"`
	CreatedAt         time.Time               `json:"createdAt"`
	ProcessedAt       *time.Time              `json:"processedAt,omitempty"`
}

type createTraineeCorrectionRequestInput struct {
	ReportDate        string                  `json:"reportDate"`
	Department        string                  `json:"department"`
	ReporterID        int                     `json:"reporterId"`
	DebtsterTraineeID *int                    `json:"debtsterTraineeId"`
	DebtsterFullName  string                  `json:"debtsterFullName"`
	DebtsterSource    string                  `json:"debtsterSource"`
	LocalRecord       traineeCorrectionDetail `json:"localRecord"`
	ProposedChange    map[string]any          `json:"proposedChange"`
	Note              string                  `json:"note"`
}

type traineeCorrectionResultInput struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (a *App) traineeCorrectionDetails(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	date := strings.TrimSpace(r.URL.Query().Get("reportDate"))
	reportRowID := strings.TrimSpace(r.URL.Query().Get("reportRowId"))
	if name == "" || date == "" {
		problem(w, http.StatusUnprocessableEntity, "Укажите ФИО и дату отчёта")
		return
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		problem(w, http.StatusUnprocessableEntity, "Некорректная дата отчёта")
		return
	}

	rows, err := a.db.Query(r.Context(), `
		SELECT p.full_name, p.category, r.report_date::text,
		       COALESCE(o.name, rr.office_name_snapshot, rr.debtster_department_name, 'Не указан'),
		       COALESCE(string_agg(DISTINCT NULLIF(trim(concat_ws(' ', e.last_name, e.first_name, e.middle_name)), ''), ', '), ''),
		       min(p.created_at), rr.id::text
		FROM report_row_people p
		JOIN report_rows rr ON rr.id = p.report_row_id
		JOIN reports r ON r.id = rr.report_id
		LEFT JOIN offices o ON o.id = rr.office_id
		LEFT JOIN users u ON u.id = r.owner_user_id
		LEFT JOIN employees e ON e.id = u.employee_id
		WHERE r.report_date BETWEEN $2::date - 29 AND $2::date
		  AND lower(trim(p.full_name)) = lower(trim($1))
		  AND ($3 = '' OR rr.id::text = $3)
		GROUP BY p.full_name, p.category, r.report_date,
		         COALESCE(o.name, rr.office_name_snapshot, rr.debtster_department_name, 'Не указан'), rr.id
		ORDER BY r.report_date DESC, min(p.created_at) DESC`, name, date, reportRowID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []traineeCorrectionDetail{}
	for rows.Next() {
		var item traineeCorrectionDetail
		if err = rows.Scan(&item.FullName, &item.Category, &item.ReportDate, &item.Department, &item.Responsible, &item.AddedAt, &item.ReportRowID); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusOK, items)
}

func (a *App) createTraineeCorrectionRequest(w http.ResponseWriter, r *http.Request) {
	var input createTraineeCorrectionRequestInput
	if !decode(w, r, &input) {
		return
	}
	input.ReportDate = strings.TrimSpace(input.ReportDate)
	input.Department = strings.TrimSpace(input.Department)
	input.DebtsterFullName = strings.TrimSpace(input.DebtsterFullName)
	input.DebtsterSource = strings.ToUpper(strings.TrimSpace(input.DebtsterSource))
	input.Note = strings.TrimSpace(input.Note)
	if _, err := time.Parse("2006-01-02", input.ReportDate); err != nil || input.Department == "" || input.DebtsterFullName == "" {
		problem(w, http.StatusUnprocessableEntity, "Некорректные данные стажёра")
		return
	}
	if input.DebtsterSource != "ДИР" {
		problem(w, http.StatusUnprocessableEntity, "Корректировку можно отправить только для источника ДИР")
		return
	}
	if strings.TrimSpace(input.LocalRecord.FullName) == "" || len(input.ProposedChange) == 0 {
		problem(w, http.StatusUnprocessableEntity, "Не выбрана запись из нашей базы или исправление")
		return
	}
	if len([]rune(input.Note)) > 1000 {
		problem(w, http.StatusUnprocessableEntity, "Комментарий не должен превышать 1000 символов")
		return
	}
	localJSON, err := json.Marshal(input.LocalRecord)
	if err != nil {
		serverError(w, err)
		return
	}
	changeJSON, err := json.Marshal(input.ProposedChange)
	if err != nil {
		serverError(w, err)
		return
	}
	claims := claimsFrom(r.Context())
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO trainee_correction_requests
		(report_date,department,reporter_id,debtster_trainee_id,debtster_full_name,debtster_source,local_record,proposed_change,note,requested_by)
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10) RETURNING id::text`,
		input.ReportDate, input.Department, input.ReporterID, input.DebtsterTraineeID, input.DebtsterFullName, input.DebtsterSource,
		localJSON, changeJSON, input.Note, claims.UserID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows || strings.Contains(err.Error(), "trainee_correction_requests_pending_uidx") {
			problem(w, http.StatusConflict, "Такой запрос уже отправлен и ожидает обработки")
			return
		}
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusCreated, map[string]any{"id": id, "status": "pending"})
}

func (a *App) traineeCorrectionRequestsForUser(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r.Context())
	query := `SELECT id::text,report_date::text,department,COALESCE(reporter_id,0),debtster_trainee_id,
		debtster_full_name,debtster_source,local_record,proposed_change,note,status,debtster_note,
		COALESCE(requested_by::text,''),created_at,processed_at
		FROM trainee_correction_requests WHERE ($1 OR requested_by=$2::uuid) ORDER BY created_at DESC LIMIT 500`
	rows, err := a.db.Query(r.Context(), query, isManager(claims), claims.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []traineeCorrectionRequest{}
	for rows.Next() {
		var item traineeCorrectionRequest
		var localJSON, changeJSON []byte
		if err = rows.Scan(&item.ID, &item.ReportDate, &item.Department, &item.ReporterID, &item.DebtsterTraineeID, &item.DebtsterFullName, &item.DebtsterSource, &localJSON, &changeJSON, &item.Note, &item.Status, &item.DebtsterNote, &item.RequestedBy, &item.CreatedAt, &item.ProcessedAt); err != nil {
			serverError(w, err)
			return
		}
		if err = json.Unmarshal(localJSON, &item.LocalRecord); err != nil {
			serverError(w, err)
			return
		}
		if err = json.Unmarshal(changeJSON, &item.ProposedChange); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusOK, items)
}

func (a *App) debtsterIntegrationAuthorized(w http.ResponseWriter, r *http.Request) bool {
	key := strings.TrimSpace(a.debtsterIntegrationKey)
	provided := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key == "" {
		problem(w, http.StatusServiceUnavailable, "Интеграция с Debtster не настроена")
		return false
	}
	if provided == "" || subtle.ConstantTimeCompare([]byte(key), []byte(provided)) != 1 {
		w.Header().Set("WWW-Authenticate", "ApiKey")
		problem(w, http.StatusUnauthorized, "Неверный API key")
		return false
	}
	return true
}

func (a *App) debtsterCorrectionRequests(w http.ResponseWriter, r *http.Request) {
	if !a.debtsterIntegrationAuthorized(w, r) {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "pending"
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,report_date::text,department,COALESCE(reporter_id,0),debtster_trainee_id,
		debtster_full_name,debtster_source,local_record,proposed_change,note,status,debtster_note,COALESCE(requested_by::text,''),created_at,processed_at
		FROM trainee_correction_requests WHERE status=$1 ORDER BY created_at LIMIT $2`, status, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []traineeCorrectionRequest{}
	for rows.Next() {
		var item traineeCorrectionRequest
		var localJSON, changeJSON []byte
		if err = rows.Scan(&item.ID, &item.ReportDate, &item.Department, &item.ReporterID, &item.DebtsterTraineeID, &item.DebtsterFullName, &item.DebtsterSource, &localJSON, &changeJSON, &item.Note, &item.Status, &item.DebtsterNote, &item.RequestedBy, &item.CreatedAt, &item.ProcessedAt); err != nil {
			serverError(w, err)
			return
		}
		if err = json.Unmarshal(localJSON, &item.LocalRecord); err != nil {
			serverError(w, err)
			return
		}
		if err = json.Unmarshal(changeJSON, &item.ProposedChange); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) debtsterCorrectionClaim(w http.ResponseWriter, r *http.Request) {
	if !a.debtsterIntegrationAuthorized(w, r) {
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE trainee_correction_requests SET status='processing',updated_at=now() WHERE id=$1 AND status='pending'`, r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, http.StatusConflict, "Запрос уже взят в работу или не найден")
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "status": "processing"})
}

func (a *App) debtsterCorrectionResult(w http.ResponseWriter, r *http.Request) {
	if !a.debtsterIntegrationAuthorized(w, r) {
		return
	}
	var input traineeCorrectionResultInput
	if !decode(w, r, &input) {
		return
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status != "applied" && input.Status != "rejected" && input.Status != "failed" {
		problem(w, http.StatusUnprocessableEntity, "Недопустимый статус результата")
		return
	}
	if len([]rune(input.Note)) > 1000 {
		problem(w, 422, "Комментарий слишком длинный")
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE trainee_correction_requests SET status=$2,debtster_note=$3,processed_at=now(),updated_at=now() WHERE id=$1 AND status IN ('pending','processing')`, r.PathValue("id"), input.Status, strings.TrimSpace(input.Note))
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, http.StatusConflict, "Запрос уже обработан или не найден")
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "status": input.Status})
}
