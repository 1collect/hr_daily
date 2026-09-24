package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type whatsappAIAnswer struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
	Evidence   string `json:"evidence"`
}

func (a *App) extractWhatsAppAnswers(ctx context.Context, text, previous string, questions []whatsappQuestion) ([]whatsappAIAnswer, error) {
	if a.openAIAPIKey == "" {
		return localWhatsAppAnswers(text, questions), nil
	}
	questionData := make([]map[string]any, 0, len(questions))
	for _, q := range questions {
		questionData = append(questionData, map[string]any{"question_id": q.ID, "position": q.Position, "question": q.Text, "answer_type": q.Type})
	}
	payload := map[string]any{"model": a.openAIModel, "input": []any{map[string]any{"role": "system", "content": "You extract explicit answers from a candidate message. Do not infer. Return only answers supported by an exact quote from the new candidate message. Use only supplied question IDs. For yes_no return exactly Да or Нет; for number return digits."}, map[string]any{"role": "user", "content": mustJSON(map[string]any{"questions": questionData, "previous_bot_message": previous, "new_candidate_message": text})}}, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "questionnaire_answers", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"answers": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"question_id": map[string]any{"type": "string"}, "answer": map[string]any{"type": "string"}, "evidence": map[string]any{"type": "string"}}, "required": []string{"question_id", "answer", "evidence"}, "additionalProperties": false}}}, "required": []string{"answers"}, "additionalProperties": false}}}}
	body := mustJSON(payload)
	req, err := httpNewJSON(ctx, a.openAIAPIBaseURL+"/responses", []byte(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.openAIAPIKey)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenAI returned %s", resp.Status)
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	output := responseOutputText(result)
	if output == "" {
		return nil, fmt.Errorf("OpenAI returned no questionnaire output")
	}
	var parsed struct {
		Answers []whatsappAIAnswer `json:"answers"`
	}
	if err = json.Unmarshal([]byte(output), &parsed); err != nil {
		return nil, err
	}
	valid := map[string]bool{}
	for _, q := range questions {
		valid[q.ID] = true
	}
	source := strings.ToLower(text)
	out := []whatsappAIAnswer{}
	for _, item := range parsed.Answers {
		if valid[item.QuestionID] && item.Answer != "" && item.Evidence != "" && strings.Contains(source, strings.ToLower(item.Evidence)) {
			out = append(out, item)
		}
	}
	return out, nil
}

func localWhatsAppAnswers(text string, questions []whatsappQuestion) []whatsappAIAnswer {
	byPos := map[int]whatsappQuestion{}
	for _, q := range questions {
		byPos[q.Position] = q
	}
	// Accept both one-answer-per-line and compact replies such as
	// "1. Name 2. 19 3. Yes". Each numbered marker starts a new answer.
	re := regexp.MustCompile(`(?:^|[\s])([0-9]{1,2})\s*[).:-]\s*`)
	out := []whatsappAIAnswer{}
	markers := re.FindAllStringSubmatchIndex(text, -1)
	for i, m := range markers {
		p, _ := strconv.Atoi(text[m[2]:m[3]])
		if _, ok := byPos[p]; !ok {
			continue
		}
		start := m[1]
		if i+1 < len(markers) {
			startNext := markers[i+1][0]
			// The next marker's leading whitespace belongs to the separator.
			for startNext > start && (text[startNext-1] == ' ' || text[startNext-1] == '\n' || text[startNext-1] == '\t' || text[startNext-1] == '\r') {
				startNext--
			}
			if startNext < start {
				startNext = markers[i+1][0]
			}
			start = startNext
		}
		answer := strings.TrimSpace(text[m[1]:start])
		answer = strings.Trim(answer, " \t\r\n,;")
		if answer == "" {
			continue
		}
		q := byPos[p]
		out = append(out, whatsappAIAnswer{QuestionID: q.ID, Answer: answer, Evidence: answer})
	}
	if len(out) > 0 {
		return out
	}
	lines := strings.Split(text, "\n")
	clean := []string{}
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			clean = append(clean, strings.TrimSpace(line))
		}
	}
	for i, line := range clean {
		if i >= len(questions) {
			break
		}
		out = append(out, whatsappAIAnswer{QuestionID: questions[i].ID, Answer: line, Evidence: line})
	}
	if len(out) == 0 && len(questions) > 0 && strings.TrimSpace(text) != "" {
		out = []whatsappAIAnswer{{QuestionID: questions[0].ID, Answer: strings.TrimSpace(text), Evidence: strings.TrimSpace(text)}}
	}
	return out
}

func mustJSON(value any) string { b, _ := json.Marshal(value); return string(b) }
func httpNewJSON(ctx context.Context, endpoint string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, err
}
func responseOutputText(result map[string]any) string {
	items, _ := result["output"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["type"] != "message" {
			continue
		}
		parts, _ := item["content"].([]any)
		for _, part := range parts {
			content, _ := part.(map[string]any)
			if content["type"] == "output_text" {
				text, _ := content["text"].(string)
				return text
			}
		}
	}
	return ""
}
