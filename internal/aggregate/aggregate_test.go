package aggregate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TheGU/awxreport/internal/awx"
)

// fixedT returns a deterministic timestamp at minute offset n from a base.
func fixedT(n int) time.Time {
	return time.Date(2026, 4, 1, 0, n, 0, 0, time.UTC)
}

func newTestAgg(t *testing.T, ex ExcludeRules) *Aggregator {
	t.Helper()
	// Build HostLite via JSON so we don't have to reproduce anonymous
	// struct literals in tests.
	var host awx.HostLite
	if err := json.Unmarshal([]byte(`{
		"id": 6, "name": "ubuntu-test", "inventory": 2, "enabled": true,
		"summary_fields": {"inventory": {"id": 2, "name": "lab"}}
	}`), &host); err != nil {
		t.Fatal(err)
	}
	l := &awx.Lookups{
		Hosts:       map[int]awx.HostLite{6: host},
		Inventories: map[int]awx.InventoryLite{2: {ID: 2, Name: "lab"}},
		Templates: map[int]awx.TemplateLite{
			40: {ID: 40, Name: "Check Auth Key", Playbook: "playbooks/check.yml"},
			99: {ID: 99, Name: "Health Check", Playbook: "playbooks/healthcheck.yml"},
		},
		AnsibleHost: map[int]string{6: "10.0.0.6"},
	}
	return New(l, ex)
}

func mkSummary(jobID, hostID, tplID int, tplName string, failed bool, mod time.Time) awx.SummaryLite {
	hp := &hostID
	if hostID == 0 {
		hp = nil
	}
	s := awx.SummaryLite{
		ID:       jobID*100 + hostID,
		Job:      jobID,
		Host:     hp,
		HostName: "ubuntu-test",
		Modified: mod,
		Failed:   failed,
	}
	if failed {
		s.Failures = 1
	} else {
		s.OK = 1
	}
	s.SummaryFields.Job = &struct {
		ID              int    `json:"id"`
		Name            string `json:"name"`
		Status          string `json:"status"`
		Failed          bool   `json:"failed"`
		JobTemplateID   int    `json:"job_template_id"`
		JobTemplateName string `json:"job_template_name"`
	}{ID: jobID, Status: jobStatus(failed), JobTemplateID: tplID, JobTemplateName: tplName}
	return s
}

func jobStatus(failed bool) string {
	if failed {
		return "failed"
	}
	return "successful"
}

func TestExcludeRules_Matches(t *testing.T) {
	r := NewExcludeRules([]int{99}, []string{"Health Check", "  Probe  "})
	cases := []struct {
		id     int
		name   string
		want   bool
		reason string
	}{
		{40, "Check Auth Key", false, ""},
		{99, "Anything", true, "id"},
		{1, "Daily Health Check", true, "name:health check"},
		{2, "service probe v2", true, "name:probe"},
		{3, "ping", false, ""},
	}
	for _, tc := range cases {
		got, why := r.Matches(tc.id, tc.name)
		if got != tc.want || (tc.want && why != tc.reason) {
			t.Errorf("Matches(%d, %q) = (%v, %q), want (%v, %q)",
				tc.id, tc.name, got, why, tc.want, tc.reason)
		}
	}
}

func TestAddJob_CountsByStatus(t *testing.T) {
	a := newTestAgg(t, NewExcludeRules(nil, nil))
	a.AddJob(awx.JobLite{ID: 1, JobTemplate: 40, Status: "successful", Finished: fixedT(1)})
	a.AddJob(awx.JobLite{ID: 2, JobTemplate: 40, Status: "failed", Finished: fixedT(2)})
	a.AddJob(awx.JobLite{ID: 3, JobTemplate: 40, Status: "error", Finished: fixedT(3)})
	a.AddJob(awx.JobLite{ID: 4, JobTemplate: 40, Status: "canceled", Finished: fixedT(4)})

	got := a.Templates[40]
	if got.Jobs != 4 || got.JobsOK != 1 || got.JobsFailed != 1 || got.JobsError != 1 || got.JobsCanceled != 1 {
		t.Errorf("counts: total=%d ok=%d failed=%d error=%d canceled=%d",
			got.Jobs, got.JobsOK, got.JobsFailed, got.JobsError, got.JobsCanceled)
	}
	if !got.LastRun.Equal(fixedT(4)) {
		t.Errorf("LastRun = %v, want minute 4", got.LastRun)
	}
	if !got.LastOK.Equal(fixedT(1)) {
		t.Errorf("LastOK = %v, want minute 1", got.LastOK)
	}
	if !got.LastFailed.Equal(fixedT(3)) { // last of {failed=2, error=3}
		t.Errorf("LastFailed = %v, want minute 3", got.LastFailed)
	}
}

