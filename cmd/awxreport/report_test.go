package main

import (
	"testing"
	"time"
)

func TestReportWindow(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	tests := []struct {
		name       string
		start, end string
		wantSince  time.Time
		wantUntil  time.Time
		wantErr    bool
	}{
		{"neither: days_back from now", "", "", now.AddDate(0, 0, -30), now, false},
		{"both dates, end inclusive", "2026-05-01", "2026-05-31", day(2026, 5, 1), day(2026, 6, 1), false},
		{"start only: until now", "2026-05-01", "", day(2026, 5, 1), now, false},
		{"end only: days_back before end", "", "2026-05-31", day(2026, 5, 2), day(2026, 6, 1), false},
		{"same day is a one-day window", "2026-05-01", "2026-05-01", day(2026, 5, 1), day(2026, 5, 2), false},
		{"start after end", "2026-06-01", "2026-05-01", time.Time{}, time.Time{}, true},
		{"bad start format", "01-05-2026", "", time.Time{}, time.Time{}, true},
		{"bad end format", "", "2026/05/31", time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			since, until, err := reportWindow(tt.start, tt.end, 30, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if !since.Equal(tt.wantSince) || !until.Equal(tt.wantUntil) {
				t.Errorf("window = [%s, %s), want [%s, %s)",
					since, until, tt.wantSince, tt.wantUntil)
			}
		})
	}
}
