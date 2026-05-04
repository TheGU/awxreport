// Package report writes the aggregator output to XLSX and CSV.
package report

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/TheGU/awxreport/internal/aggregate"
)

// WriteXLSX renders the four-sheet report. It uses excelize StreamWriter so
// the PlaybookHosts sheet (potentially hundreds of thousands of rows) doesn't
// hold the whole grid in memory.
func WriteXLSX(outDir string, agg *aggregate.Aggregator, windowFrom, windowTo time.Time) (string, error) {
	f := excelize.NewFile()
	defer f.Close()

	// Default sheet "Sheet1" is created by NewFile; rename it for "Playbooks".
	if err := f.SetSheetName("Sheet1", "Playbooks"); err != nil {
		return "", err
	}
	if _, err := f.NewSheet("Hosts"); err != nil {
		return "", err
	}
	if _, err := f.NewSheet("PlaybookHosts"); err != nil {
		return "", err
	}
	if _, err := f.NewSheet("Excluded"); err != nil {
		return "", err
	}
	if _, err := f.NewSheet("Meta"); err != nil {
		return "", err
	}

	if err := writePlaybooks(f, agg); err != nil {
		return "", fmt.Errorf("playbooks sheet: %w", err)
	}
	if err := writeHosts(f, agg); err != nil {
		return "", fmt.Errorf("hosts sheet: %w", err)
	}
	if err := writePlaybookHosts(f, agg); err != nil {
		return "", fmt.Errorf("playbookhosts sheet: %w", err)
	}
	if err := writeExcluded(f, agg); err != nil {
		return "", fmt.Errorf("excluded sheet: %w", err)
	}
	if err := writeMeta(f, agg, windowFrom, windowTo); err != nil {
		return "", fmt.Errorf("meta sheet: %w", err)
	}

	f.SetActiveSheet(0)

	stamp := time.Now().UTC().Format("20060102-150405")
	path := filepath.Join(outDir, fmt.Sprintf("awx-rollout-%s.xlsx", stamp))
	if err := f.SaveAs(path); err != nil {
		return "", err
	}
	return path, nil
}

func writePlaybooks(f *excelize.File, agg *aggregate.Aggregator) error {
	sw, err := f.NewStreamWriter("Playbooks")
	if err != nil {
		return err
	}
	header := []any{
		"Template ID", "Template Name", "Playbook",
		"Jobs Total", "Jobs OK", "Jobs Failed", "Jobs Error", "Jobs Canceled",
		"Distinct Hosts", "Host Runs", "Host OK", "Host Failed", "Host Dark",
		"Last Run (UTC)", "Last OK (UTC)", "Last Failed (UTC)",
	}
	if err := sw.SetRow("A1", header); err != nil {
		return err
	}

	ids := sortedTemplateIDs(agg, false)
	row := 2
	for _, id := range ids {
		t := agg.Templates[id]
		cell, _ := excelize.CoordinatesToCellName(1, row)
		err := sw.SetRow(cell, []any{
			t.ID, t.Name, t.Playbook,
			t.Jobs, t.JobsOK, t.JobsFailed, t.JobsError, t.JobsCanceled,
			len(t.DistinctHosts), t.HostRuns, t.HostOK, t.HostFailed, t.HostDark,
			fmtTime(t.LastRun), fmtTime(t.LastOK), fmtTime(t.LastFailed),
		})
		if err != nil {
			return err
		}
		row++
	}
	return sw.Flush()
}

func writeHosts(f *excelize.File, agg *aggregate.Aggregator) error {
	sw, err := f.NewStreamWriter("Hosts")
	if err != nil {
		return err
	}
	header := []any{
		"Host ID", "Host Name", "Ansible Host",
		"Inventory ID", "Inventory Name",
		"Enabled", "Ever OK", "Distinct Templates",
		"Total Runs", "OK Runs", "Failed Runs", "Dark",
		"Last Run (UTC)", "Last OK (UTC)", "Last Failed (UTC)",
	}
	if err := sw.SetRow("A1", header); err != nil {
		return err
	}

	ids := make([]int, 0, len(agg.Hosts))
	for id := range agg.Hosts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		// Real (positive) hosts first, ordered by id; synthetic hosts
		// (negative ids) at the bottom by name.
		a, b := ids[i], ids[j]
		if (a > 0) != (b > 0) {
			return a > 0
		}
		if a > 0 {
			return a < b
		}
		return agg.Hosts[a].Name < agg.Hosts[b].Name
	})

	row := 2
	for _, id := range ids {
		h := agg.Hosts[id]
		idCell := id
		if id < 0 {
			idCell = 0 // hide synthetic id from human readers
		}
		cell, _ := excelize.CoordinatesToCellName(1, row)
		err := sw.SetRow(cell, []any{
			idCell, h.Name, h.AnsibleHost,
			h.InventoryID, h.InventoryName,
			h.Enabled, h.EverOK, len(h.DistinctTemplates),
			h.Runs, h.OK, h.Failed, h.Dark,
			fmtTime(h.LastRun), fmtTime(h.LastOK), fmtTime(h.LastFailed),
		})
		if err != nil {
			return err
		}
		row++
	}
	return sw.Flush()
}

