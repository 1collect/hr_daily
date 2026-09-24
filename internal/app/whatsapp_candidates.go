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
	ID               string    `json:"id"`
	ConfigID         string    `json:"configId"`
	EmployeeName     string    `json:"employeeName"`
	PhoneNumber      string    `json:"phoneNumber"`
	ExternalID       string    `json:"externalId"`
	DisplayName      string    `json:"displayName"`
	Status           string    `json:"status"`
	CurrentQuestion  string    `json:"currentQuestion"`
	FullName         string    `json:"fullName"`
	Age              string    `json:"age"`
	Studying         string    `json:"studying"`
	FinalYear        string    `json:"finalYear"`
	CriminalRecord   string    `json:"criminalRecord"`
	BankRestrictions string    `json:"bankRestrictions"`
	LastJob          string    `json:"lastJob"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
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
		w.phone_number,c.external_id,c.display_name,
		CASE
		  WHEN c.status='rejected' THEN 'rejected'
		  WHEN c.status IN ('survey_completed','contacted','hired') THEN 'accepted'
		  WHEN COALESCE(m.last_incoming,c.created_at) < now()-interval '24 hours' THEN 'ignored'
		  WHEN COALESCE(a.answer_count,0)>0 THEN 'incomplete'
		  ELSE 'in_progress'
		END,
		COALESCE(q.text,''),COALESCE(a.full_name,''),COALESCE(a.age,''),COALESCE(a.studying,''),
		COALESCE(a.final_year,''),COALESCE(a.criminal_record,''),COALESCE(a.bank_restrictions,''),COALESCE(a.last_job,''),c.created_at,c.updated_at
		FROM whatsapp_candidates c
		JOIN whatsapp_waba_configs w ON w.id=c.config_id
		JOIN employees e ON e.id=w.employee_id
		LEFT JOIN whatsapp_questions q ON q.id=c.current_question_id
		LEFT JOIN LATERAL (
		  SELECT count(*) AS answer_count,
		    max(a.text) FILTER (WHERE question.key='full_name') AS full_name,
		    max(a.text) FILTER (WHERE question.key='age') AS age,
		    max(a.text) FILTER (WHERE question.key='studying') AS studying,
		    max(a.text) FILTER (WHERE question.key='is_final_year') AS final_year,
		    max(a.text) FILTER (WHERE question.key='criminal_record') AS criminal_record,
		    max(a.text) FILTER (WHERE question.key='bank_restrictions') AS bank_restrictions,
		    max(a.text) FILTER (WHERE question.key='last_job') AS last_job
		  FROM whatsapp_answers a JOIN whatsapp_questions question ON question.id=a.question_id
		  WHERE a.candidate_id=c.id
		) a ON true
		LEFT JOIN LATERAL (SELECT max(sent_at) AS last_incoming FROM whatsapp_messages WHERE candidate_id=c.id AND direction='incoming') m ON true
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
		if err = rows.Scan(&x.ID, &x.ConfigID, &x.EmployeeName, &x.PhoneNumber, &x.ExternalID, &x.DisplayName, &x.Status, &x.CurrentQuestion, &x.FullName, &x.Age, &x.Studying, &x.FinalYear, &x.CriminalRecord, &x.BankRestrictions, &x.LastJob, &x.CreatedAt, &x.UpdatedAt); err != nil {
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

func (a *App) deleteWhatsAppCandidate(w http.ResponseWriter, r *http.Request) {
	c, ok := a.requireWhatsAppCandidateAccess(w, r)
	if !ok {
		return
	}
	result, err := a.db.Exec(r.Context(), `
		DELETE FROM whatsapp_candidates c
		USING whatsapp_waba_configs w
		WHERE c.config_id=w.id AND c.id=$1
		  AND ($2::text='' OR w.employee_id=$2::uuid)`, r.PathValue("id"), c.EmployeeID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, http.StatusNotFound, "Кандидат не найден")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if messageID != "" {
		var exists bool
		if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM whatsapp_messages WHERE external_message_id=$1)`, messageID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	err := a.db.QueryRow(ctx, `SELECT id,status,COALESCE(current_question_id::text,'')
		FROM whatsapp_candidates WHERE config_id=$1 AND external_id=$2
		ORDER BY created_at DESC,id DESC LIMIT 1`, cfgID, sender).Scan(&candidateID, &status, &current)
	if err == pgx.ErrNoRows || (err == nil && mode == "whatsapp_test" && (status == "survey_completed" || status == "rejected")) {
		err = a.db.QueryRow(ctx, `INSERT INTO whatsapp_candidates(config_id,external_id,display_name)
			VALUES($1,$2,$3) RETURNING id,status,COALESCE(current_question_id::text,'')`, cfgID, sender, display).Scan(&candidateID, &status, &current)
	} else if err == nil {
		err = a.db.QueryRow(ctx, `UPDATE whatsapp_candidates
			SET display_name=CASE WHEN $2<>'' THEN $2 ELSE display_name END,updated_at=now()
			WHERE id=$1 RETURNING status,COALESCE(current_question_id::text,'')`, candidateID, display).Scan(&status, &current)
	}
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `INSERT INTO whatsapp_messages(candidate_id,direction,text,transport,external_message_id) VALUES($1,'incoming',$2,$3,NULLIF($4,''))`, candidateID, text, mode, messageID)
	if err != nil {
		return err
	}
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
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "Здравствуйте! Для рассмотрения вашей кандидатуры, пожалуйста, ответьте одним сообщением на вопросы ниже. Если какой-то вопрос к вам не относится, напишите «нет».\n\n1. Ваше полное ФИО?\n2. Сколько Вам лет?\n3. Учитесь ли Вы сейчас?\n4. Имеется ли у Вас судимость?\n5. Имеется ли арест или ограничение на банковских счетах?\n6. Ваше последнее место работы?")
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
		log.Printf("whatsapp answer extraction failed; trying local parser: %v", err)
		extracted = localWhatsAppAnswers(text, questions)
	} else {
		// Preserve explicit numbered answers even if the AI only extracted a subset.
		seen := map[string]bool{}
		for _, item := range extracted {
			seen[item.QuestionID] = true
		}
		for _, item := range localWhatsAppAnswers(text, questions) {
			if !seen[item.QuestionID] {
				extracted = append(extracted, item)
				seen[item.QuestionID] = true
			}
		}
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
	if reason, err := a.whatsappRejectionReason(ctx, candidateID); err != nil {
		return err
	} else if reason != "" {
		_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='rejected',current_question_id=NULL,survey_completed_at=NULL,updated_at=now() WHERE id=$1`, candidateID)
		if err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "Спасибо за ответы!\n\nК сожалению, ваша кандидатура нам не подходит.")
	}
	remainingRows, err := a.db.Query(ctx, `SELECT q.id,q.text,q.position FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer)) ORDER BY q.position`, candidateID)
	if err != nil {
		return err
	}
	remaining := []whatsappQuestion{}
	for remainingRows.Next() {
		var q whatsappQuestion
		if err = remainingRows.Scan(&q.ID, &q.Text, &q.Position); err != nil {
			remainingRows.Close()
			return err
		}
		remaining = append(remaining, q)
	}
	err = remainingRows.Err()
	remainingRows.Close()
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='survey_completed',current_question_id=NULL,survey_completed_at=now(),updated_at=now() WHERE id=$1`, candidateID)
		if err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "Спасибо за ответы!\n\nС вами свяжутся наши менеджеры в ближайшее время.")
	}
	_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET current_question_id=$2,status='survey_in_progress',updated_at=now() WHERE id=$1`, candidateID, remaining[0].ID)
	if err != nil {
		return err
	}
	_ = current
	var followup strings.Builder
	if saved == 0 {
		followup.WriteString("Не удалось распознать ответы. Пожалуйста, ответьте одним сообщением на оставшиеся вопросы:\n\n")
	} else {
		followup.WriteString("Спасибо! Остались вопросы — пожалуйста, ответьте на них одним сообщением:\n\n")
	}
	for _, q := range remaining {
		fmt.Fprintf(&followup, "%d. %s\n", q.Position, q.Text)
	}
	return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, strings.TrimSpace(followup.String()))
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
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "Спасибо за ответы!\n\nК сожалению, ваша кандидатура нам не подходит.")
	}
	var nextID, nextText string
	if err = a.db.QueryRow(ctx, `SELECT id,text FROM whatsapp_questions q WHERE q.is_active AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id) AND (q.show_if_question_id IS NULL OR EXISTS(SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1 AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer)) ORDER BY position LIMIT 1`, candidateID).Scan(&nextID, &nextText); err == pgx.ErrNoRows {
		_, err = a.db.Exec(ctx, `UPDATE whatsapp_candidates SET status='survey_completed',current_question_id=NULL,survey_completed_at=now(),updated_at=now() WHERE id=$1`, candidateID)
		if err != nil {
			return err
		}
		return a.sendWhatsApp(ctx, candidateID, mode, token, phoneNumber, sender, "Спасибо за ответы!\n\nС вами свяжутся наши менеджеры в ближайшее время.")
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
