package app

import (
	"strings"
	"testing"
	"time"
)

func TestValidateCorrectionRequestInput(t *testing.T) {
	validDate := previousBusinessDate(localToday())
	tests := []struct {
		name  string
		input createCorrectionRequestInput
		valid bool
	}{
		{name: "valid", input: createCorrectionRequestInput{ReportDate: validDate, ReportType: "rp", Note: "Не успел заполнить отчёт", Rows: []requestedCorrectionRow{{RowID: "row"}}}, valid: true},
		{name: "short note", input: createCorrectionRequestInput{ReportDate: validDate, ReportType: "rp", Note: "нет", Rows: []requestedCorrectionRow{{RowID: "row"}}}},
		{name: "long note", input: createCorrectionRequestInput{ReportDate: validDate, ReportType: "rp", Note: strings.Repeat("а", 1001), Rows: []requestedCorrectionRow{{RowID: "row"}}}},
		{name: "today", input: createCorrectionRequestInput{ReportDate: localToday(), ReportType: "rp", Note: "Нужно исправить отчёт", Rows: []requestedCorrectionRow{{RowID: "row"}}}},
		{name: "missing type", input: createCorrectionRequestInput{ReportDate: validDate, Note: "Нужно исправить отчёт", Rows: []requestedCorrectionRow{{RowID: "row"}}}},
		{name: "missing rows", input: createCorrectionRequestInput{ReportDate: validDate, ReportType: "rp", Note: "Нужно исправить отчёт"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateCorrectionRequestInput(tt.input)
			if (got == "") != tt.valid {
				t.Fatalf("validation message = %q, valid = %v", got, tt.valid)
			}
		})
	}
}

func previousBusinessDate(value string) string {
	date, _ := time.Parse("2006-01-02", value)
	for {
		date = date.AddDate(0, 0, -1)
		if date.Weekday() != time.Saturday && date.Weekday() != time.Sunday {
			return date.Format("2006-01-02")
		}
	}
}