func writePlaybookHosts(f *excelize.File, agg *aggregate.Aggregator) error {
	sw, err := f.NewStreamWriter("PlaybookHosts")
	if err != nil {
		return err
	}
	header := []any{
		"Template ID", "Template Name",
		"Host ID", "Host Name", "Ansible Host",
		"Inventory ID", "Inventory Name",
		"Runs", "OK", "Failed", "Changed", "Dark",
		"Last Run (UTC)", "Last OK (UTC)", "Last Failed (UTC)",
	}
	if err := sw.SetRow("A1", header); err != nil {
		return err
	}

	keys := make([]aggregate.PairKey, 0, len(agg.Pairs))
	for k := range agg.Pairs {
		// Skip excluded templates from this sheet — they only appear in the
		// Excluded summary sheet, per design.
		if t := agg.Templates[k.TemplateID]; t != nil && t.Excluded {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].TemplateID != keys[j].TemplateID {
			return keys[i].TemplateID < keys[j].TemplateID
		}
		return keys[i].HostID < keys[j].HostID
	})

	row := 2
	for _, k := range keys {
		p := agg.Pairs[k]
		t := agg.Templates[k.TemplateID]
		h := agg.Hosts[k.HostID]
		hostIDCell := k.HostID
		if hostIDCell < 0 {
			hostIDCell = 0
		}
		var tplName, hostName, ansHost, invName string
		var invID int
		if t != nil {
			tplName = t.Name
		}
		if h != nil {
			hostName = h.Name
			ansHost = h.AnsibleHost
			invID = h.InventoryID
			invName = h.InventoryName
		}
		cell, _ := excelize.CoordinatesToCellName(1, row)
		err := sw.SetRow(cell, []any{
			k.TemplateID, tplName,
			hostIDCell, hostName, ansHost,
			invID, invName,
			p.Runs, p.OK, p.Failed, p.Changed, p.Dark,
			fmtTime(p.LastRun), fmtTime(p.LastOK), fmtTime(p.LastFailed),
		})
		if err != nil {
			return err
		}
		row++
	}
	return sw.Flush()
}

func writeExcluded(f *excelize.File, agg *aggregate.Aggregator) error {
	sw, err := f.NewStreamWriter("Excluded")
	if err != nil {
		return err
	}
	header := []any{
		"Template ID", "Template Name", "Match Reason",
		"Jobs Total", "Jobs OK", "Jobs Failed", "Jobs Error", "Jobs Canceled",
		"Distinct Hosts", "Host Runs",
	}
	if err := sw.SetRow("A1", header); err != nil {
		return err
	}

	ids := sortedTemplateIDs(agg, true)
	row := 2
	for _, id := range ids {
		t := agg.Templates[id]
		cell, _ := excelize.CoordinatesToCellName(1, row)
		err := sw.SetRow(cell, []any{
			t.ID, t.Name, t.ExcludeReason,
			t.Jobs, t.JobsOK, t.JobsFailed, t.JobsError, t.JobsCanceled,
			len(t.DistinctHosts), t.HostRuns,
		})
		if err != nil {
			return err
		}
		row++
	}
	return sw.Flush()
}

func writeMeta(f *excelize.File, agg *aggregate.Aggregator, from, to time.Time) error {
	rows := [][]any{
		{"Generated (UTC)", time.Now().UTC().Format(time.RFC3339)},
		{"Window from (UTC)", from.UTC().Format(time.RFC3339)},
		{"Window to (UTC)", to.UTC().Format(time.RFC3339)},
		{"Jobs seen", agg.JobsSeen},
		{"Summaries seen", agg.SummariesSeen},
		{"Jobs with unknown template", agg.UnknownTplJobs},
		{"Templates in lookup", len(agg.Lookups.Templates)},
		{"Hosts in lookup", len(agg.Lookups.Hosts)},
		{"Inventories in lookup", len(agg.Lookups.Inventories)},
	}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Meta", cell, &r); err != nil {
			return err
		}
	}
	return nil
}

// sortedTemplateIDs returns template IDs filtered by Excluded flag, sorted by name.
func sortedTemplateIDs(agg *aggregate.Aggregator, excluded bool) []int {
	ids := make([]int, 0, len(agg.Templates))
	for id, t := range agg.Templates {
		if t.Excluded != excluded {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ni := agg.Templates[ids[i]].Name
		nj := agg.Templates[ids[j]].Name
		if ni != nj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})
	return ids
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
