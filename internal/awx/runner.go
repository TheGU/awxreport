package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// JobVisitor is called once per job.
type JobVisitor func(j JobLite) error

// SummaryVisitor is called once per job_host_summary row. Called in
// streaming order — the caller is expected to aggregate and discard.
type SummaryVisitor func(s SummaryLite) error

// jobTemplateChunkSize is the max number of ids joined into one
// job_template__in filter, chosen to keep the generated query string well
// under typical server URL length limits.
const jobTemplateChunkSize = 200

// IterateJobsWithSummaries pages through completed jobs in [since, until) and
// for each job pages through its job_host_summaries.
//
// When templateIDs is non-empty, jobs are additionally filtered server-side
// with job_template__in, split into chunks of jobTemplateChunkSize ids (one
// request series per chunk). Jobs whose job_template FK is null are excluded
// by the filter server-side.
//
// We do NOT hold all jobs or summaries in memory. Each call is one page; the
// visitor must aggregate and return quickly. Pacing is enforced inside Get,
// so this respects the rate budget without extra coordination.
//
// Heuristics:
//   - Order jobs ascending by id, so debug dumps are deterministic across
//     re-runs of the same window.
//   - We filter on `finished__gte` / `finished__lt`. Running/pending jobs in
//     the window have no `finished` set yet and are excluded — desirable for
//     a monthly report that should reflect completed runs.
func (c *Client) IterateJobsWithSummaries(ctx context.Context, since, until time.Time, templateIDs []int,
	onJob JobVisitor, onSummary SummaryVisitor) error {

	baseQ := url.Values{
		"finished__gte": []string{since.UTC().Format(time.RFC3339)},
		"finished__lt":  []string{until.UTC().Format(time.RFC3339)},
		"order_by":      []string{"id"},
	}

	visit := func(ctx context.Context, p Page) error {
		var rows []JobLite
		if err := json.Unmarshal(p.Results, &rows); err != nil {
			return fmt.Errorf("decode jobs page %d: %w", p.Index, err)
		}
		for _, j := range rows {
			if err := onJob(j); err != nil {
				return err
			}
			if err := c.iterJobSummaries(ctx, j.ID, onSummary); err != nil {
				return fmt.Errorf("job %d summaries: %w", j.ID, err)
			}
		}
		return nil
	}

	if len(templateIDs) == 0 {
		return c.Paginate(ctx, "jobs", "jobs/", baseQ, visit)
	}

	ids := make([]int, len(templateIDs))
	copy(ids, templateIDs)
	sort.Ints(ids)
	ids = dedupeInts(ids)

	for start := 0; start < len(ids); start += jobTemplateChunkSize {
		end := start + jobTemplateChunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunkQ := url.Values{}
		for k, v := range baseQ {
			chunkQ[k] = v
		}
		chunkQ.Set("job_template__in", joinInts(ids[start:end]))
		if err := c.Paginate(ctx, "jobs", "jobs/", chunkQ, visit); err != nil {
			return err
		}
	}
	return nil
}

// dedupeInts removes adjacent duplicates from a sorted slice.
func dedupeInts(sorted []int) []int {
	if len(sorted) == 0 {
		return sorted
	}
	out := sorted[:1]
	for _, v := range sorted[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// joinInts renders ids as a comma-separated string for job_template__in.
func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// CountJobs returns the total number of completed jobs in [since, until)
// with no template filter, from the list envelope count of a single
// page_size=1 request.
func (c *Client) CountJobs(ctx context.Context, since, until time.Time) (int, error) {
	q := url.Values{
		"finished__gte": []string{since.UTC().Format(time.RFC3339)},
		"finished__lt":  []string{until.UTC().Format(time.RFC3339)},
		"page_size":     []string{"1"},
	}
	count := 0
	err := c.Paginate(ctx, "jobs", "jobs/", q, func(_ context.Context, p Page) error {
		count = p.Count
		return errStop
	})
	if err == errStop {
		err = nil
	}
	return count, err
}

func (c *Client) iterJobSummaries(ctx context.Context, jobID int, onSummary SummaryVisitor) error {
	path := fmt.Sprintf("jobs/%d/job_host_summaries/", jobID)
	return c.Paginate(ctx, "job_host_summaries", path, nil, func(_ context.Context, p Page) error {
		var rows []SummaryLite
		if err := json.Unmarshal(p.Results, &rows); err != nil {
			return fmt.Errorf("decode summaries page %d: %w", p.Index, err)
		}
		for _, s := range rows {
			if err := onSummary(s); err != nil {
				return err
			}
		}
		return nil
	})
}
