package awx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingJobsServer serves /api/v2/jobs/ with an empty page on every
// request and records the job_template__in and finished filter params seen
// on each request.
func recordingJobsServer(t *testing.T, seenJobTemplateIn *[]string, seenFinished *[]bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*seenJobTemplateIn = append(*seenJobTemplateIn, q.Get("job_template__in"))
		*seenFinished = append(*seenFinished, q.Get("finished__gte") != "" && q.Get("finished__lt") != "")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 0, "next": nil, "previous": nil, "results": []any{},
		})
	})
	return httptest.NewServer(mux)
}

func TestIterateJobsWithSummaries_ChunksTemplateIDs(t *testing.T) {
	var seenJobTemplateIn []string
	var seenFinished []bool
	srv := recordingJobsServer(t, &seenJobTemplateIn, &seenFinished)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ids := make([]int, 250)
	for i := range ids {
		ids[i] = 250 - i // unsorted, descending, to exercise the sort step
	}

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobsWithSummaries(context.Background(), since, until, ids, 1,
		func(JobLite) error { return nil },
		func(SummaryLite) error { return nil })
	if err != nil {
		t.Fatalf("IterateJobsWithSummaries: %v", err)
	}

	if len(seenJobTemplateIn) != 2 {
		t.Fatalf("request series count = %d, want 2 (got job_template__in values: %v)", len(seenJobTemplateIn), seenJobTemplateIn)
	}

	wantFirst := joinInts(rangeInts(1, 200))
	wantSecond := joinInts(rangeInts(201, 250))
	if seenJobTemplateIn[0] != wantFirst {
		t.Errorf("first chunk job_template__in = %q, want %q", seenJobTemplateIn[0], wantFirst)
	}
	if seenJobTemplateIn[1] != wantSecond {
		t.Errorf("second chunk job_template__in = %q, want %q", seenJobTemplateIn[1], wantSecond)
	}
	for i, ok := range seenFinished {
		if !ok {
			t.Errorf("request %d missing finished__gte/finished__lt filters", i)
		}
	}
}

