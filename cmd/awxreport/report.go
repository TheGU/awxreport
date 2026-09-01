package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TheGU/awxreport/internal/aggregate"
	"github.com/TheGU/awxreport/internal/awx"
	"github.com/TheGU/awxreport/internal/config"
	"github.com/TheGU/awxreport/internal/report"
)

// reportOpts carries the report command's CLI flag values. Flags override
// (never merge with) the corresponding config.yaml lists.
type reportOpts struct {
	startDate, endDate string
	templateIDs        []int
	projectIDs         []int
	full               bool
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

// effectiveInclude applies CLI overrides to the config include lists.
//
// --full clears the include block entirely (full report) and cannot be
// combined with --template-ids/--project-ids. Otherwise, a non-empty flag
// list replaces (never merges with) the corresponding config list. Id
// validation lives here so both sources (flags and config-bypassing flags)
// get the same check, with an error message naming where the bad id came
// from.
func effectiveInclude(cfgInc config.IncludeFilter, ro reportOpts) (config.IncludeFilter, error) {
	if ro.full && (len(ro.templateIDs)+len(ro.projectIDs)) > 0 {
		return config.IncludeFilter{}, fmt.Errorf("--full cannot be combined with --template-ids or --project-ids")
	}
	if ro.full {
		return config.IncludeFilter{}, nil
	}

	inc := cfgInc
	if len(ro.templateIDs) > 0 {
		inc.TemplateIDs = ro.templateIDs
	}
	if len(ro.projectIDs) > 0 {
		inc.ProjectIDs = ro.projectIDs
	}
	for _, id := range inc.TemplateIDs {
		if id < 1 {
			return config.IncludeFilter{}, fmt.Errorf("invalid template id %d from --template-ids or include.template_ids: must be a positive integer", id)
		}
	}
	for _, id := range inc.ProjectIDs {
		if id < 1 {
			return config.IncludeFilter{}, fmt.Errorf("invalid project id %d from --project-ids or include.project_ids: must be a positive integer", id)
		}
	}
	return inc, nil
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

	inc, err := effectiveInclude(cfg.Include, ro)
	if err != nil {
		return err
	}
	fullOverride := ro.full && cfg.Include.Active()
	var ignoredTemplateIDs, ignoredProjectIDs []int
	if fullOverride {
		ignoredTemplateIDs = cfg.Include.TemplateIDs
		ignoredProjectIDs = cfg.Include.ProjectIDs
	}

	if cfg.SummaryStrategy == "per_host" && inc.Active() {
		return fmt.Errorf("summary_strategy per_host is not supported in selective mode; selective runs use the per-job path")
	}

	modeStr := "full"
	if inc.Active() {
		modeStr = fmt.Sprintf("selective (%d requested)", len(inc.TemplateIDs)+len(inc.ProjectIDs))
	}

	u.banner(fmt.Sprintf("awxreport - %s%s", cfg.BaseURL, cfg.APIRoot))
	u.table([][2]string{
		{"window", fmt.Sprintf("%s .. %s (%.0f days)",
			since.Format(time.RFC3339), until.Format(time.RFC3339),
			until.Sub(since).Hours()/24)},
		{"output", cfg.OutputDir},
		{"pacing", fmt.Sprintf("%dms", cfg.RequestPacingMS)},
		{"page size", fmt.Sprintf("%d", cfg.PageSize)},
		{"debug dir", emptyDash(cfg.DebugDir)},
		{"mode", modeStr},
		{"summary strategy", cfg.SummaryStrategy},
		{"summary workers", fmt.Sprintf("%d", cfg.SummaryWorkers)},
	})
	if fullOverride {
		u.warn("--full: ignoring config include block (template_ids=%s project_ids=%s)",
			joinIntsComma(ignoredTemplateIDs), joinIntsComma(ignoredProjectIDs))
	}

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
	hostsWalked := -1
	hostPruning := ""

	if cfg.SummaryStrategy == "per_host" {
		hostsWalked, hostPruning, err = runPerHostStrategy(ctx, u, client, cfg, agg, lookups, csvOut, since, until, t1)
	} else {
		err = runPerJobStrategy(ctx, u, client, cfg, agg, lookups, csvOut, since, until, selected, t1)
	}
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
	if agg.SummariesUnknownTpl > 0 {
		u.warn("%d summar%s could not be attributed to a template (summary_fields.job missing)",
			agg.SummariesUnknownTpl, pluralY(agg.SummariesUnknownTpl))
	}

	if cfg.SummaryStrategy == "per_host" {
		var zeroHostRunTemplates int
		for _, t := range agg.Templates {
			if t.Jobs > 0 && t.HostRuns == 0 && !t.Excluded {
				zeroHostRunTemplates++
			}
		}
		if zeroHostRunTemplates > 0 {
			u.warn("%d template(s) have jobs but zero host summary rows in this per_host walk (possible wholesale summary loss)", zeroHostRunTemplates)
		}
	}

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
		Strategy:             cfg.SummaryStrategy,
		Workers:              cfg.SummaryWorkers,
		HostsWalked:          hostsWalked,
		HostPruning:          hostPruning,
		FullOverride:         fullOverride,
		IgnoredTemplateIDs:   ignoredTemplateIDs,
		IgnoredProjectIDs:    ignoredProjectIDs,
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
	if agg.SummariesUnknownTpl > 0 {
		summary = append(summary, [2]string{"summaries unknown template", fmt.Sprintf("%d", agg.SummariesUnknownTpl)})
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

// runPerJobStrategy is the default summary fetch path: for every job in the
// window, fetch that job's job_host_summaries. It streams both job counters
// and per-summary CSV rows in job-id order.
func runPerJobStrategy(ctx context.Context, u *ui, client *awx.Client, cfg *loadedConfig,
	agg *aggregate.Aggregator, lookups *awx.Lookups, csvOut *report.DetailCSV,
	since, until time.Time, selected []int, t1 time.Time) error {

	return client.IterateJobsWithSummaries(ctx, since, until, selected, cfg.SummaryWorkers,
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
			// onSummary is serialized by the runner, so this progress call
			// is as safe as the onJob one above -- it just also fires
			// during the concurrent summary fan-out, where onJob doesn't.
			if int(agg.SummariesSeen)%200 == 0 {
				u.progress("jobs=%d  summaries=%d  pairs=%d  rate=%s  elapsed=%s",
					agg.JobsSeen, agg.SummariesSeen, len(agg.Pairs),
					fmtRate(agg.JobsSeen, time.Since(t1)),
					time.Since(t1).Truncate(time.Second))
			}
			ansHost, invID, invName, excluded := summaryEnrichment(s, lookups, agg)
			return csvOut.Write(s, ansHost, invID, invName, excluded)
		},
	)
}

// runPerHostStrategy is the opt-in summary fetch path: walk every active
// host's job_host_summaries instead of every job's, trading missing
// null-host/deleted-host rows for far fewer requests on job-heavy
// controllers. Returns the number of hosts walked and how the host set was
// chosen, for the Meta sheet.
func runPerHostStrategy(ctx context.Context, u *ui, client *awx.Client, cfg *loadedConfig,
	agg *aggregate.Aggregator, lookups *awx.Lookups, csvOut *report.DetailCSV,
	since, until time.Time, t1 time.Time) (hostsWalked int, hostPruning string, retErr error) {

	// Host pruning first, so the projection line below reports a real
	// number instead of "(pending)".
	activeIDs, supported, err := client.FetchActiveHostIDs(ctx, since)
	if err != nil {
		return -1, "", fmt.Errorf("host activity lookup: %w", err)
	}
	var hostIDs []int
	if supported {
		hostIDs = activeIDs
		hostPruning = "active-host filter"
	} else {
		hostIDs = sortedLookupHostIDs(lookups)
		hostPruning = "unsupported, walked all hosts"
		u.warn("host activity filter not supported; walking all %d hosts", len(hostIDs))
	}

	jobCount, err := client.CountJobs(ctx, since, until)
	if err != nil {
		u.warn("could not count jobs in window for projection: %v", err)
	} else {
		u.ok("strategy=per_host: ~%d host walks vs ~%d per-job summary requests", len(hostIDs), jobCount)
	}

	// Mandatory pre-flight: confirm the controller accepts job__finished
	// filtering on the per-host summaries endpoint before committing to the
	// whole walk. Prefer a host from the pruned active set (when pruning
	// worked and found any) over an arbitrary lookup host, so the
	// returned-row half of the check -- summary_fields.job presence -- has
	// real data to look at instead of likely coming back empty.
	var sampleHostID int
	var ok bool
	if supported && len(hostIDs) > 0 {
		sampleHostID, ok = hostIDs[0], true
	} else {
		sampleHostID, ok = firstLookupHostID(lookups)
	}
	if !ok {
		return -1, "", fmt.Errorf("summary_strategy per_host pre-flight requires at least one host on the controller")
	}
	row, err := preflightPerHost(ctx, client, sampleHostID, since, until)
	if err != nil {
		return -1, "", fmt.Errorf("summary_strategy per_host pre-flight failed on hosts/%d/job_host_summaries/ with job__finished filters: %w (the controller may not support job__finished filtering there, which per_host requires)", sampleHostID, err)
	}
	if row != nil && row.SummaryFields.Job == nil {
		u.warn("per_host pre-flight: summary_fields.job missing on the sample row; template attribution may be incomplete for some rows (see 'Summaries with unknown template' in the report)")
	}

	u.warn("per_host cannot see summary rows whose host record is gone: rows with host null (ad-hoc and localhost plays) and rows for hosts deleted since their jobs ran are missing from this report; use summary_strategy per_job for full fidelity")

	// Step 2a: job counters, unknown_tpl detection, keyset -- no summaries.
	if err := client.IterateJobs(ctx, since, until, nil, func(j awx.JobLite) error {
		agg.AddJob(j)
		return nil
	}); err != nil {
		return -1, "", fmt.Errorf("job walk: %w", err)
	}
	u.ok("jobs=%d  unknown_tpl=%d  (%.1fs)", agg.JobsSeen, agg.UnknownTplJobs, time.Since(t1).Seconds())

	// Step 2b: per-host summary walk.
	err = client.IterateHostSummaries(ctx, hostIDs, since, until, cfg.SummaryWorkers,
		func(s awx.SummaryLite) error {
			agg.AddSummary(s)
			ansHost, invID, invName, excluded := summaryEnrichment(s, lookups, agg)
			return csvOut.Write(s, ansHost, invID, invName, excluded)
		},
		func(hostID, done, total int) error {
			u.progress("hosts=%d/%d  summaries=%d  elapsed=%s", done, total, agg.SummariesSeen, time.Since(t1).Truncate(time.Second))
			return nil
		},
	)
	if err != nil {
		return -1, "", err
	}

	return len(hostIDs), hostPruning, nil
}

// summaryEnrichment resolves the CSV columns not present on SummaryLite
// itself: ansible_host and inventory id/name from the host lookup, and
// whether the row's template is excluded (from the aggregator, since
// AddSummary may have just created the template entry).
func summaryEnrichment(s awx.SummaryLite, lookups *awx.Lookups, agg *aggregate.Aggregator) (ansHost string, invID int, invName string, excluded bool) {
	if s.Host != nil {
		if h, ok := lookups.Hosts[*s.Host]; ok {
			ansHost = lookups.AnsibleHost[h.ID]
			invID, invName = lookups.HostInventoryName(h.ID)
		}
	}
	if s.SummaryFields.Job != nil {
		if t := agg.Templates[s.SummaryFields.Job.JobTemplateID]; t != nil {
			excluded = t.Excluded
		}
	}
	return
}

// errPreflightStop aborts Paginate after one page in preflightPerHost, the
// same one-page-only trick internal/awx uses internally (see errStop there).
var errPreflightStop = errors.New("preflight stop")

// preflightPerHost issues one page_size=1 request to a sample host's
// job_host_summaries endpoint with both job__finished filters, to confirm
// the controller supports that filtering before the full per_host walk
// commits to it. Returns the sample row, or nil if the host has none in the
// window.
func preflightPerHost(ctx context.Context, client *awx.Client, hostID int, since, until time.Time) (*awx.SummaryLite, error) {
	q := url.Values{
		"job__finished__gte": []string{since.UTC().Format(time.RFC3339)},
		"job__finished__lt":  []string{until.UTC().Format(time.RFC3339)},
		"page_size":          []string{"1"},
	}
	path := fmt.Sprintf("hosts/%d/job_host_summaries/", hostID)
	var row *awx.SummaryLite
	err := client.Paginate(ctx, "job_host_summaries", path, q, func(_ context.Context, p awx.Page) error {
		var rows []awx.SummaryLite
		if uerr := json.Unmarshal(p.Results, &rows); uerr != nil {
			return fmt.Errorf("decode preflight summaries: %w", uerr)
		}
		if len(rows) > 0 {
			row = &rows[0]
		}
		return errPreflightStop
	})
	if err != nil && !errors.Is(err, errPreflightStop) {
		return nil, err
	}
	return row, nil
}

// firstLookupHostID returns the lowest host id in the lookup table, or
// ok=false when there are none.
func firstLookupHostID(lookups *awx.Lookups) (id int, ok bool) {
	ids := sortedLookupHostIDs(lookups)
	if len(ids) == 0 {
		return 0, false
	}
	return ids[0], true
}

// sortedLookupHostIDs returns every host id known to the lookup table,
// ascending -- the fallback host set when the active-host filter is
// unsupported.
func sortedLookupHostIDs(lookups *awx.Lookups) []int {
	ids := make([]int, 0, len(lookups.Hosts))
	for id := range lookups.Hosts {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// joinIntsComma renders ids as a comma-separated string.
func joinIntsComma(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// pluralY returns "ies" for n != 1, "y" for n == 1 (summary/summaries).
func pluralY(n int64) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
