package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

const whatsappReminderDelay = 30 * time.Minute

type whatsappPendingReminder struct {
	candidateID string
	sender      string
	token       string
	phoneID     string
	lastMessage string
}

func (a *App) runWhatsAppReminderWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := a.sendPendingWhatsAppReminders(ctx); err != nil && ctx.Err() == nil {
			log.Printf("whatsapp reminder scan failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) sendPendingWhatsAppReminders(ctx context.Context) error {
	rows, err := a.db.Query(ctx, `
		SELECT c.id,c.external_id,w.access_token,w.phone_number_id,latest_incoming.text
		FROM whatsapp_candidates c
		JOIN whatsapp_waba_configs w ON w.id=c.config_id AND w.active
		JOIN LATERAL (
			SELECT m.sent_at,m.text FROM whatsapp_messages m
			WHERE m.candidate_id=c.id AND m.direction='incoming'
			ORDER BY m.sent_at DESC,m.id DESC LIMIT 1
		) latest_incoming ON true
		LEFT JOIN LATERAL (
			SELECT max(m.sent_at) AS sent_at FROM whatsapp_messages m
			WHERE m.candidate_id=c.id AND m.direction='outgoing'
		) latest_outgoing ON true
		WHERE c.status='survey_in_progress'
		  AND latest_incoming.sent_at <= now()-($1 * interval '1 second')
		  AND (latest_outgoing.sent_at IS NULL OR latest_outgoing.sent_at < latest_incoming.sent_at)
		  AND EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=c.id)
		ORDER BY latest_incoming.sent_at
		LIMIT 100`, int(whatsappReminderDelay.Seconds()))
	if err != nil {
		return err
	}
	pending := []whatsappPendingReminder{}
	for rows.Next() {
		var item whatsappPendingReminder
		if err = rows.Scan(&item.candidateID, &item.sender, &item.token, &item.phoneID, &item.lastMessage); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = a.sendWhatsAppReminder(ctx, item); err != nil {
			log.Printf("whatsapp reminder failed for candidate %s: %v", item.candidateID, err)
		}
	}
	return nil
}

func (a *App) sendWhatsAppReminder(ctx context.Context, item whatsappPendingReminder) error {
	// Recheck just before sending. A new incoming message restarts the 30-minute
	// quiet period and must not receive a stale reminder.
	var due bool
	err := a.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM whatsapp_candidates c
			JOIN LATERAL (
				SELECT m.sent_at FROM whatsapp_messages m WHERE m.candidate_id=c.id AND m.direction='incoming'
				ORDER BY m.sent_at DESC,m.id DESC LIMIT 1
			) incoming ON true
			LEFT JOIN LATERAL (
				SELECT max(m.sent_at) AS sent_at FROM whatsapp_messages m WHERE m.candidate_id=c.id AND m.direction='outgoing'
			) outgoing ON true
			WHERE c.id=$1 AND c.status='survey_in_progress'
			  AND incoming.sent_at <= now()-($2 * interval '1 second')
			  AND (outgoing.sent_at IS NULL OR outgoing.sent_at < incoming.sent_at)
		)`, item.candidateID, int(whatsappReminderDelay.Seconds())).Scan(&due)
	if err != nil || !due {
		return err
	}

	rows, err := a.db.Query(ctx, `
		SELECT q.id,q.text,q.answer_type,q.position,q.key,COALESCE(q.show_if_question_id::text,''),q.show_if_answer
		FROM whatsapp_questions q
		WHERE q.is_active
		  AND NOT EXISTS(SELECT 1 FROM whatsapp_answers a WHERE a.candidate_id=$1 AND a.question_id=q.id)
		  AND (q.show_if_question_id IS NULL OR EXISTS(
			SELECT 1 FROM whatsapp_answers parent WHERE parent.candidate_id=$1
			  AND parent.question_id=q.show_if_question_id AND parent.text=q.show_if_answer
		  ))
		ORDER BY q.position`, item.candidateID)
	if err != nil {
		return err
	}
	remaining := []whatsappQuestion{}
	for rows.Next() {
		var q whatsappQuestion
		if err = rows.Scan(&q.ID, &q.Text, &q.Type, &q.Position, &q.Key, &q.ShowIfQuestionID, &q.ShowIfAnswer); err != nil {
			rows.Close()
			return err
		}
		remaining = append(remaining, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(remaining) == 0 {
		return err
	}

	answers := map[string]string{}
	answersByID := map[string]string{}
	answerRows, err := a.db.Query(ctx, `
		SELECT q.id,q.key,a.text FROM whatsapp_answers a
		JOIN whatsapp_questions q ON q.id=a.question_id WHERE a.candidate_id=$1`, item.candidateID)
	if err != nil {
		return err
	}
	for answerRows.Next() {
		var id, key, value string
		if err = answerRows.Scan(&id, &key, &value); err != nil {
			answerRows.Close()
			return err
		}
		answers[key] = value
		answersByID[id] = value
	}
	err = answerRows.Err()
	answerRows.Close()
	if err != nil {
		return err
	}

	message := ""
	if a.openAIAPIKey != "" {
		var previous string
		_ = a.db.QueryRow(ctx, `SELECT text FROM whatsapp_messages WHERE candidate_id=$1 AND direction='outgoing' ORDER BY sent_at DESC,id DESC LIMIT 1`, item.candidateID).Scan(&previous)
		aiCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		analysis, analyzeErr := a.analyzeWhatsAppMessage(aiCtx, item.lastMessage, previous, remaining, answers, answersByID)
		cancel()
		if analyzeErr == nil && validWhatsAppAIFollowUp(analysis, remaining) {
			message = strings.TrimSpace(analysis.FollowUp)
		}
	}
	if message == "" {
		message = fallbackWhatsAppReminder(remaining)
	}

	// Check the quiet period again after the AI call, which may take several seconds.
	var stillDue bool
	err = a.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM whatsapp_candidates c
			JOIN LATERAL (
				SELECT m.sent_at FROM whatsapp_messages m WHERE m.candidate_id=c.id AND m.direction='incoming'
				ORDER BY m.sent_at DESC,m.id DESC LIMIT 1
			) incoming ON true
			LEFT JOIN LATERAL (
				SELECT max(m.sent_at) AS sent_at FROM whatsapp_messages m WHERE m.candidate_id=c.id AND m.direction='outgoing'
			) outgoing ON true
			WHERE c.id=$1 AND c.status='survey_in_progress'
			  AND incoming.sent_at <= now()-($2 * interval '1 second')
			  AND (outgoing.sent_at IS NULL OR outgoing.sent_at < incoming.sent_at)
		)`, item.candidateID, int(whatsappReminderDelay.Seconds())).Scan(&stillDue)
	if err != nil || !stillDue {
		return err
	}
	return a.sendWhatsApp(ctx, item.candidateID, a.whatsappMode, item.token, item.phoneID, item.sender, message)
}

func fallbackWhatsAppReminder(remaining []whatsappQuestion) string {
	var message strings.Builder
	message.WriteString("Спасибо, часть ответов уже есть 🙂 Осталось уточнить:")
	for _, q := range remaining {
		fmt.Fprintf(&message, "\n%d. %s", q.Position, q.Text)
	}
	message.WriteString("\n\nНапишите, пожалуйста, одним сообщением.")
	return message.String()
}
