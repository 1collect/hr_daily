package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const whatsappCandidatePermission = "candidates.whatsapp.view"

type whatsappCandidate struct {
	ID              string    `json:"id"`
	ConfigID        string    `json:"configId"`
	EmployeeName    string    `json:"employeeName"`
	PhoneNumber     string    `json:"phoneNumber"`
	ExternalID      string    `json:"externalId"`
	DisplayName     string    `json:"displayName"`
	Status          string    `json:"status"`
	CurrentQuestion string    `json:"currentQuestion"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type whatsappQuestion struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Type     string `json:"answerType"`
	Position int    `json:"position"`
	Key      string `json:"key"`
}

type whatsappMessage struct {
	ID        string    `json:"id"`
	Direction string    `json:"direction"`
	Text      string    `json:"text"`
	Transport string    `json:"transport"`
	SentAt    time.Time `json:"sentAt"`
}

func (a *App) requireWhatsAppCandidateAccess(w http.ResponseWriter, r *http.Request) (sessionClaims, bool) {
	c := claimsFrom(r.Context())
	if c.UserID == "" || !a.hasPermission(r.Context(), c.UserID, whatsappCandidatePermission) {
		problem(w, http.StatusForbidden, "Нет доступа к кандидатам WhatsApp")
		return c, false
	}
	return c, true
}

func (a *App) whatsappCandidateList(w http.ResponseWriter, r *http.Request) {
	c, ok := a.requireWhatsAppCandidateAccess(w, r)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `
		SELECT c.id,c.config_id,trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),
		w.phone_number,c.external_id,c.display_name,c.status,COALESCE(q.text,''),c.created_at,c.updated_at
		FROM whatsapp_candidates c
		JOIN whatsapp_waba_configs w ON w.id=c.config_id
		JOIN employees e ON e.id=w.employee_id
		LEFT JOIN whatsapp_questions q ON q.id=c.current_question_id
		WHERE ($1::text='' OR w.employee_id=$1::uuid)
		ORDER BY c.updated_at DESC`, c.EmployeeID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []whatsappCandidate{}
	for rows.Next() {
		var x whatsappCandidate
		if err = rows.Scan(&x.ID, &x.ConfigID, &x.EmployeeName, &x.PhoneNumber, &x.ExternalID, &x.DisplayName, &x.Status, &x.CurrentQuestion, &x.CreatedAt, &x.UpdatedAt); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, x)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusOK, out)
}

func (a *App) whatsappCandidateDetail(w http.ResponseWriter, r *http.Request) {
	c, ok := a.requireWhatsAppCandidateAccess(w, r)
	if !ok {
		return
	}
	var item whatsappCandidate
	err := a.db.QueryRow(r.Context(), `
		SELECT c.id,c.config_id,trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),
		w.phone_number,c.external_id,c.display_name,c.status,COALESCE(q.text,''),c.created_at,c.updated_at
		FROM whatsapp_candidates c JOIN whatsapp_waba_configs w ON w.id=c.config_id
		JOIN employees e ON e.id=w.employee_id LEFT JOIN whatsapp_questions q ON q.id=c.current_question_id
		WHERE c.id=$1 AND ($2::text='' OR w.employee_id=$2::uuid)`, r.PathValue("id"), c.EmployeeID).
		Scan(&item.ID, &item.ConfigID, &item.EmployeeName, &item.PhoneNumber, &item.ExternalID, &item.DisplayName, &item.Status, &item.CurrentQuestion, &item.CreatedAt, &item.UpdatedAt)
	if err == pgx.ErrNoRows {
		problem(w, http.StatusNotFound, "Кандидат не найден")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var questions []whatsappQuestion
	qrows, err := a.db.Query(r.Context(), `SELECT id,text,answer_type,position,key FROM whatsapp_questions WHERE is_active ORDER BY position`)
	if err != nil {
		serverError(w, err)
		return
	}
	for qrows.Next() {
		var q whatsappQuestion
		if err = qrows.Scan(&q.ID, &q.Text, &q.Type, &q.Position, &q.Key); err != nil {
			qrows.Close()
			serverError(w, err)
			return
		}
		questions = append(questions, q)
	}
	qrows.Close()
	answers := map[string]string{}
	arows, err := a.db.Query(r.Context(), `SELECT q.key,a.text FROM whatsapp_answers a JOIN whatsapp_questions q ON q.id=a.question_id WHERE a.candidate_id=$1 ORDER BY q.position`, item.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for arows.Next() {
		var key, text string
		if err = arows.Scan(&key, &text); err != nil {
			arows.Close()
			serverError(w, err)
			return
		}
		answers[key] = text
	}
	arows.Close()
	msgs := []whatsappMessage{}
	mrows, err := a.db.Query(r.Context(), `SELECT id,direction,text,transport,sent_at FROM whatsapp_messages WHERE candidate_id=$1 ORDER BY sent_at,id`, item.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for mrows.Next() {
		var m whatsappMessage
		if err = mrows.Scan(&m.ID, &m.Direction, &m.Text, &m.Transport, &m.SentAt); err != nil {
			mrows.Close()
			serverError(w, err)
			return
		}
		msgs = append(msgs, m)
	}
	mrows.Close()
	jsonOut(w, http.StatusOK, map[string]any{"candidate": item, "questions": questions, "answers": answers, "messages": msgs})
}

func (a *App) whatsappCandidateAnswer(w http.ResponseWriter, r *http.Request) {
	c, ok := a.requireWhatsAppCandidateAccess(w, r)
	if !ok {
		return
	}
	var in struct {
		Answer string `json:"answer"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Answer = strings.TrimSpace(in.Answer)
	if in.Answer == "" {
		problem(w, 422, "Ответ не может быть пустым")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var candidateID, externalID, configID, employeeID, mode, token, phone, apiVersion, apiBase string
	var currentQuestion string
	err = tx.QueryRow(ctx, `SELECT c.id,c.external_id,c.config_id,w.employee_id,c.current_question_id::text,w.transport_mode,w.access_token,w.phone_number_id,w.phone_number_id FROM whatsapp_candidates c JOIN whatsapp_waba_configs w ON w.id=c.config_id WHERE c.id=$1 AND ($2::text='' OR w.employee_id=$2::uuid) FOR UPDATE`, r.PathValue("id"), c.EmployeeID).Scan(&candidateID, &externalID, &configID, &employeeID, &currentQuestion, &mode, &token, &phone, &apiVersion, &apiBase)
	if err == pgx.ErrNoRows {
		problem(w, 404, "Кандидат не найден")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_ = employeeID
	_ = configID
	_ = apiVersion
	_ = apiBase
	var qid, qtype string
	var qpos int
	err = tx.QueryRow(ctx, `SELECT id,answer_type,position FROM whatsapp_questions WHERE id=$1`, currentQuestion).Scan(&qid, &qtype, &qpos)
	if err != nil {
		problem(w, 422, "У кандидата нет активного вопроса")
		return
	}
	answer, err := normalizeWhatsAppAnswer(qtype, in.Answer)
	if err != nil {
		problem(w, 422, err.Error())
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO whatsapp_answers(candidate_id,question_id,text) VALUES($1,$2,$3) ON CONFLICT(candidate_id,question_id) DO UPDATE SET text=EXCLUDED.text,answered_at=now()`, candidateID, qid, answer); err != nil {
		serverError(w, err)
		return
	}
	if err = advanceWhatsAppCandidate(ctx, tx, candidateID, mode); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	_ = phone
	_ = token
	_ = qpos
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func normalizeWhatsAppAnswer(kind, value string) (string, error) {
	switch kind {
	case "yes_no":
		v := strings.ToLower(strings.TrimSpace(value))
		if v == "да" || v == "yes" {
			return "Да", nil
		}
		if v == "нет" || v == "no" {
			return "Нет", nil
		}
		return "", fmt.Errorf("Выберите «Да» или «Нет»")
	case "number":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 999999999 {
			return "", fmt.Errorf("Введите целое неотрицательное число")
		}
		return strconv.Itoa(n), nil
	}
	return value, nil
}

func advanceWhatsAppCandidate(ctx context.Context, tx pgx.Tx, candidateID, mode string) error {
	var nextID, nextText string
	var nextType string
	err := tx.QueryRow(ctx, `SELECT q.id,q.text,q.answer_type FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer)) ORDER BY q.position LIMIT 1`, candidateID).Scan(&nextID, &nextText, &nextType)
	if err == pgx.ErrNoRows {
		_, err = tx.Exec(ctx, `UPDATE whatsapp_candidates SET status='survey_completed',current_question_id=NULL,survey_completed_at=now(),updated_at=now() WHERE id=$1`, candidateID)
		return err
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE whatsapp_candidates SET status='survey_in_progress',current_question_id=$2,updated_at=now() WHERE id=$1`, candidateID, nextID)
	if err != nil {
		return err
	}
	_ = nextText
	_ = nextType
	_ = mode
	return nil
}

