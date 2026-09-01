package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TheGU/awxreport/internal/config"
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

func TestEffectiveInclude(t *testing.T) {
	tests := []struct {
		name    string
		cfgInc  config.IncludeFilter
		ro      reportOpts
		want    config.IncludeFilter
		wantErr string // substring; empty means no error expected
	}{
		{
			name:   "no flags: config passes through unchanged",
			cfgInc: config.IncludeFilter{TemplateIDs: []int{4, 9}, ProjectIDs: []int{2}},
			ro:     reportOpts{},
			want:   config.IncludeFilter{TemplateIDs: []int{4, 9}, ProjectIDs: []int{2}},
		},
		{
			name:   "template-ids flag replaces, does not merge",
			cfgInc: config.IncludeFilter{TemplateIDs: []int{4, 9}, ProjectIDs: []int{2}},
			ro:     reportOpts{templateIDs: []int{7}},
			want:   config.IncludeFilter{TemplateIDs: []int{7}, ProjectIDs: []int{2}},
		},
		{
			name:   "project-ids flag replaces, does not merge",
			cfgInc: config.IncludeFilter{TemplateIDs: []int{4, 9}, ProjectIDs: []int{2}},
			ro:     reportOpts{projectIDs: []int{5}},
			want:   config.IncludeFilter{TemplateIDs: []int{4, 9}, ProjectIDs: []int{5}},
		},
		{
			name:   "both flags replace both lists",
			cfgInc: config.IncludeFilter{TemplateIDs: []int{4, 9}, ProjectIDs: []int{2}},
			ro:     reportOpts{templateIDs: []int{7}, projectIDs: []int{5}},
			want:   config.IncludeFilter{TemplateIDs: []int{7}, ProjectIDs: []int{5}},
		},
		{
			name:   "full clears a non-empty config include block",
			cfgInc: config.IncludeFilter{TemplateIDs: []int{4, 9}, ProjectIDs: []int{2}},
			ro:     reportOpts{full: true},
			want:   config.IncludeFilter{},
		},
		{
			name:   "full with empty config include block is a no-op",
			cfgInc: config.IncludeFilter{},
			ro:     reportOpts{full: true},
			want:   config.IncludeFilter{},
		},
		{
			name:    "full plus template-ids conflicts",
			cfgInc:  config.IncludeFilter{},
			ro:      reportOpts{full: true, templateIDs: []int{7}},
			wantErr: "--full cannot be combined with --template-ids or --project-ids",
		},
		{
			name:    "full plus project-ids conflicts",
			cfgInc:  config.IncludeFilter{},
			ro:      reportOpts{full: true, projectIDs: []int{5}},
			wantErr: "--full cannot be combined with --template-ids or --project-ids",
		},
		{
			name:    "invalid template id from flag names the source",
			cfgInc:  config.IncludeFilter{},
			ro:      reportOpts{templateIDs: []int{0}},
			wantErr: "invalid template id 0 from --template-ids or include.template_ids: must be a positive integer",
		},
		{
			name:    "invalid project id from flag names the source",
			cfgInc:  config.IncludeFilter{},
			ro:      reportOpts{projectIDs: []int{-1}},
			wantErr: "invalid project id -1 from --project-ids or include.project_ids: must be a positive integer",
		},
		{
			name:    "invalid template id from config-only (no flag) still validated",
			cfgInc:  config.IncludeFilter{TemplateIDs: []int{-3}},
			ro:      reportOpts{},
			wantErr: "invalid template id -3 from --template-ids or include.template_ids: must be a positive integer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := effectiveInclude(tt.cfgInc, tt.ro)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("effectiveInclude: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("effectiveInclude = %+v, want %+v", got, tt.want)
			}
		})
	}
}
