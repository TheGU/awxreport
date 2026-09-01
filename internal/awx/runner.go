package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// JobVisitor is called once per job. It is always called from a single
// goroutine, in ascending job id order, and is never called concurrently
// with a SummaryVisitor invocation.
type JobVisitor func(j JobLite) error

// SummaryVisitor is called once per job_host_summary row. Called in
// streaming order per source (job or host); the caller is expected to
// aggregate and discard. When fetched concurrently (IterateJobsWithSummaries
// with workers > 1, or IterateHostSummaries), calls are serialized by the
// runner through a shared mutex, so a SummaryVisitor never needs its own
// locking.
type SummaryVisitor func(s SummaryLite) error

// HostVisitor is called after each host's summaries are fetched, serialized
// alongside SummaryVisitor calls.
type HostVisitor func(hostID int, done, total int) error

// jobTemplateChunkSize is the max number of ids joined into one
// job_template__in filter, chosen to keep the generated query string well
// under typical server URL length limits.
const jobTemplateChunkSize = 200

// cloneValues returns a shallow copy of v: a new map, but each value slice
// still shares its backing array with v's. Callers may add or replace keys
// via Set (which assigns a new slice) without mutating v, but must never
// mutate an existing value slice's contents in place -- that would corrupt v
// too. Paginate itself writes into the map it's given (page_size), which
// would race if the same map were handed to concurrent requests; cloning
// avoids that.
func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = vals
	}
	return out
}

// forEachTemplateChunk splits templateIDs into sorted, deduped chunks of at
// most jobTemplateChunkSize ids and calls walk once per chunk with baseQ
// plus that chunk's job_template__in filter. When templateIDs is empty, walk
// is called once with the unfiltered baseQ.
func forEachTemplateChunk(ctx context.Context, baseQ url.Values, templateIDs []int,
	walk func(ctx context.Context, chunkQ url.Values) error) error {

	if len(templateIDs) == 0 {
		return walk(ctx, baseQ)
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
		chunkQ := cloneValues(baseQ)
		chunkQ.Set("job_template__in", joinInts(ids[start:end]))
		if err := walk(ctx, chunkQ); err != nil {
			return err
		}
	}
	return nil
}

// iterateJobsKeyset walks jobs/ for one query series using keyset pagination
// (id__gt) instead of next-link following. baseQuery already carries the
// finished window and, if applicable, job_template__in; iterateJobsKeyset
// adds order_by=id and id__gt on every request. onPage receives each page's
// rows in ascending id order and must not retain the slice past the call.
//
// order_by=id is load-bearing: keyset correctness depends on the server
// returning rows in strictly ascending id order, so id__gt always advances
// and never revisits or skips a row.
//
// Termination is ONLY on a page with zero rows. A short page (fewer rows
// than requested) does not end the walk -- the server may cap page_size
// below what was requested -- and the envelope Count is never consulted in
// keyset mode, since it shrinks as id__gt advances and stops reflecting the
// remaining work.
func (c *Client) iterateJobsKeyset(ctx context.Context, baseQuery url.Values,
	onPage func(ctx context.Context, rows []JobLite) error) error {

	lastID := 0
	for {
		q := cloneValues(baseQuery)
		q.Set("order_by", "id")
		if lastID > 0 {
			q.Set("id__gt", strconv.Itoa(lastID))
		}

		var rows []JobLite
		err := c.Paginate(ctx, "jobs", "jobs/", q, func(_ context.Context, p Page) error {
			if err := json.Unmarshal(p.Results, &rows); err != nil {
				return fmt.Errorf("decode jobs page: %w", err)
			}
			return errStop
		})
		if err != nil && err != errStop {
			return err
		}

		if len(rows) == 0 {
			return nil
		}
		if lastID > 0 && rows[0].ID <= lastID {
			return fmt.Errorf("jobs endpoint ignored the id__gt filter (page repeated at id %d); aborting", rows[0].ID)
		}
		// A server that honors id__gt but ignores order_by=id can still
		// advance past the guard above one id at a time, re-serving the rest
		// of a descending page as "new" on every request. Require strict
		// ascending order within the page too, and compute the next id__gt
		// from the page's actual max rather than assuming rows[len-1] is it.
		maxID := rows[0].ID
		for i := 1; i < len(rows); i++ {
			if rows[i].ID <= rows[i-1].ID {
				return fmt.Errorf("jobs endpoint returned an unordered page; order_by=id not honored; aborting")
			}
			if rows[i].ID > maxID {
				maxID = rows[i].ID
			}
		}

		if err := onPage(ctx, rows); err != nil {
			return err
		}
		lastID = maxID
	}
}

