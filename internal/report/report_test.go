package report

import (
	"encoding/csv"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/TheGU/awxreport/internal/aggregate"
	"github.com/TheGU/awxreport/internal/awx"
)

func newAgg(t *testing.T) *aggregate.Aggregator {
	t.Helper()
	l := &awx.Lookups{
		Hosts: map[int]awx.HostLite{
			6: {ID: 6, Name: "ubuntu", Inventory: 2},
		},
		Inventories: map[int]awx.InventoryLite{2: {ID: 2, Name: "lab"}},
		Templates: map[int]awx.TemplateLite{
			40: {ID: 40, Name: "Probe", Playbook: "p.yml"},
			99: {ID: 99, Name: "Health Check", Playbook: "h.yml"},
		},
		AnsibleHost: map[int]string{6: "10.0.0.6"},
	}
	a := aggregate.New(l, aggregate.NewExcludeRules([]int{99}, nil))
	a.AddJob(awx.JobLite{ID: 1, JobTemplate: 40, Status: "successful",
		Finished: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)})
	a.AddJob(awx.JobLite{ID: 2, JobTemplate: 99, Status: "successful",
		Finished: time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)})

	hp := 6
	s := awx.SummaryLite{
		ID: 100, Job: 1, Host: &hp, HostName: "ubuntu",
		Modified: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		OK:       3, Failed: false,
	}
	s.SummaryFields.Job = &struct {
		ID              int    `json:"id"`
		Name            string `json:"name"`
		Status          string `json:"status"`
		Failed          bool   `json:"failed"`
		JobTemplateID   int    `json:"job_template_id"`
		JobTemplateName string `json:"job_template_name"`
	}{ID: 1, Status: "successful", JobTemplateID: 40, JobTemplateName: "Probe"}
	a.AddSummary(s)
	return a
}

func TestWriteXLSX_AllSheetsPresent(t *testing.T) {
	a := newAgg(t)
	dir := t.TempDir()
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	path, err := WriteXLSX(dir, a, from, to)
	if err != nil {
		t.Fatalf("WriteXLSX: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("xlsx not created: %v", err)
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	want := []string{"Playbooks", "Hosts", "PlaybookHosts", "Excluded", "Meta"}
	got := f.GetSheetList()
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing sheet %q (got %v)", w, got)
		}
	}

	// Playbooks sheet should have a header row and at least the non-excluded template.
	rows, err := f.GetRows("Playbooks")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("Playbooks has %d rows, want ≥2", len(rows))
	}
	if rows[0][0] != "Template ID" {
		t.Errorf("first header cell = %q, want 'Template ID'", rows[0][0])
	}
	// Excluded template (99) must not appear here.
	for _, r := range rows[1:] {
		if r[0] == "99" {
			t.Errorf("excluded template 99 leaked into Playbooks sheet: %v", r)
		}
	}

	// Excluded sheet should contain template 99.
	exc, err := f.GetRows("Excluded")
	if err != nil {
		t.Fatal(err)
	}
	found99 := false
	for _, r := range exc[1:] {
		if r[0] == "99" {
			found99 = true
		}
	}
	if !found99 {
		t.Error("template 99 missing from Excluded sheet")
	}
}

func TestDetailCSV_HeaderAndRow(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	d, err := NewDetailCSV(dir, now)
	if err != nil {
		t.Fatal(err)
	}

	hp := 6
	s := awx.SummaryLite{
		ID: 1, Job: 10, Host: &hp, HostName: "ubuntu",
		Modified: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		OK:       3, Failed: false, Processed: 1,
	}
	s.SummaryFields.Job = &struct {
		ID              int    `json:"id"`
		Name            string `json:"name"`
		Status          string `json:"status"`
		Failed          bool   `json:"failed"`
		JobTemplateID   int    `json:"job_template_id"`
		JobTemplateName string `json:"job_template_name"`
	}{ID: 10, Status: "successful", JobTemplateID: 40, JobTemplateName: "Probe"}

	if err := d.Write(s, "10.0.0.6", 2, "lab", false); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(d.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (header + 1 data)", len(rows))
	}
	header := strings.Join(rows[0], ",")
	for _, col := range []string{"summary_id", "ansible_host", "inventory_name", "template_excluded"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing %q: %s", col, header)
		}
	}
	if rows[1][8] != "10.0.0.6" { // ansible_host column
		t.Errorf("ansible_host = %q, want '10.0.0.6'", rows[1][8])
	}
}
