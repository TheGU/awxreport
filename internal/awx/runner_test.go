package awx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	err = c.IterateJobsWithSummaries(context.Background(), since, until, ids,
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
	err = c.IterateJobsWithSummaries(context.Background(), since, until, nil,
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
	err = c.IterateJobsWithSummaries(context.Background(), since, until, ids,
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
	err = c.IterateJobsWithSummaries(context.Background(), since, until, ids,
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
