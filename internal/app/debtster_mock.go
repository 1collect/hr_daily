package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

func newDebtsterClient(mock bool) *http.Client {
	client := &http.Client{Timeout: 10 * time.Second}
	if mock {
		client.Transport = debtsterMockTransport{}
	}
	return client
}

// debtsterMockTransport exercises the normal JSON decoders without network access.
type debtsterMockTransport struct{}

func (debtsterMockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if req.Method != http.MethodGet {
		return nil, fmt.Errorf("Debtster mock: unsupported method %s", req.Method)
	}
	departments := []debtsterDepartment{
		{ID: 990001, Name: "Тест Алматы", DisplayName: "Тест Алматы"},
		{ID: 990002, Name: "Тест Астана", DisplayName: "Тест Астана"},
		{ID: 990003, Name: "Тест Шымкент", DisplayName: "Тест Шымкент"},
	}
	var data any
	switch req.URL.Path {
	case debtsterDepartmentsPath:
		data = departments
	case debtsterVacanciesPath:
		rows := make([]debtsterVacancyReport, 0, len(departments))
		for i, department := range departments {
			rows = append(rows, debtsterVacancyReport{ID: department.ID, RP: department.DisplayName,
				StaffPositionsCount: 20 + i*5, ActiveEmployeesCount: 17 + i*4, VacantPositionsCount: 3 + i,
				TraineesCount: i + 1, RecruitmentCount: 2 + i, PlannedDismissalsCount: 1,
				PlannedDismissals: []debtsterPlannedDismissal{{FirstName: "Демо", LastName: "Тестов", MiddleName: "Тестович"}},
			})
		}
		data = rows
	case debtsterTraineesPath:
		rows := []debtsterTraineeReport{}
		filter := req.URL.Query().Get("department_id")
		date := req.URL.Query().Get("report_date")
		for i, department := range departments {
			if filter != "" && filter != strconv.Itoa(department.ID) {
				continue
			}
			for j := 0; j <= i; j++ {
				id := department.ID*10 + j
				rows = append(rows, debtsterTraineeReport{ReportTypeID: 1, Department: department.DisplayName,
					ReportDate: date, ReporterID: department.ID, TraineeID: &id,
					FullName: fmt.Sprintf("Тестов Кандидат %d", id), StatusID: "1", Source: "Тестовые данные",
					InterviewDate: &date, InternshipStartDate: &date, Note: "Демонстрационный стажёр Debtster",
				})
			}
		}
		data = rows
	default:
		return nil, fmt.Errorf("Debtster mock: unsupported path %s", req.URL.Path)
	}
	body, err := json.Marshal(map[string]any{"error_code": 0, "status": "success", "data": data})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
}
