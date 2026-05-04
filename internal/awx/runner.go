package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// JobVisitor is called once per job.
type JobVisitor func(j JobLite) error

// SummaryVisitor is called once per job_host_summary row. Called in
// streaming order — the caller is expected to aggregate and discard.
type SummaryVisitor func(s SummaryLite) error

// IterateJobsWithSummaries pages through completed jobs in [since, now] and
// for each job pages through its job_host_summaries.
//
// We do NOT hold all jobs or summaries in memory. Each call is one page; the
// visitor must aggregate and return quickly. Pacing is enforced inside Get,
// so this respects the rate budget without extra coordination.
//
// Heuristics:
//   - Order jobs ascending by id, so debug dumps are deterministic across
//     re-runs of the same window.
//   - We only request `finished__gte=...`. Running/pending jobs in the window
//     have no `finished` set yet and are excluded — desirable for a monthly
//     report that should reflect completed runs.
func (c *Client) IterateJobsWithSummaries(ctx context.Context, since time.Time,
	onJob JobVisitor, onSummary SummaryVisitor) error {

	jobsQ := url.Values{
		"finished__gte": []string{since.UTC().Format(time.RFC3339)},
		"order_by":      []string{"id"},
	}

	return c.Paginate(ctx, "jobs", "jobs/", jobsQ, func(ctx context.Context, p Page) error {
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
	})
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
