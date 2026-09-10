package app

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestTraineeNameSimilarity(t *testing.T) {
	cases := []struct {
		a, b     string
		min, max int
	}{
		{"Османов Али", "  АЛИ,  Османов. ", 100, 100},
		{"Османов Али Ерланович", "Али Османов", 90, 99},
		{"Әли Қасымов", "Али Касымов", 95, 100},
		{"Әли Османов", "Osmanov Ali", 95, 100},
		{"Айгерим Хасанова", "Khasanova Aigerim", 95, 100},
		{"Османов Али Ерланович", "Османов А.", 75, 89},
		{"Османов Али Ерланович", "Османов Али Е.", 80, 99},
		{"Османов Али Ерланович", "Осмонов Али", 75, 94},
		{"Нурмухамедов Нурлан", "Нурмухаметов Нурлан", 85, 99},
		{"Османов Али Ерланович", "Али", 0, 54},
		{"Османов Али", "Османов Серик", 0, 54},
		{"Османов Али", "Али Али", 0, 54},
		{"А. Е.", "Али Ерланович", 0, 54},
		{"", "Али Османов", 0, 0},
		{"Иванов Александр", "Серикова Айжан", 0, 54},
	}
	for _, tc := range cases {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			forward := nameSimilarity(nameTokens(tc.a), nameTokens(tc.b))
			reverse := nameSimilarity(nameTokens(tc.b), nameTokens(tc.a))
			if forward < tc.min || forward > tc.max {
				t.Errorf("score=%d, want %d..%d", forward, tc.min, tc.max)
			}
			if reverse != forward {
				t.Errorf("asymmetric scores: %d != %d", forward, reverse)
			}
		})
	}
}

func TestTraineeSearchPreservesNamesAndOrdersMatches(t *testing.T) {
	names := []string{"Осмонов Али", "  Али ОСМАНОВ Ерланович  ", "Иванов Иван", "Али", "Османов А."}
	idx := newTraineeNameIndex(names)
	matches := idx.search("Османов Али Ерланович", 55)
	if len(matches) != 3 {
		t.Fatalf("unexpected matches: %+v", matches)
	}
	if matches[0].FullName != names[1] || matches[0].Score != 100 {
		t.Fatalf("original full name not preserved: %+v", matches)
	}
	for i := 1; i < len(matches); i++ {
		if matches[i-1].Score < matches[i].Score {
			t.Fatal("unsorted matches")
		}
	}
	if got := idx.search("Османов Али Ерланович", 95); len(got) != 1 {
		t.Fatalf("threshold ignored: %+v", got)
	}
	if got := idx.search("Петров Николай", 55); got == nil || len(got) != 0 {
		t.Fatalf("want empty array, got %+v", got)
	}
	if !reflect.DeepEqual(names, idx.names) {
		t.Fatal("source names modified")
	}
}

func TestTraineesRejectInvalidThreshold(t *testing.T) {
	for _, value := range []string{"0", "101", "abc", "55.5"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/trainees?report_date=2026-09-10&threshold="+value, nil)
		(&App{}).trainees(w, r)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("threshold %q: status %d", value, w.Code)
		}
	}
}
