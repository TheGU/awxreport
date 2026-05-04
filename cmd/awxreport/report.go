package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/TheGU/awxreport/internal/aggregate"
	"github.com/TheGU/awxreport/internal/awx"
	"github.com/TheGU/awxreport/internal/report"
)

func runReport(ctx context.Context, u *ui, opts globalOpts) error {
	cfg, client, err := loadAndConnect(opts)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -cfg.DaysBack)

	u.banner(fmt.Sprintf("awxreport — %s%s", cfg.BaseURL, cfg.APIRoot))
	u.table([][2]string{
		{"window", fmt.Sprintf("%s .. %s (%d days)",
			since.Format(time.RFC3339), now.Format(time.RFC3339), cfg.DaysBack)},
		{"output", cfg.OutputDir},
		{"pacing", fmt.Sprintf("%dms", cfg.RequestPacingMS)},
		{"page size", fmt.Sprintf("%d", cfg.PageSize)},
		{"debug dir", emptyDash(cfg.DebugDir)},
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

	// Step 2: stream jobs + summaries.
	u.section("[2/3] Iterating jobs and host summaries")
	excl := aggregate.NewExcludeRules(cfg.ExcludeTemplates.IDs, cfg.ExcludeTemplates.NameContains)
	agg := aggregate.New(lookups, excl)

	csvOut, err := report.NewDetailCSV(cfg.OutputDir, now)
	if err != nil {
		return fmt.Errorf("open detail csv: %w", err)
	}

	t1 := time.Now()
	err = client.IterateJobsWithSummaries(ctx, since,
		func(j awx.JobLite) error {
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
	xlsxPath, err := report.WriteXLSX(cfg.OutputDir, agg, since, now)
	if err != nil {
		return fmt.Errorf("write xlsx: %w", err)
	}
	u.ok("xlsx written  (%.2fs)", time.Since(t2).Seconds())

	// Final summary.
	u.section("Done")
	u.table([][2]string{
		{"jobs",       fmt.Sprintf("%d", agg.JobsSeen)},
		{"summaries",  fmt.Sprintf("%d", agg.SummariesSeen)},
		{"pairs",      fmt.Sprintf("%d", len(agg.Pairs))},
		{"xlsx",       xlsxPath},
		{"detail csv", csvOut.Path()},
		{"elapsed",    time.Since(t0).Truncate(time.Second).String()},
	})
	return nil
}
