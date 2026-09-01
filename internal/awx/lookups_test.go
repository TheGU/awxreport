package awx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractAnsibleHost(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"just yaml header", "---\n", ""},
		{"yaml with key", "---\nansible_host: 10.0.0.1\n", "10.0.0.1"},
		{"yaml without key", "---\nfoo: bar\n", ""},
		{"json with key", `{"ansible_host": "10.0.0.2"}`, "10.0.0.2"},
		{"non-string value gets stringified", "---\nansible_host: 12345\n", "12345"},
		{"malformed yaml returns empty (does not panic)", "---\n  : : : invalid", ""},
		{"key with extra vars", "---\nansible_user: admin\nansible_host: 10.0.0.3\nansible_port: 22\n", "10.0.0.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAnsibleHost(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFetchActiveHostIDs_ReturnsSortedIDs(t *testing.T) {
	var gotGTE, gotIsNull string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/hosts/", func(w http.ResponseWriter, r *http.Request) {
		gotGTE = r.URL.Query().Get("or__last_job__finished__gte")
		gotIsNull = r.URL.Query().Get("or__last_job__finished__isnull")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 3, "next": nil, "previous": nil,
			"results": []map[string]int{{"id": 30}, {"id": 10}, {"id": 20}},
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
	ids, supported, err := c.FetchActiveHostIDs(context.Background(), since)
	if err != nil {
		t.Fatalf("FetchActiveHostIDs: %v", err)
	}
	if !supported {
		t.Error("supported = false, want true")
	}
	if len(ids) != 3 || ids[0] != 10 || ids[1] != 20 || ids[2] != 30 {
		t.Errorf("ids = %v, want sorted [10 20 30]", ids)
	}
	if gotGTE == "" {
		t.Error("or__last_job__finished__gte filter was not sent")
	}
	if gotIsNull != "True" {
		t.Errorf("or__last_job__finished__isnull = %q, want %q (so hosts with a still-running last job aren't pruned)", gotIsNull, "True")
	}
}

func TestFetchActiveHostIDs_400ReturnsUnsupported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/hosts/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"unknown filter"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ids, supported, err := c.FetchActiveHostIDs(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("FetchActiveHostIDs: %v", err)
	}
	if supported {
		t.Error("supported = true, want false on a 400")
	}
	if ids != nil {
		t.Errorf("ids = %v, want nil", ids)
	}
}