// IterateJobs pages through completed jobs in [since, until) with keyset
// pagination, calling onJob for every row in ascending id order. It does not
// fetch summaries -- see IterateJobsWithSummaries for the combined walk, and
// IterateHostSummaries for the per-host summary walk used by the per_host
// strategy.
//
// When templateIDs is non-empty, jobs are additionally filtered server-side
// with job_template__in, split into chunks of jobTemplateChunkSize ids (one
// keyset series per chunk).
func (c *Client) IterateJobs(ctx context.Context, since, until time.Time, templateIDs []int, onJob JobVisitor) error {
	baseQ := url.Values{
		"finished__gte": []string{since.UTC().Format(time.RFC3339)},
		"finished__lt":  []string{until.UTC().Format(time.RFC3339)},
	}

	return forEachTemplateChunk(ctx, baseQ, templateIDs, func(ctx context.Context, chunkQ url.Values) error {
		return c.iterateJobsKeyset(ctx, chunkQ, func(_ context.Context, rows []JobLite) error {
			for _, j := range rows {
				if err := onJob(j); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// IterateJobsWithSummaries pages through completed jobs in [since, until)
// with keyset pagination and, for each page, fetches every job's
// job_host_summaries. onJob is called for every row on the page first,
// serially, in ascending id order; the page's summaries are then fetched
// concurrently, up to workers at a time (workers < 1 is treated as 1), with
// every onSummary invocation serialized through one mutex shared across the
// whole call so a caller-supplied visitor never needs its own locking.
// onJob is never called concurrently with onSummary.
//
// When templateIDs is non-empty, jobs are additionally filtered server-side
// with job_template__in, split into chunks of jobTemplateChunkSize ids (one
// keyset series per chunk). Jobs whose job_template FK is null are excluded
// by the filter server-side.
//
// We do NOT hold all jobs or summaries in memory. Each call is one page; the
// visitors must aggregate and return quickly. Pacing is enforced inside Get,
// so this respects the rate budget without extra coordination.
func (c *Client) IterateJobsWithSummaries(ctx context.Context, since, until time.Time, templateIDs []int, workers int,
	onJob JobVisitor, onSummary SummaryVisitor) error {

	if workers < 1 {
		workers = 1
	}

	baseQ := url.Values{
		"finished__gte": []string{since.UTC().Format(time.RFC3339)},
		"finished__lt":  []string{until.UTC().Format(time.RFC3339)},
	}

	var mu sync.Mutex
	return forEachTemplateChunk(ctx, baseQ, templateIDs, func(ctx context.Context, chunkQ url.Values) error {
		return c.iterateJobsKeyset(ctx, chunkQ, func(ctx context.Context, rows []JobLite) error {
			for _, j := range rows {
				if err := onJob(j); err != nil {
					return err
				}
			}

			g, gctx := errgroup.WithContext(ctx)
			g.SetLimit(workers)
			for _, j := range rows {
				jobID := j.ID
				g.Go(func() error {
					if err := c.iterJobSummaries(gctx, jobID, onSummary, &mu); err != nil {
						return fmt.Errorf("job %d summaries: %w", jobID, err)
					}
					return nil
				})
			}
			return g.Wait()
		})
	})
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

// iterJobSummaries pages through one job's job_host_summaries, calling
// onSummary for every row. mu serializes onSummary calls so this can safely
// run concurrently for different jobIDs against the same visitor.
func (c *Client) iterJobSummaries(ctx context.Context, jobID int, onSummary SummaryVisitor, mu *sync.Mutex) error {
	path := fmt.Sprintf("jobs/%d/job_host_summaries/", jobID)
	return c.Paginate(ctx, "job_host_summaries", path, nil, func(_ context.Context, p Page) error {
		var rows []SummaryLite
		if err := json.Unmarshal(p.Results, &rows); err != nil {
			return fmt.Errorf("decode summaries page %d: %w", p.Index, err)
		}
		for _, s := range rows {
			mu.Lock()
			err := onSummary(s)
			mu.Unlock()
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// IterateHostSummaries walks hosts/<id>/job_host_summaries/ for each id in
// hostIDs, fetching up to workers hosts at a time (workers < 1 is treated as
// 1). Every summary row is filtered server-side to [since, until) via
// job__finished__gte/job__finished__lt, ordered by id. onSummary and
// onHostDone are both serialized through one shared mutex, so neither needs
// its own locking; onHostDone fires once per host, after that host's
// summaries are exhausted, with a running count against len(hostIDs).
func (c *Client) IterateHostSummaries(ctx context.Context, hostIDs []int, since, until time.Time, workers int,
	onSummary SummaryVisitor, onHostDone HostVisitor) error {

	if workers < 1 {
		workers = 1
	}

	ids := make([]int, len(hostIDs))
	copy(ids, hostIDs)
	sort.Ints(ids)

	baseQ := url.Values{
		"job__finished__gte": []string{since.UTC().Format(time.RFC3339)},
		"job__finished__lt":  []string{until.UTC().Format(time.RFC3339)},
		"order_by":           []string{"id"},
	}

	total := len(ids)
	done := 0
	var mu sync.Mutex

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	for _, hostID := range ids {
		g.Go(func() error {
			path := fmt.Sprintf("hosts/%d/job_host_summaries/", hostID)
			err := c.Paginate(gctx, "job_host_summaries", path, cloneValues(baseQ), func(_ context.Context, p Page) error {
				var rows []SummaryLite
				if err := json.Unmarshal(p.Results, &rows); err != nil {
					return fmt.Errorf("decode summaries page %d: %w", p.Index, err)
				}
				for _, s := range rows {
					mu.Lock()
					err := onSummary(s)
					mu.Unlock()
					if err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("host %d summaries: %w", hostID, err)
			}

			mu.Lock()
			done++
			err = onHostDone(hostID, done, total)
			mu.Unlock()
			return err
		})
	}
	return g.Wait()
}
