package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TheGU/awxreport/internal/aggregate"
	"github.com/TheGU/awxreport/internal/awx"
	"github.com/TheGU/awxreport/internal/report"
)

// reportOpts carries the report command's CLI flag values. Flags override
// (never merge with) the corresponding config.yaml lists.
type reportOpts struct {
	startDate, endDate string
	templateIDs        []int
	projectIDs         []int
}

// reportWindow resolves the export window. startStr and endStr are
// YYYY-MM-DD dates in UTC; the end date is inclusive. A missing end defaults
// to now; a missing start defaults to daysBack days before the end.
func reportWindow(startStr, endStr string, daysBack int, now time.Time) (since, until time.Time, err error) {
	until = now
	if endStr != "" {
		d, err := time.ParseInLocation("2006-01-02", endStr, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid --end-date %q: want YYYY-MM-DD", endStr)
		}
		until = d.AddDate(0, 0, 1) // inclusive end date
	}
	since = until.AddDate(0, 0, -daysBack)
	if startStr != "" {
		since, err = time.ParseInLocation("2006-01-02", startStr, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid --start-date %q: want YYYY-MM-DD", startStr)
		}
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, fmt.Errorf("start date must not be after end date")
	}
	return since, until, nil
}

func runReport(ctx context.Context, u *ui, opts globalOpts, ro reportOpts) (retErr error) {
	cfg, client, err := loadAndConnect(opts)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close client: %w", err)
		}
	}()

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	now := time.Now().UTC()
	since, until, err := reportWindow(ro.startDate, ro.endDate, cfg.DaysBack, now)
	if err != nil {
		return err
	}

	// Effective include: flags replace (never merge with) the config lists.
	inc := cfg.Include
	if len(ro.templateIDs) > 0 {
		inc.TemplateIDs = ro.templateIDs
	}
	if len(ro.projectIDs) > 0 {
		inc.ProjectIDs = ro.projectIDs
	}
	// Config-file ids are validated in config.go; flag-supplied ids bypass
	// that path and need the same check here.
	for _, id := range inc.TemplateIDs {
		if id < 1 {
			return fmt.Errorf("invalid template id %d: must be a positive integer", id)
		}
	}
	for _, id := range inc.ProjectIDs {
		if id < 1 {
			return fmt.Errorf("invalid project id %d: must be a positive integer", id)
		}
	}
	modeStr := "full"
	if inc.Active() {
		modeStr = fmt.Sprintf("selective (%d requested)", len(inc.TemplateIDs)+len(inc.ProjectIDs))
	}

	u.banner(fmt.Sprintf("awxreport — %s%s", cfg.BaseURL, cfg.APIRoot))
	u.table([][2]string{
		{"window", fmt.Sprintf("%s .. %s (%.0f days)",
			since.Format(time.RFC3339), until.Format(time.RFC3339),
			until.Sub(since).Hours()/24)},
		{"output", cfg.OutputDir},
		{"pacing", fmt.Sprintf("%dms", cfg.RequestPacingMS)},
		{"page size", fmt.Sprintf("%d", cfg.PageSize)},
		{"debug dir", emptyDash(cfg.DebugDir)},
		{"mode", modeStr},
	})

	// Step 1: lookup tables.
	u.section("[1/3] Lookup tables")
	t0 := time.Now()
	lookups, err := client.FetchLookups(ctx)
	if err != nil {
		return fmt.Errorf("lookups: %w", err)
	}
	u.ok("templates=%d  inventories=%d  hosts=%d  ansible_host=%d  (%.1fs)",
		len(lookups.Templates), len(lookups.Inventories), len(lookups.Hosts),
		len(lookups.AnsibleHost), time.Since(t0).Seconds())

	excl := aggregate.NewExcludeRules(cfg.ExcludeTemplates.IDs, cfg.ExcludeTemplates.NameContains)
	selected, removed, err := aggregate.ResolveSelection(lookups, inc.TemplateIDs, inc.ProjectIDs, excl)
	if err != nil {
		return fmt.Errorf("selection: %w", err)
	}

	totalJobsInWindow := -1
	if len(selected) > 0 {
		u.ok("selection resolved: templates=%d removed_by_exclude=%d", len(selected), len(removed))
		if len(removed) > 0 {
			u.warn("removed from selection by exclude rules: %s", joinIntsComma(removed))
		}
		var countErr error
		totalJobsInWindow, countErr = client.CountJobs(ctx, since, until)
		if countErr != nil {
			u.warn("could not count jobs in window: %v", countErr)
			totalJobsInWindow = -1
		}
	}

	// Step 2: stream jobs + summaries.
	u.section("[2/3] Iterating jobs and host summaries")
	agg := aggregate.New(lookups, excl, selected)

	csvOut, err := report.NewDetailCSV(cfg.OutputDir, now)
	if err != nil {
		return fmt.Errorf("open detail csv: %w", err)
	}

	t1 := time.Now()
	err = client.IterateJobsWithSummaries(ctx, since, until, selected,
		func(j awx.JobLite) error {
			if agg.Selected != nil && !agg.Selected[j.JobTemplate] {
				return fmt.Errorf("controller returned job %d (template %d) outside the selection; job_template__in filter was not honored", j.ID, j.JobTemplate)
			}
			agg.AddJob(j)
			if int(agg.JobsSeen)%10 == 0 {
				u.progress("jobs=%d  summaries=%d  pairs=%d  rate=%s  elapsed=%s",
					agg.JobsSeen, agg.SummariesSeen, len(agg.Pairs),
					fmtRate(agg.JobsSeen, time.Since(t1)),
					time.Since(t1).Truncate(time.Second))
			}
			return nil
		},
		func(s awx.SummaryLite) error {
			agg.AddSummary(s)

			ansHost := ""
			invID := 0
			invName := ""
			excluded := false
			if s.Host != nil {
				if h, ok := lookups.Hosts[*s.Host]; ok {
					ansHost = lookups.AnsibleHost[h.ID]
					iid, iname := lookups.HostInventoryName(h.ID)
					invID, invName = iid, iname
				}
			}
			if s.SummaryFields.Job != nil {
				if t := agg.Templates[s.SummaryFields.Job.JobTemplateID]; t != nil {
					excluded = t.Excluded
				}
			}
			return csvOut.Write(s, ansHost, invID, invName, excluded)
		},
	)
	u.progressDone()
	if err != nil {
		_ = csvOut.Close()
		return fmt.Errorf("iteration: %w", err)
	}
	if err := csvOut.Close(); err != nil {
		return fmt.Errorf("close detail csv: %w", err)
	}
	u.ok("jobs=%d  summaries=%d  pairs=%d  unknown_tpl=%d  (%.1fs)",
		agg.JobsSeen, agg.SummariesSeen, len(agg.Pairs), agg.UnknownTplJobs,
		time.Since(t1).Seconds())

	// Step 3: render XLSX.
	u.section("[3/3] Rendering XLSX")
	t2 := time.Now()
	meta := report.RunMeta{
		Selective:            len(selected) > 0,
		RequestedTemplateIDs: inc.TemplateIDs,
		RequestedProjectIDs:  inc.ProjectIDs,
		SelectedTemplates:    selected,
		RemovedByExclude:     removed,
		TotalJobsInWindow:    totalJobsInWindow,
		ResolvedAt:           now,
	}
	xlsxPath, err := report.WriteXLSX(cfg.OutputDir, agg, since, until, meta)
	if err != nil {
		return fmt.Errorf("write xlsx: %w", err)
	}
	u.ok("xlsx written  (%.2fs)", time.Since(t2).Seconds())

	// Final summary.
	u.section("Done")
	summary := [][2]string{
		{"jobs", fmt.Sprintf("%d", agg.JobsSeen)},
		{"summaries", fmt.Sprintf("%d", agg.SummariesSeen)},
		{"pairs", fmt.Sprintf("%d", len(agg.Pairs))},
		{"xlsx", xlsxPath},
		{"detail csv", csvOut.Path()},
		{"elapsed", time.Since(t0).Truncate(time.Second).String()},
	}
	if meta.Selective {
		jobsInWindowStr := "(not measured)"
		jobsSkippedStr := "(not measured)"
		if totalJobsInWindow >= 0 {
			jobsInWindowStr = fmt.Sprintf("%d", totalJobsInWindow)
			jobsSkippedStr = fmt.Sprintf("%d", totalJobsInWindow-int(agg.JobsSeen))
		}
		summary = append(summary,
			[2]string{"jobs in window (all)", jobsInWindowStr},
			[2]string{"jobs skipped by selection", jobsSkippedStr},
		)
	}
	u.table(summary)
	return nil
}

// joinIntsComma renders ids as a comma-separated string.
func joinIntsComma(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}