var whatsappNumberedAnswer = regexp.MustCompile(`(?m)(?:^|\n)\s*(\d+)\s*[).:-]\s*([^\n]+)`)

func (a *App) whatsappWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		token := r.URL.Query().Get("hub.verify_token")
		challenge := r.URL.Query().Get("hub.challenge")
		var valid bool
		if token != "" {
			_ = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM whatsapp_waba_configs WHERE verify_token=$1 AND active)`, token).Scan(&valid)
		}
		if valid {
			_, _ = w.Write([]byte(challenge))
			return
		}
		http.Error(w, "Webhook verification failed", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	for _, entry := range anySlice(payload["entry"]) {
		e, _ := entry.(map[string]any)
		for _, change := range anySlice(e["changes"]) {
			ch, _ := change.(map[string]any)
			value, _ := ch["value"].(map[string]any)
			metadata, _ := value["metadata"].(map[string]any)
			phoneID, _ := metadata["phone_number_id"].(string)
			var cfgID, mode, token, secret, phoneNumberID string
			if err := a.db.QueryRow(r.Context(), `SELECT id,$2,access_token,app_secret,phone_number_id FROM whatsapp_waba_configs WHERE phone_number_id=$1 AND active`, phoneID, a.whatsappMode).Scan(&cfgID, &mode, &token, &secret, &phoneNumberID); err != nil {
				continue
			}
			if !validWhatsAppSignature(body, r.Header.Get("X-Hub-Signature-256"), secret) {
				continue
			}
			for _, raw := range anySlice(value["messages"]) {
				msg, _ := raw.(map[string]any)
				sender, _ := msg["from"].(string)
				messageID, _ := msg["id"].(string)
				if sender == "" {
					continue
				}
				if err := a.handleWhatsAppIncoming(r.Context(), cfgID, mode, token, phoneNumberID, sender, whatsappContactName(value, sender), whatsappMessageText(msg), messageID); err != nil {
					log.Printf("whatsapp webhook handling failed: %v", err)
				}
			}
		}
	}
	jsonOut(w, 200, map[string]string{"status": "ok"})
}

func anySlice(v any) []any {
	if x, ok := v.([]any); ok {
		return x
	}
	return nil
}
func whatsappContactName(value map[string]any, sender string) string {
	for _, raw := range anySlice(value["contacts"]) {
		c, _ := raw.(map[string]any)
		if c["wa_id"] == sender {
			p, _ := c["profile"].(map[string]any)
			n, _ := p["name"].(string)
			return n
		}
	}
	return ""
}
func whatsappMessageText(m map[string]any) string {
	if t, ok := m["text"].(map[string]any); ok {
		v, _ := t["body"].(string)
		return v
	}
	if i, ok := m["interactive"].(map[string]any); ok {
		if b, ok := i["button_reply"].(map[string]any); ok {
			v, _ := b["title"].(string)
			return v
		}
	}
	return ""
}
func validWhatsAppSignature(body []byte, header, secret string) bool {
	if secret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), mustDecodeHex(strings.TrimPrefix(header, "sha256=")))
}
func mustDecodeHex(v string) []byte { b, _ := hex.DecodeString(v); return b }

func (a *App) handleWhatsAppIncoming(ctx context.Context, cfgID, mode, token, phoneNumber, sender, display, text, messageID string) error {
	var candidateID, status, current string
	var restarted bool
	err := a.db.QueryRow(ctx, `INSERT INTO whatsapp_candidates(config_id,external_id,display_name) VALUES($1,$2,$3) ON CONFLICT(config_id,external_id) DO UPDATE SET display_name=CASE WHEN EXCLUDED.display_name<>'' THEN EXCLUDED.display_name ELSE whatsapp_candidates.display_name END,updated_at=now() RETURNING id,status,COALESCE(current_question_id::text,'')`, cfgID, sender, display).Scan(&candidateID, &status, &current)
	if err != nil {
		return err
	}
	if messageID != "" {
		var exists bool
		_ = a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM whatsapp_messages WHERE external_message_id=$1)`, messageID).Scan(&exists)
		if exists {
			return nil
		}
	}
	if mode == "whatsapp_test" && status == "survey_completed" {
		_, err = a.db.Exec(ctx, `DELETE FROM whatsapp_answers WHERE candidate_id=$1; UPDATE whatsapp_candidates SET status='new',current_question_id=NULL,survey_completed_at=NULL,updated_at=now() WHERE id=$1`, candidateID)
		if err != nil {
			return err
		}
		status = "new"
		current = ""
		restarted = true
	}
	_, err = a.db.Exec(ctx, `INSERT INTO whatsapp_messages(candidate_id,direction,text,transport,external_message_id) VALUES($1,'incoming',$2,$3,NULLIF($4,''))`, candidateID, text, mode, messageID)
	if err != nil {
		return err
	}
	_ = restarted
	if status == "new" || current == "" {
		var qid, qtext string
		err = a.db.QueryRow(ctx, `SELECT id,text FROM whatsapp_questions q WHERE q.is_active ORDER BY position LIMIT 1`).Scan(&qid, &qtext)
		if err != nil {
			return err
		}
		_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='survey_in_progress',current_question_id=$2,survey_started_at=COALESCE(survey_started_at,now()),updated_at=now() WHERE id=$1`, candidateID, qid)
		if err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "Здравствуйте! Для рассмотрения вашей кандидатуры, пожалуйста, ответьте на несколько вопросов:\n\n1. Ваше полное ФИО?\n2. Сколько Вам лет?\n3. Учитесь ли Вы сейчас?\n4. Имеется ли у Вас судимость?\n5. Имеется ли арест или ограничение на банковских счетах?\n6. Ваше последнее место работы?")
	}
	return a.processWhatsAppTextAI(ctx, candidateID, mode, token, phoneNumber, sender, text, current)
}

func (a *App) processWhatsAppTextAI(ctx context.Context, candidateID, mode, token, phoneNumber, sender, text, current string) error {
	rows, err := a.db.Query(ctx, `SELECT q.id,q.text,q.answer_type,q.position,q.key FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer)) ORDER BY q.position`, candidateID)
	if err != nil {
		return err
	}
	defer rows.Close()
	questions := []whatsappQuestion{}
	for rows.Next() {
		var q whatsappQuestion
		if err = rows.Scan(&q.ID, &q.Text, &q.Type, &q.Position, &q.Key); err != nil {
			return err
		}
		questions = append(questions, q)
	}
	if len(questions) == 0 {
		return nil
	}
	previous := ""
	_ = a.db.QueryRow(ctx, `SELECT text FROM whatsapp_messages WHERE candidate_id=$1 AND direction='outgoing' ORDER BY sent_at DESC,id DESC LIMIT 1`, candidateID).Scan(&previous)
	extracted, err := a.extractWhatsAppAnswers(ctx, text, previous, questions)
	if err != nil {
		return nil
	}
	valid := map[string]whatsappQuestion{}
	for _, q := range questions {
		valid[q.ID] = q
	}
	saved := 0
	for _, item := range extracted {
		q, ok := valid[item.QuestionID]
		if !ok {
			continue
		}
		normalized, nerr := normalizeWhatsAppAnswer(q.Type, item.Answer)
		if nerr != nil {
			continue
		}
		if _, err = a.db.Exec(ctx, `INSERT INTO whatsapp_answers(candidate_id,question_id,text) VALUES($1,$2,$3) ON CONFLICT(candidate_id,question_id) DO UPDATE SET text=EXCLUDED.text,answered_at=now()`, candidateID, q.ID, normalized); err != nil {
			return err
		}
		saved++
	}
	if saved == 0 {
		return nil
	}
	if reason, err := a.whatsappRejectionReason(ctx, candidateID); err != nil {
		return err
	} else if reason != "" {
		_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='rejected',current_question_id=NULL,survey_completed_at=NULL,updated_at=now() WHERE id=$1`, candidateID)
		if err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "К сожалению, ваша кандидатура нам не подходит.")
	}
	var nextID, nextText string
	err = a.db.QueryRow(ctx, `SELECT q.id,q.text FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer)) ORDER BY q.position LIMIT 1`, candidateID).Scan(&nextID, &nextText)
	if err == pgx.ErrNoRows {
		_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='survey_completed',current_question_id=NULL,survey_completed_at=now(),updated_at=now() WHERE id=$1`, candidateID)
		if err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "С вами свяжутся наши менеджеры в ближайшее время.")
	}
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET current_question_id=$2,status='survey_in_progress',updated_at=now() WHERE id=$1`, candidateID, nextID)
	if err != nil {
		return err
	}
	_ = current
	return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, nextText)
}