func rangeInts(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func TestIterateJobsWithSummaries_NoTemplateFilterWhenEmpty(t *testing.T) {
	var seenJobTemplateIn []string
	var seenFinished []bool
	srv := recordingJobsServer(t, &seenJobTemplateIn, &seenFinished)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobsWithSummaries(context.Background(), since, until, nil, 1,
		func(JobLite) error { return nil },
		func(SummaryLite) error { return nil })
	if err != nil {
		t.Fatalf("IterateJobsWithSummaries: %v", err)
	}

	if len(seenJobTemplateIn) != 1 {
		t.Fatalf("request series count = %d, want 1", len(seenJobTemplateIn))
	}
	if seenJobTemplateIn[0] != "" {
		t.Errorf("job_template__in = %q, want empty (no filter)", seenJobTemplateIn[0])
	}
	for i, ok := range seenFinished {
		if !ok {
			t.Errorf("request %d missing finished__gte/finished__lt filters", i)
		}
	}
}

func TestIterateJobsWithSummaries_ExactlyChunkSizeIsOneChunk(t *testing.T) {
	var seenJobTemplateIn []string
	var seenFinished []bool
	srv := recordingJobsServer(t, &seenJobTemplateIn, &seenFinished)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ids := rangeInts(1, jobTemplateChunkSize) // exactly 200 ids
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobsWithSummaries(context.Background(), since, until, ids, 1,
		func(JobLite) error { return nil },
		func(SummaryLite) error { return nil })
	if err != nil {
		t.Fatalf("IterateJobsWithSummaries: %v", err)
	}

	if len(seenJobTemplateIn) != 1 {
		t.Fatalf("request series count = %d, want 1 (exactly one chunk at the boundary)", len(seenJobTemplateIn))
	}
	want := joinInts(rangeInts(1, jobTemplateChunkSize))
	if seenJobTemplateIn[0] != want {
		t.Errorf("job_template__in = %q, want %q", seenJobTemplateIn[0], want)
	}
}

func TestIterateJobsWithSummaries_DuplicateIDsAreDeduped(t *testing.T) {
	var seenJobTemplateIn []string
	var seenFinished []bool
	srv := recordingJobsServer(t, &seenJobTemplateIn, &seenFinished)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ids := []int{5, 3, 5, 3, 1, 1}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobsWithSummaries(context.Background(), since, until, ids, 1,
		func(JobLite) error { return nil },
		func(SummaryLite) error { return nil })
	if err != nil {
		t.Fatalf("IterateJobsWithSummaries: %v", err)
	}

	if len(seenJobTemplateIn) != 1 {
		t.Fatalf("request series count = %d, want 1", len(seenJobTemplateIn))
	}
	want := joinInts([]int{1, 3, 5})
	if seenJobTemplateIn[0] != want {
		t.Errorf("job_template__in = %q, want %q (deduped, sorted)", seenJobTemplateIn[0], want)
	}
}

func TestCountJobs_ReturnsEnvelopeCountWithPageSize1(t *testing.T) {
	var gotPageSize string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		gotPageSize = r.URL.Query().Get("page_size")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 4321, "next": nil, "previous": nil, "results": []any{},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	count, err := c.CountJobs(context.Background(), since, until)
	if err != nil {
		t.Fatalf("CountJobs: %v", err)
	}
	if count != 4321 {
		t.Errorf("count = %d, want 4321", count)
	}
	if gotPageSize != "1" {
		t.Errorf("page_size = %q, want %q", gotPageSize, "1")
	}
}

// TestIterateJobs_KeysetProgressesAndDoesNotStopOnShortPage exercises a
// server that caps every page at 2 rows regardless of the requested
// page_size, to prove the walk keeps going past a short (but non-empty)
// page and only stops on a genuinely empty one.
func TestIterateJobs_KeysetProgressesAndDoesNotStopOnShortPage(t *testing.T) {
	ids := []int{10, 20, 30, 40, 50}
	var seenIDGT []string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gt := q.Get("id__gt")
		seenIDGT = append(seenIDGT, gt)
		lastID := 0
		if gt != "" {
			lastID, _ = strconv.Atoi(gt)
		}
		var page []map[string]int
		for _, id := range ids {
			if id > lastID {
				page = append(page, map[string]int{"id": id})
				if len(page) == 2 { // server-side cap, independent of page_size
					break
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": len(ids), "next": nil, "previous": nil, "results": page,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{PageSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var gotIDs []int
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobs(context.Background(), since, until, nil, func(j JobLite) error {
		gotIDs = append(gotIDs, j.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("IterateJobs: %v", err)
	}

	if !reflect.DeepEqual(gotIDs, ids) {
		t.Errorf("gotIDs = %v, want %v", gotIDs, ids)
	}
	// pages of 2,2,1 then a terminating empty page: id__gt = "", "20", "40", "50".
	want := []string{"", "20", "40", "50"}
	if !reflect.DeepEqual(seenIDGT, want) {
		t.Errorf("seenIDGT = %v, want %v (walk must not stop on the short 1-row page)", seenIDGT, want)
	}
}

// TestIterateJobs_IgnoredIDGTReturnsMonotonicityError guards against an
// infinite loop behind a proxy that strips the id__gt filter: the server
// keeps returning the same row no matter what, so the walk must detect that
// id stopped advancing and abort instead of looping forever.
func TestIterateJobs_IgnoredIDGTReturnsMonotonicityError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 1, "next": nil, "previous": nil,
			"results": []map[string]int{{"id": 5}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobs(context.Background(), since, until, nil, func(JobLite) error { return nil })
	if err == nil {
		t.Fatal("expected a monotonicity error")
	}
	if !strings.Contains(err.Error(), "ignored the id__gt filter") {
		t.Errorf("err = %v, want mention of 'ignored the id__gt filter'", err)
	}
}

// TestIterateJobs_UnorderedPageReturnsError guards the case a server honors
// id__gt but ignores order_by=id: the rows[0]-vs-lastID guard alone would
// never fire against a descending page (id__gt would still exclude the
// smallest id each round), so the within-page ascending check must catch it
// directly.
func TestIterateJobs_UnorderedPageReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 3, "next": nil, "previous": nil,
			"results": []map[string]int{{"id": 50}, {"id": 40}, {"id": 30}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobs(context.Background(), since, until, nil, func(JobLite) error { return nil })
	if err == nil {
		t.Fatal("expected an unordered-page error")
	}
	if !strings.Contains(err.Error(), "unordered page") {
		t.Errorf("err = %v, want mention of 'unordered page'", err)
	}
}

// TestIterateJobsWithSummaries_ConcurrencyBoundedAndComplete drives a fixed
// batch of jobs through a summary server that tracks in-flight requests, to
// prove the errgroup SetLimit(workers) bound is respected and every job's
// summary is still delivered exactly once despite the fan-out.
func TestIterateJobsWithSummaries_ConcurrencyBoundedAndComplete(t *testing.T) {
	const totalJobs = 6
	const workers = 3

	var inFlight, maxInFlight atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/job_host_summaries/") {
			cur := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				m := maxInFlight.Load()
				if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond) // widen the overlap window

			parts := strings.Split(strings.Trim(path, "/"), "/")
			jobID, _ := strconv.Atoi(parts[3]) // api/v2/jobs/<id>/job_host_summaries
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 1, "next": nil, "previous": nil,
				"results": []map[string]int{{"id": jobID*10 + 1, "job": jobID}},
			})
			return
		}

		q := r.URL.Query()
		lastID := 0
		if gt := q.Get("id__gt"); gt != "" {
			lastID, _ = strconv.Atoi(gt)
		}
		var page []map[string]int
		if lastID == 0 {
			for i := 1; i <= totalJobs; i++ {
				page = append(page, map[string]int{"id": i})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": totalJobs, "next": nil, "previous": nil, "results": page,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var mu sync.Mutex
	delivered := map[int]int{}

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	err = c.IterateJobsWithSummaries(context.Background(), since, until, nil, workers,
		func(JobLite) error { return nil },
		func(s SummaryLite) error {
			mu.Lock()
			delivered[s.ID]++
			mu.Unlock()
			return nil
		})
	if err != nil {
		t.Fatalf("IterateJobsWithSummaries: %v", err)
	}

	if got := maxInFlight.Load(); got > workers {
		t.Errorf("max in-flight summary requests = %d, want <= %d", got, workers)
	}
	if len(delivered) != totalJobs {
		t.Errorf("delivered %d distinct summaries, want %d", len(delivered), totalJobs)
	}
	for id, n := range delivered {
		if n != 1 {
			t.Errorf("summary %d delivered %d times, want exactly 1", id, n)
		}
	}
}

// TestIterateHostSummaries_VisitsAllHostsWithFilters checks every host id is
// walked, the job__finished window is applied to each per-host request, and
// onHostDone fires once per host with an accurate running total.
func TestIterateHostSummaries_VisitsAllHostsWithFilters(t *testing.T) {
	hostIDs := []int{3, 1, 2}

	var qMu sync.Mutex
	seenQueries := map[int]url.Values{}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/hosts/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		hostID, _ := strconv.Atoi(parts[3]) // api/v2/hosts/<id>/job_host_summaries
		qMu.Lock()
		seenQueries[hostID] = r.URL.Query()
		qMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 1, "next": nil, "previous": nil,
			"results": []map[string]int{{"id": hostID*100 + 1, "host": hostID}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	var summaries []int
	type doneCall struct{ hostID, done, total int }
	var doneCalls []doneCall

	err = c.IterateHostSummaries(context.Background(), hostIDs, since, until, 2,
		func(s SummaryLite) error {
			summaries = append(summaries, s.ID)
			return nil
		},
		func(hostID, done, total int) error {
			doneCalls = append(doneCalls, doneCall{hostID, done, total})
			return nil
		})
	if err != nil {
		t.Fatalf("IterateHostSummaries: %v", err)
	}

	if len(summaries) != len(hostIDs) {
		t.Errorf("summaries delivered = %d, want %d", len(summaries), len(hostIDs))
	}
	if len(doneCalls) != len(hostIDs) {
		t.Fatalf("onHostDone called %d times, want %d", len(doneCalls), len(hostIDs))
	}
	seenHosts := map[int]bool{}
	for _, dc := range doneCalls {
		if dc.total != len(hostIDs) {
			t.Errorf("onHostDone total = %d, want %d", dc.total, len(hostIDs))
		}
		seenHosts[dc.hostID] = true
	}
	for _, h := range hostIDs {
		if !seenHosts[h] {
			t.Errorf("onHostDone never called for host %d", h)
		}
		q, ok := seenQueries[h]
		if !ok {
			t.Fatalf("host %d never queried", h)
		}
		if q.Get("job__finished__gte") == "" || q.Get("job__finished__lt") == "" {
			t.Errorf("host %d query missing job__finished filters: %v", h, q)
		}
	}
}