func TestAddJob_SkipsZeroTemplate(t *testing.T) {
	a := newTestAgg(t, NewExcludeRules(nil, nil))
	a.AddJob(awx.JobLite{ID: 1, JobTemplate: 0, Status: "successful", Finished: fixedT(1)})
	if a.UnknownTplJobs != 1 {
		t.Errorf("UnknownTplJobs = %d, want 1", a.UnknownTplJobs)
	}
	for _, tpl := range a.Templates {
		if tpl.Jobs != 0 {
			t.Errorf("template %d had %d jobs, expected 0", tpl.ID, tpl.Jobs)
		}
	}
}

func TestAddSummary_TemplateAndHostCounters(t *testing.T) {
	a := newTestAgg(t, NewExcludeRules(nil, nil))
	a.AddSummary(mkSummary(1, 6, 40, "Check Auth Key", false, fixedT(1)))
	a.AddSummary(mkSummary(2, 6, 40, "Check Auth Key", true, fixedT(2)))
	a.AddSummary(mkSummary(3, 6, 40, "Check Auth Key", false, fixedT(3)))

	tpl := a.Templates[40]
	if tpl.HostRuns != 3 || tpl.HostOK != 2 || tpl.HostFailed != 1 {
		t.Errorf("template counters: runs=%d ok=%d failed=%d", tpl.HostRuns, tpl.HostOK, tpl.HostFailed)
	}
	if len(tpl.DistinctHosts) != 1 {
		t.Errorf("DistinctHosts = %d, want 1", len(tpl.DistinctHosts))
	}

	host := a.Hosts[6]
	if host.Runs != 3 || host.OK != 2 || host.Failed != 1 {
		t.Errorf("host counters: runs=%d ok=%d failed=%d", host.Runs, host.OK, host.Failed)
	}
	if !host.EverOK {
		t.Error("EverOK should be true")
	}
	if !host.LastOK.Equal(fixedT(3)) {
		t.Errorf("host LastOK = %v, want minute 3", host.LastOK)
	}
	if !host.LastFailed.Equal(fixedT(2)) {
		t.Errorf("host LastFailed = %v, want minute 2", host.LastFailed)
	}

	pair := a.Pairs[PairKey{TemplateID: 40, HostID: 6}]
	if pair == nil {
		t.Fatal("pair (40,6) missing")
	}
	if pair.Runs != 3 || pair.OK != 2 || pair.Failed != 1 {
		t.Errorf("pair counters: runs=%d ok=%d failed=%d", pair.Runs, pair.OK, pair.Failed)
	}
}

func TestAddSummary_SyntheticHostForNullHostID(t *testing.T) {
	a := newTestAgg(t, NewExcludeRules(nil, nil))
	s := mkSummary(1, 0, 40, "Check Auth Key", false, fixedT(1))
	s.HostName = "localhost"
	a.AddSummary(s)

	// Synthetic id is negative; the host should appear under that id with the literal name.
	var found *HostAgg
	for id, h := range a.Hosts {
		if id < 0 && h.Name == "localhost" {
			found = h
			break
		}
	}
	if found == nil {
		t.Fatal("synthetic localhost host not registered")
	}
	if found.Runs != 1 {
		t.Errorf("synthetic host runs = %d, want 1", found.Runs)
	}

	// Subsequent localhost summaries should reuse the same synthetic id (no proliferation).
	a.AddSummary(s)
	count := 0
	for id := range a.Hosts {
		if id < 0 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 synthetic host id, got %d", count)
	}
}

func TestAddSummary_PrePopulatedTemplateExcludedFromConfig(t *testing.T) {
	ex := NewExcludeRules([]int{99}, nil)
	a := newTestAgg(t, ex)
	if !a.Templates[99].Excluded {
		t.Error("template 99 should be marked excluded from config")
	}
	if a.Templates[40].Excluded {
		t.Error("template 40 should not be excluded")
	}
}

func TestAddSummary_LazyTemplateInheritsExcludeFromName(t *testing.T) {
	ex := NewExcludeRules(nil, []string{"health"})
	a := newTestAgg(t, ex)

	// Template 500 isn't in the lookup; it's created lazily when a summary
	// arrives. Its name matches the exclude filter, so it should be marked.
	s := mkSummary(1, 6, 500, "Server Health Probe", false, fixedT(1))
	a.AddSummary(s)
	got := a.Templates[500]
	if got == nil || !got.Excluded {
		t.Errorf("template 500 should be excluded by name match: %+v", got)
	}
}