func (a *App) processWhatsAppText(ctx context.Context, candidateID, mode, token, phoneNumber, sender, text, current string) error {
	var qtype string
	var qpos int
	if err := a.db.QueryRow(ctx, `SELECT answer_type,position FROM whatsapp_questions WHERE id=$1`, current).Scan(&qtype, &qpos); err != nil {
		return err
	}
	answer := strings.TrimSpace(text)
	if matches := whatsappNumberedAnswer.FindStringSubmatch(text); len(matches) > 0 {
		answer = matches[2]
	}
	normalized, err := normalizeWhatsAppAnswer(qtype, answer)
	if err != nil {
		return nil
	}
	var qid string
	if err = a.db.QueryRow(ctx, `SELECT id FROM whatsapp_questions WHERE position=$1`, qpos).Scan(&qid); err != nil {
		return err
	}
	if _, err = a.db.Exec(ctx, `INSERT INTO whatsapp_answers(candidate_id,question_id,text) VALUES($1,$2,$3) ON CONFLICT(candidate_id,question_id) DO UPDATE SET text=EXCLUDED.text,answered_at=now()`, candidateID, qid, normalized); err != nil {
		return err
	}
	if reason, err := a.whatsappRejectionReason(ctx, candidateID); err != nil {
		return err
	} else if reason != "" {
		if _, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='rejected',current_question_id=NULL,survey_completed_at=NULL,updated_at=now() WHERE id=$1`, candidateID); err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "К сожалению, ваша кандидатура нам не подходит.")
	}
	var nextID, nextText string
	if err = a.db.QueryRow(ctx, `SELECT id,text FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer)) ORDER BY position LIMIT 1`, candidateID).Scan(&nextID, &nextText); err == pgx.ErrNoRows {
		_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='survey_completed',current_question_id=NULL,survey_completed_at=now(),updated_at=now() WHERE id=$1`, candidateID)
		if err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "С вами свяжутся наши менеджеры в ближайшее время.")
	} else if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET current_question_id=$2,status='survey_in_progress',updated_at=now() WHERE id=$1`, candidateID, nextID)
	if err != nil {
		return err
	}
	return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, nextText)
}

func (a *App) whatsappRejectionReason(ctx context.Context, candidateID string) (string, error) {
	answers := map[string]string{}
	rows, err := a.db.Query(ctx, `SELECT q.key,a.text FROM whatsapp_answers a JOIN whatsapp_questions q ON q.id=a.question_id WHERE a.candidate_id=$1`, candidateID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return "", err
		}
		answers[key] = value
	}
	if age, err := strconv.Atoi(answers["age"]); err == nil && age < 18 {
		return "underage", nil
	}
	if answers["criminal_record"] == "Да" || answers["bank_restrictions"] == "Да" {
		return "screening", nil
	}
	if answers["studying"] == "Да" && answers["is_final_year"] == "Нет" {
		return "not_final_year", nil
	}
	return "", rows.Err()
}

func (a *App) sendWhatsApp(ctx context.Context, candidateID, mode, token, phoneNumber, sender, text string) error {
	metadata := map[string]any{}
	if token != "" {
		payload := map[string]any{"messaging_product": "whatsapp", "to": sender, "type": "text", "text": map[string]any{"preview_url": false, "body": text}}
		body, _ := json.Marshal(payload)
		u := "https://graph.facebook.com/v23.0/" + url.PathEscape(phoneNumber) + "/messages"
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode >= 300 {
			return fmt.Errorf("WhatsApp API returned %s: %s", resp.Status, string(raw))
		}
		_ = json.Unmarshal(raw, &metadata)
	}
	_, err := a.db.Exec(ctx, `INSERT INTO whatsapp_messages(candidate_id,direction,text,transport,metadata) VALUES($1,'outgoing',$2,$3,$4)`, candidateID, text, mode, metadata)
	return err
}

func (a *App) updateWhatsAppCandidateAnswer(w http.ResponseWriter, r *http.Request) {
	c, ok := a.requireWhatsAppCandidateAccess(w, r)
	if !ok {
		return
	}
	var in struct {
		Answer string `json:"answer"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Answer = strings.TrimSpace(in.Answer)
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var candidateID, kind string
	err = tx.QueryRow(ctx, `SELECT c.id,q.answer_type FROM whatsapp_candidates c JOIN whatsapp_waba_configs w ON w.id=c.config_id JOIN whatsapp_answers a ON a.candidate_id=c.id JOIN whatsapp_questions q ON q.id=a.question_id WHERE c.id=$1 AND q.id=$2 AND ($3::text='' OR w.employee_id=$3::uuid) FOR UPDATE`, r.PathValue("id"), r.PathValue("questionId"), c.EmployeeID).Scan(&candidateID, &kind)
	if err == pgx.ErrNoRows {
		problem(w, 404, "Ответ не найден")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	normalized, err := normalizeWhatsAppAnswer(kind, in.Answer)
	if err != nil {
		problem(w, 422, err.Error())
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE whatsapp_answers SET text=$3,answered_at=now() WHERE candidate_id=$1 AND question_id=$2`, candidateID, r.PathValue("questionId"), normalized); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE whatsapp_candidates SET current_question_id=(SELECT q.id FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer)) ORDER BY q.position LIMIT 1),status=CASE WHEN EXISTS(SELECT 1 FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer))) THEN 'survey_in_progress' ELSE 'survey_completed' END,updated_at=now() WHERE id=$1`, candidateID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (a *App) updateWhatsAppCandidate(w http.ResponseWriter, r *http.Request) {
	c, ok := a.requireWhatsAppCandidateAccess(w, r)
	if !ok {
		return
	}
	var in struct {
		Status      string `json:"status"`
		DisplayName string `json:"displayName"`
	}
	if !decode(w, r, &in) {
		return
	}
	valid := map[string]bool{"new": true, "survey_in_progress": true, "survey_completed": true, "contacted": true, "rejected": true, "hired": true}
	if in.Status != "" && !valid[in.Status] {
		problem(w, 422, "Недопустимый статус кандидата")
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE whatsapp_candidates c SET status=CASE WHEN $2='' THEN c.status ELSE $2 END,display_name=CASE WHEN $3='' THEN c.display_name ELSE $3 END,updated_at=now() FROM whatsapp_waba_configs w WHERE c.config_id=w.id AND c.id=$1 AND ($4::text='' OR w.employee_id=$4::uuid)`, r.PathValue("id"), in.Status, strings.TrimSpace(in.DisplayName), c.EmployeeID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, 404, "Кандидат не найден")
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
