package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type mainOfficeVacancy struct {
	ID              string `json:"id"`
	BitrixRequestID int64  `json:"bitrixRequestId"`
	RequestTitle    string `json:"requestTitle"`
	VacancyTitle    string `json:"vacancyTitle"`
	RequestedCount  int    `json:"requestedCount"`
	HiredCount      int    `json:"hiredCount"`
	IsClosed        bool   `json:"isClosed"`
	CreatedTime     string `json:"createdTime"`
	Initiator       string `json:"initiator"`
	Responsible     string `json:"responsible"`
	DepartmentCode  string `json:"departmentCode"`
	Details         string `json:"details"`
}

func (a *App) mainOfficeVacancies(w http.ResponseWriter, r *http.Request) {
	var syncError string
	if a.bitrixWebhookBaseURL != "" {
		if err := a.syncBitrixHRVacancies(r.Context()); err != nil {
			syncError = err.Error()
		}
	}
	rows, err := a.loadMainOfficeVacancies(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"items": rows, "syncError": syncError})
}

func (a *App) syncBitrixHRVacancies(ctx context.Context) error {
	items, err := a.fetchBitrixHRRequests(ctx)
	if err != nil {
		return err
	}
	a.resolveBitrixRequestUsers(ctx, items)
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, item := range items {
		payload := item.RawPayload
		if len(payload) == 0 {
			payload, err = json.Marshal(item)
			if err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO bitrix_hr_requests(
				bitrix_request_id,title,stage_id,created_time,created_by_id,created_by_name,
				assigned_by_id,assigned_by_name,department_code,details,raw_payload,synced_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
			ON CONFLICT(bitrix_request_id) DO UPDATE SET
				title=EXCLUDED.title,stage_id=EXCLUDED.stage_id,created_time=EXCLUDED.created_time,
				created_by_id=EXCLUDED.created_by_id,created_by_name=EXCLUDED.created_by_name,
				assigned_by_id=EXCLUDED.assigned_by_id,assigned_by_name=EXCLUDED.assigned_by_name,
				department_code=EXCLUDED.department_code,details=EXCLUDED.details,
				raw_payload=EXCLUDED.raw_payload,synced_at=now()
		`, item.ID, item.Title, item.StageID, item.CreatedTime, item.CreatedBy, item.Initiator,
			item.AssignedByID, item.Responsible, bitrixValue(item.Department), item.Details, payload); err != nil {
			return err
		}
		vacancyTitle := numberedRequestValue(item.Details, 2)
		if vacancyTitle == "" {
			continue
		}
		requestedCount := numberedRequestCount(item.Details, 3)
		if _, err = tx.Exec(ctx, `
			INSERT INTO main_office_vacancies(bitrix_request_id,title,requested_count)
			VALUES($1,$2,$3)
			ON CONFLICT(bitrix_request_id) DO UPDATE SET
				title=EXCLUDED.title,requested_count=EXCLUDED.requested_count,updated_at=now()
		`, item.ID, vacancyTitle, requestedCount); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (a *App) runBitrixHRVacancySync(ctx context.Context) {
	syncOnce := func() {
		if err := a.syncBitrixHRVacancies(ctx); err != nil {
			log.Printf("Bitrix HR vacancy sync failed: %v", err)
		}
	}
	syncOnce()
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}

func (a *App) loadMainOfficeVacancies(ctx context.Context) ([]mainOfficeVacancy, error) {
	rows, err := a.db.Query(ctx, `
		SELECT v.id::text,r.bitrix_request_id,r.title,v.title,v.requested_count,
			count(c.id) FILTER (WHERE c.status='hired')::int,
			count(c.id) FILTER (WHERE c.status='hired') >= v.requested_count,
			r.created_time,r.created_by_name,r.assigned_by_name,r.department_code,r.details
		FROM main_office_vacancies v
		JOIN bitrix_hr_requests r USING(bitrix_request_id)
		LEFT JOIN main_office_candidates c ON c.vacancy_id=v.id
		GROUP BY v.id,r.bitrix_request_id,r.title,v.title,v.requested_count,r.created_time,
			r.created_by_name,r.assigned_by_name,r.department_code,r.details
		ORDER BY r.created_time DESC,r.bitrix_request_id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []mainOfficeVacancy{}
	for rows.Next() {
		var item mainOfficeVacancy
		if err = rows.Scan(&item.ID, &item.BitrixRequestID, &item.RequestTitle, &item.VacancyTitle,
			&item.RequestedCount, &item.HiredCount, &item.IsClosed, &item.CreatedTime, &item.Initiator, &item.Responsible,
			&item.DepartmentCode, &item.Details); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type mainOfficeCandidate struct {
	ID           string `json:"id"`
	FullName     string `json:"fullName"`
	InvitedAt    string `json:"invitedAt"`
	Status       string `json:"status"`
	InternshipAt string `json:"internshipAt"`
	HiredAt      string `json:"hiredAt,omitempty"`
}

func (a *App) mainOfficeVacancyCandidates(w http.ResponseWriter, r *http.Request) {
	var vacancyExists string
	if err := a.db.QueryRow(r.Context(), `SELECT id::text FROM main_office_vacancies WHERE id=$1`, r.PathValue("id")).Scan(&vacancyExists); err != nil {
		problem(w, http.StatusNotFound, "Вакансия не найдена")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,full_name,invited_at::text,status,COALESCE(internship_at::text,''),COALESCE(hired_at::text,'') FROM main_office_candidates WHERE vacancy_id=$1 ORDER BY created_at,id`, r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []mainOfficeCandidate{}
	for rows.Next() {
		var item mainOfficeCandidate
		if err = rows.Scan(&item.ID, &item.FullName, &item.InvitedAt, &item.Status, &item.InternshipAt, &item.HiredAt); err != nil {
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

func (a *App) createMainOfficeCandidate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FullName  string `json:"fullName"`
		InvitedAt string `json:"invitedAt"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.FullName = strings.TrimSpace(input.FullName)
	if input.FullName == "" {
		problem(w, http.StatusUnprocessableEntity, "Укажите имя кандидата")
		return
	}
	if input.InvitedAt == "" {
		input.InvitedAt = localToday()
	}
	if _, err := time.Parse("2006-01-02", input.InvitedAt); err != nil {
		problem(w, http.StatusUnprocessableEntity, "Укажите корректную дату приглашения")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var requestedCount, hiredCount int
	err = tx.QueryRow(ctx, `SELECT requested_count,(SELECT count(*) FROM main_office_candidates c WHERE c.vacancy_id=v.id AND c.status='hired') FROM main_office_vacancies v WHERE v.id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&requestedCount, &hiredCount)
	if err != nil || hiredCount >= requestedCount {
		problem(w, http.StatusConflict, "Вакансия закрыта или не найдена")
		return
	}
	var item mainOfficeCandidate
	err = tx.QueryRow(ctx, `INSERT INTO main_office_candidates(vacancy_id,full_name,invited_at) VALUES($1,$2,$3::date) RETURNING id::text,full_name,invited_at::text,status,COALESCE(internship_at::text,''),''`, r.PathValue("id"), input.FullName, input.InvitedAt).Scan(&item.ID, &item.FullName, &item.InvitedAt, &item.Status, &item.InternshipAt, &item.HiredAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusCreated, item)
}

func (a *App) updateMainOfficeCandidate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status       string `json:"status"`
		InvitedAt    string `json:"invitedAt"`
		InternshipAt string `json:"internshipAt"`
	}
	if !decode(w, r, &input) {
		return
	}
	validStatus := map[string]bool{"invited": true, "interviewed": true, "internship": true, "hired": true, "rejected": true}
	if !validStatus[input.Status] {
		problem(w, http.StatusUnprocessableEntity, "Неизвестный этап кандидата")
		return
	}
	if _, err := time.Parse("2006-01-02", input.InvitedAt); err != nil {
		problem(w, http.StatusUnprocessableEntity, "Укажите корректную дату приглашения")
		return
	}
	if input.InternshipAt != "" {
		if _, err := time.Parse("2006-01-02", input.InternshipAt); err != nil {
			problem(w, http.StatusUnprocessableEntity, "Укажите корректную дату стажировки")
			return
		}
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var vacancyID, previousStatus string
	var requestedCount int
	if err = tx.QueryRow(ctx, `SELECT c.vacancy_id::text,c.status,v.requested_count FROM main_office_candidates c JOIN main_office_vacancies v ON v.id=c.vacancy_id WHERE c.id=$1 FOR UPDATE OF c,v`, r.PathValue("id")).Scan(&vacancyID, &previousStatus, &requestedCount); err != nil {
		problem(w, http.StatusNotFound, "Кандидат не найден")
		return
	}
	if input.Status == "hired" && previousStatus != "hired" {
		var hiredCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM main_office_candidates WHERE vacancy_id=$1 AND status='hired'`, vacancyID).Scan(&hiredCount); err != nil {
			serverError(w, err)
			return
		}
		if hiredCount >= requestedCount {
			problem(w, http.StatusConflict, "Все места по вакансии уже закрыты")
			return
		}
	}
	var item mainOfficeCandidate
	err = tx.QueryRow(ctx, `
		UPDATE main_office_candidates SET status=$2,invited_at=$3::date,
			internship_at=NULLIF($4,'')::date,
			hired_at=CASE WHEN $2='hired' THEN COALESCE(hired_at,now()) ELSE NULL END,
			updated_at=now()
		WHERE id=$1
		RETURNING id::text,full_name,invited_at::text,status,COALESCE(internship_at::text,''),COALESCE(hired_at::text,'')
	`, r.PathValue("id"), input.Status, input.InvitedAt, input.InternshipAt).Scan(&item.ID, &item.FullName, &item.InvitedAt, &item.Status, &item.InternshipAt, &item.HiredAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusOK, item)
}

func numberedRequestValue(details string, number int) string {
	prefix := strconv.Itoa(number) + "."
	for _, line := range strings.Split(details, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if fullWidthColon := strings.Index(line, "："); colon < 0 || (fullWidthColon >= 0 && fullWidthColon < colon) {
			colon = fullWidthColon
		}
		if colon < 0 {
			return ""
		}
		return strings.TrimSpace(line[colon+1:])
	}
	return ""
}

func numberedRequestCount(details string, number int) int {
	value := numberedRequestValue(details, number)
	for _, field := range strings.Fields(value) {
		if count, err := strconv.Atoi(strings.Trim(field, ",.;:()[]")); err == nil && count > 0 {
			return count
		}
	}
	return 1
}

func bitrixValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
