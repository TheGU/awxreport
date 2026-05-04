package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// ProbeResult is a tiny summary printed after probe finishes. Full JSON of
// every page lands in DebugDir if configured.
type ProbeResult struct {
	PingOK             bool
	MeUsername         string
	JobTemplates       int
	Inventories        int
	Hosts              int
	JobsInWindow       int
	SampleJobID        int
	SampleJobName      string
	SampleJobStatus    string
	SampleSummariesGot int
	WindowFrom         string
}

// Probe walks the same endpoints the report will use, but pulls only one
// small page from each, so we can verify field names, filters, and pagination
// behavior on the target AWX before running the full job.
//
// Strategy mirrors Phase 2:
//   - lookup tables (templates, inventories, hosts): paginated list endpoints
//   - jobs in window: /api/v2/jobs/?finished__gte=...
//   - per-host outcomes: /api/v2/jobs/{id}/job_host_summaries/
//
// AWX 24.6.1 does NOT expose /api/v2/job_host_summaries/ as a top-level list
// endpoint — only nested under jobs/{id} and hosts/{id}. Per-job iteration is
// strictly more efficient than per-host because hosts with no recent activity
// get skipped for free.
func (c *Client) Probe(ctx context.Context, daysBack int) (*ProbeResult, error) {
	res := &ProbeResult{}

	// 1. /ping/ — confirms the server responds.
	{
		body, err := c.Get(ctx, "ping", c.URL("ping/", nil))
		if err != nil {
			return res, fmt.Errorf("ping: %w", err)
		}
		var ping map[string]any
		if err := json.Unmarshal(body, &ping); err == nil {
			res.PingOK = true
		}
	}

	// 2. /me/ — confirm token is valid and capture username.
	{
		body, err := c.Get(ctx, "me", c.URL("me/", nil))
		if err != nil {
			return res, fmt.Errorf("me: %w", err)
		}
		var me struct {
			Results []struct {
				Username string `json:"username"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &me); err == nil && len(me.Results) > 0 {
			res.MeUsername = me.Results[0].Username
		}
	}

	// 3. Lookup tables: templates, inventories, hosts. We only pull one page
	//    here but record the total count from the envelope.
	sampleCount := func(endpoint, path string) (int, error) {
		var count int
		err := c.Paginate(ctx, endpoint, path, url.Values{"page_size": []string{"5"}},
			func(ctx context.Context, p Page) error {
				count = p.Count
				return errStop
			})
		if err == errStop {
			err = nil
		}
		return count, err
	}

	var err error
	if res.JobTemplates, err = sampleCount("job_templates", "job_templates/"); err != nil {
		return res, fmt.Errorf("job_templates: %w", err)
	}
	if res.Inventories, err = sampleCount("inventories", "inventories/"); err != nil {
		return res, fmt.Errorf("inventories: %w", err)
	}
	if res.Hosts, err = sampleCount("hosts", "hosts/"); err != nil {
		return res, fmt.Errorf("hosts: %w", err)
	}

	// 4. Jobs in the report window. We use `finished__gte` (not `created__gte`)
	//    because we only count completed runs.
	since := time.Now().UTC().AddDate(0, 0, -daysBack)
	res.WindowFrom = since.Format(time.RFC3339)

	// Prefer a successful job for the summaries sample (more representative).
	// Fall back to failed, then anything completed.
	type jobLite struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}

	pickSample := func(status string) (*jobLite, error) {
		q := url.Values{
			"page_size":     []string{"1"},
			"finished__gte": []string{res.WindowFrom},
			"order_by":      []string{"-id"},
			"status":        []string{status},
		}
		var picked *jobLite
		err := c.Paginate(ctx, "jobs", "jobs/", q, func(ctx context.Context, p Page) error {
			if res.JobsInWindow == 0 {
				// Capture the total count from the first list call (without status
				// filter) below; this branch never runs because we only call this
				// helper after the count is known.
			}
			var rows []jobLite
			if err := json.Unmarshal(p.Results, &rows); err != nil {
				return fmt.Errorf("decode jobs: %w", err)
			}
			if len(rows) > 0 {
				picked = &rows[0]
			}
			return errStop
		})
		if err != nil && err != errStop {
			return nil, err
		}
		return picked, nil
	}

	// First pass: count jobs in the window with no status filter.
	err = c.Paginate(ctx, "jobs", "jobs/", url.Values{
		"page_size":     []string{"1"},
		"finished__gte": []string{res.WindowFrom},
		"order_by":      []string{"-id"},
	}, func(ctx context.Context, p Page) error {
		res.JobsInWindow = p.Count
		return errStop
	})
	if err != nil && err != errStop {
		return res, fmt.Errorf("jobs: %w", err)
	}

	// Second pass: find a useful sample.
	var sample *jobLite
	for _, status := range []string{"successful", "failed", "error"} {
		j, perr := pickSample(status)
		if perr != nil {
			return res, fmt.Errorf("jobs (status=%s): %w", status, perr)
		}
		if j != nil {
			sample = j
			break
		}
	}

	// Per-job summaries — the core data source.
	if sample != nil {
		res.SampleJobID = sample.ID
		res.SampleJobName = sample.Name
		res.SampleJobStatus = sample.Status

		path := fmt.Sprintf("jobs/%d/job_host_summaries/", sample.ID)
		err := c.Paginate(ctx, "job_host_summaries", path, url.Values{"page_size": []string{"50"}},
			func(ctx context.Context, p Page) error {
				res.SampleSummariesGot = p.Count
				return errStop
			})
		if err != nil && err != errStop {
			return res, fmt.Errorf("summaries for job %d: %w", sample.ID, err)
		}
	}

	return res, nil
}

// errStop is a sentinel used by probe to abort Paginate after one page.
var errStop = stopErr{}

type stopErr struct{}

func (stopErr) Error() string { return "stop" }
