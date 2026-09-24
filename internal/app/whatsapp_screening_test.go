package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWhatsAppIgnoreMessage(t *testing.T) {
	cases := []struct {
		message string
		ignore  bool
	}{
		{"Ты идиот", true},
		{"Просто дурак", true},
		{"Иди нахуй", true},
		{"Стоп", true},
		{"Не пишите мне", true},
		{"Иван Дураков", false},
		{"Судимости нет", false},
		{"Не учусь, работал в банке", false},
	}
	for _, tc := range cases {
		if got := isWhatsAppIgnoreMessage(tc.message); got != tc.ignore {
			t.Errorf("ignore(%q) = %v, want %v", tc.message, got, tc.ignore)
		}
	}
}

func TestLocalWhatsAppAnswersDoNotGuessFullName(t *testing.T) {
	questions := []whatsappQuestion{
		{ID: "name", Key: "full_name", Position: 1},
		{ID: "age", Key: "age", Position: 2},
		{ID: "study", Key: "studying", Position: 3},
		{ID: "final", Key: "is_final_year", Position: 7, ShowIfQuestionID: "study", ShowIfAnswer: "Да"},
	}
	if got := localWhatsAppAnswers("Здравствуйте, как дела?", questions); len(got) != 0 {
		t.Fatalf("unrelated message became answers: %+v", got)
	}
	if got := localWhatsAppAnswers("Иван Иванов", questions); len(got) != 0 {
		t.Fatalf("unnumbered single message became full name: %+v", got)
	}
	got := localWhatsAppAnswers("1. Иван Иванов 2. 19 3. Да 7. Да", questions)
	if len(got) != 4 || got[0].Answer != "Иван Иванов" || got[3].QuestionID != "final" {
		t.Fatalf("numbered answers not parsed: %+v", got)
	}
	got = localWhatsAppAnswers("Иван Иванов\n19\nНет", questions)
	if len(got) != 3 || got[1].Answer != "19" {
		t.Fatalf("complete line-by-line answers not parsed: %+v", got)
	}
}

func TestWhatsAppAnswerValidation(t *testing.T) {
	cases := []struct {
		question whatsappQuestion
		answer   string
		valid    bool
	}{
		{whatsappQuestion{Key: "full_name", Type: "text"}, "Иван Дураков", true},
		{whatsappQuestion{Key: "full_name", Type: "text"}, "Добрый день", false},
		{whatsappQuestion{Key: "full_name", Type: "text"}, "Ты идиот", false},
		{whatsappQuestion{Key: "full_name", Type: "text"}, "Просто дурак", false},
		{whatsappQuestion{Key: "age", Type: "number"}, "19 лет", true},
		{whatsappQuestion{Key: "age", Type: "number"}, "0", false},
		{whatsappQuestion{Key: "last_job", Type: "text"}, "Не работал", true},
		{whatsappQuestion{Key: "last_job", Type: "text"}, "Привет", false},
	}
	for _, tc := range cases {
		_, err := normalizeWhatsAppCandidateAnswer(tc.question, tc.answer)
		if (err == nil) != tc.valid {
			t.Errorf("validate %s=%q: err=%v, want valid=%v", tc.question.Key, tc.answer, err, tc.valid)
		}
	}
}

func TestWhatsAppAIFollowUpMustMatchRemainingQuestions(t *testing.T) {
	remaining := []whatsappQuestion{{ID: "age", Text: "Сколько Вам лет?"}, {ID: "job", Text: "Ваше последнее место работы?"}}
	analysis := whatsappAIAnalysis{MissingQuestionIDs: []string{"age", "job"}, FollowUp: "Пожалуйста, ответьте одним сообщением:\n2. Сколько Вам лет?\n6. Ваше последнее место работы?"}
	if !validWhatsAppAIFollowUp(analysis, remaining) {
		t.Fatal("valid follow-up rejected")
	}
	analysis.MissingQuestionIDs = []string{"age"}
	if validWhatsAppAIFollowUp(analysis, remaining) {
		t.Fatal("follow-up omitted a required question")
	}
}

func TestAnalyzeWhatsAppMessageUsesStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected OpenAI request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("invalid request JSON: %v", err)
		}
		format, _ := body["text"].(map[string]any)
		if format == nil || format["format"] == nil {
			t.Error("structured output format missing")
		}
		payload := `{"intent":"ignore","ignore_reason":"abuse","ignore_evidence":"Ты идиот","answers":[],"missing_question_ids":[],"follow_up":""}`
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": payload}}}}})
	}))
	defer server.Close()
	a := &App{openAIAPIKey: "test-key", openAIModel: "test-model", openAIAPIBaseURL: server.URL, aiHTTPClient: server.Client()}
	analysis, err := a.analyzeWhatsAppMessage(context.Background(), "Ты идиот", "", nil, nil, nil)
	if err != nil || analysis.Intent != "ignore" {
		t.Fatalf("ignore analysis = %+v, err=%v", analysis, err)
	}
}

func TestAnalyzeWhatsAppMessagePreparesMissingQuestion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := `{"intent":"answers","ignore_reason":"none","ignore_evidence":"","answers":[{"question_id":"age","answer":"19","evidence":"19"}],"missing_question_ids":["job"],"follow_up":"Спасибо! Ответьте одним сообщением: 6. Ваше последнее место работы?"}`
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": payload}}}}})
	}))
	defer server.Close()
	a := &App{openAIAPIKey: "test-key", openAIModel: "test-model", openAIAPIBaseURL: server.URL, aiHTTPClient: server.Client()}
	questions := []whatsappQuestion{{ID: "age", Text: "Сколько Вам лет?", Key: "age", Type: "number", Position: 2}, {ID: "job", Text: "Ваше последнее место работы?", Key: "last_job", Type: "text", Position: 6}}
	analysis, err := a.analyzeWhatsAppMessage(context.Background(), "Мне 19 лет", "", questions, nil, nil)
	if err != nil || len(analysis.Answers) != 1 || analysis.Answers[0].Answer != "19" {
		t.Fatalf("answers = %+v, err=%v", analysis, err)
	}
	if !validWhatsAppAIFollowUp(analysis, questions[1:]) {
		t.Fatalf("model follow-up was not accepted: %+v", analysis)
	}
}
