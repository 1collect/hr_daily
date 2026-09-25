package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type bitrixHRRequest struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	StageID     string `json:"stageId"`
	CreatedTime string `json:"createdTime"`
	Department  any    `json:"ufCrm16_1729775124"`
	Details     string `json:"ufCrm16_1724755746"`
}

func (a *App) bitrixHRRequests(w http.ResponseWriter, r *http.Request) {
	if a.bitrixWebhookBaseURL == "" {
		problem(w, http.StatusServiceUnavailable, "Bitrix24 не настроен")
		return
	}
	payload := map[string]any{
		"entityTypeId": 1068,
		"filter":       map[string]any{"ufCrm16_1724409072100": 80, "stageId": "DT1068_22:UC_F7LSF1"},
		"select":       []string{"id", "title", "stageId", "createdTime", "ufCrm16_1729775124", "ufCrm16_1724755746"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		problem(w, http.StatusInternalServerError, "Не удалось подготовить запрос к Bitrix24")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, a.bitrixWebhookBaseURL+"/crm.item.list.json", bytes.NewReader(body))
	if err != nil {
		problem(w, http.StatusInternalServerError, "Не удалось подготовить запрос к Bitrix24")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.bitrixHTTPClient.Do(req)
	if err != nil {
		problem(w, http.StatusBadGateway, "Не удалось получить заявки из Bitrix24")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		problem(w, http.StatusBadGateway, fmt.Sprintf("Bitrix24 вернул ошибку (HTTP %d)", resp.StatusCode))
		return
	}
	var result struct {
		Result struct {
			Items []bitrixHRRequest `json:"items"`
		} `json:"result"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		problem(w, http.StatusBadGateway, "Bitrix24 вернул некорректный ответ")
		return
	}
	if result.Error != "" {
		message := result.ErrorDesc
		if message == "" {
			message = result.Error
		}
		problem(w, http.StatusBadGateway, "Ошибка Bitrix24: "+strings.TrimSpace(message))
		return
	}
	if result.Result.Items == nil {
		result.Result.Items = []bitrixHRRequest{}
	}
	jsonOut(w, http.StatusOK, result.Result.Items)
}
