package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type bitrixHRRequest struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	StageID      string `json:"stageId"`
	CreatedTime  string `json:"createdTime"`
	CreatedBy    int64  `json:"createdBy"`
	AssignedByID int64  `json:"assignedById"`
	Initiator    string `json:"initiator"`
	Responsible  string `json:"responsible"`
	Department   any    `json:"ufCrm16_1729775124"`
	Details      string `json:"ufCrm16_1724755746"`
}

type bitrixUser struct {
	ID         string `json:"ID"`
	Name       string `json:"NAME"`
	LastName   string `json:"LAST_NAME"`
	SecondName string `json:"SECOND_NAME"`
}

func (a *App) bitrixHRRequests(w http.ResponseWriter, r *http.Request) {
	if a.bitrixWebhookBaseURL == "" {
		problem(w, http.StatusServiceUnavailable, "Bitrix24 не настроен")
		return
	}
	payload := map[string]any{
		"entityTypeId": 1068,
		"filter":       map[string]any{"ufCrm16_1724409072100": 80, "stageId": "DT1068_22:UC_F7LSF1"},
		"select":       []string{"id", "title", "stageId", "createdTime", "createdBy", "assignedById", "ufCrm16_1729775124", "ufCrm16_1724755746"},
	}
	var result struct {
		Items []bitrixHRRequest `json:"items"`
	}
	if _, err := a.bitrixPost(r.Context(), "crm.item.list.json", payload, &result); err != nil {
		problem(w, http.StatusBadGateway, err.Error())
		return
	}
	ids := make([]string, 0, len(result.Items)*2)
	seen := make(map[string]bool, len(result.Items)*2)
	for _, item := range result.Items {
		for _, id := range []int64{item.CreatedBy, item.AssignedByID} {
			key := fmt.Sprint(id)
			if id > 0 && !seen[key] {
				seen[key] = true
				ids = append(ids, key)
			}
		}
	}
	users := a.bitrixUsers(r.Context(), ids)
	for i := range result.Items {
		item := &result.Items[i]
		item.Initiator = bitrixUserName(users[fmt.Sprint(item.CreatedBy)], item.CreatedBy)
		item.Responsible = bitrixUserName(users[fmt.Sprint(item.AssignedByID)], item.AssignedByID)
	}
	if result.Items == nil {
		result.Items = []bitrixHRRequest{}
	}
	jsonOut(w, http.StatusOK, result.Items)
}

func (a *App) bitrixUsers(ctx context.Context, ids []string) map[string]bitrixUser {
	users := make(map[string]bitrixUser, len(ids))
	if len(ids) == 0 {
		return users
	}
	for start := 0; ; start += 50 {
		var page []bitrixUser
		payload := map[string]any{"filter": map[string]any{"@ID": ids}, "start": start}
		next, err := a.bitrixPost(ctx, "user.get.json", payload, &page)
		if err != nil {
			break
		}
		for _, user := range page {
			users[user.ID] = user
		}
		if next == nil {
			break
		}
	}
	return users
}

func bitrixUserName(user bitrixUser, id int64) string {
	name := strings.TrimSpace(strings.Join([]string{user.Name, user.SecondName, user.LastName}, " "))
	if name != "" {
		return name
	}
	if id > 0 {
		return fmt.Sprintf("ID %d", id)
	}
	return "—"
}

func (a *App) bitrixPost(ctx context.Context, method string, payload any, result any) (*int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("не удалось подготовить запрос к Bitrix24")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.bitrixWebhookBaseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("не удалось подготовить запрос к Bitrix24")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.bitrixHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить данные из Bitrix24")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Bitrix24 вернул ошибку (HTTP %d)", resp.StatusCode)
	}
	var envelope struct {
		Result    json.RawMessage `json:"result"`
		Error     string          `json:"error"`
		ErrorDesc string          `json:"error_description"`
		Next      *int            `json:"next"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("Bitrix24 вернул некорректный ответ")
	}
	if envelope.Error != "" {
		message := envelope.ErrorDesc
		if message == "" {
			message = envelope.Error
		}
		return nil, fmt.Errorf("ошибка Bitrix24: %s", strings.TrimSpace(message))
	}
	if err = json.Unmarshal(envelope.Result, result); err != nil {
		return nil, fmt.Errorf("Bitrix24 вернул некорректный ответ")
	}
	return envelope.Next, nil
}
