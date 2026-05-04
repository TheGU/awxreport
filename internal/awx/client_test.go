package awx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClient_RetriesOn429(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, "slow down")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "tok", Options{MaxRetry: 5})
	defer c.Close()

	body, err := c.Get(context.Background(), "x", srv.URL+"/api/v2/x/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("body = %s", body)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("hits = %d, want 3 (2 retries + 1 success)", got)
	}
}

func TestClient_FailsFastOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "bad token")
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "tok", Options{MaxRetry: 5})
	defer c.Close()

	_, err := c.Get(context.Background(), "x", srv.URL+"/api/v2/x/")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want mention of 401", err)
	}
}

func TestClient_DebugDumpsPagesPerEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hello":"world"}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	c, err := New(srv.URL, "tok", Options{DebugDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "things", srv.URL+"/api/v2/things/"); err != nil {
			t.Fatal(err)
		}
	}
	// One file per call, plus a requests.log.
	entries, _ := os.ReadDir(filepath.Join(dir, "things"))
	if len(entries) != 3 {
		t.Errorf("expected 3 dump files, got %d", len(entries))
	}
	logBytes, err := os.ReadFile(filepath.Join(dir, "requests.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(logBytes), "endpoint=things"); got != 3 {
		t.Errorf("requests.log has %d 'things' lines, want 3", got)
	}
	if strings.Contains(string(logBytes), "tok") {
		t.Error("requests.log leaked the token — must redact Authorization")
	}
}

func TestClient_URL_AbsolutePassthrough(t *testing.T) {
	c, _ := New("https://awx.example.com", "tok", Options{})
	defer c.Close()
	if got := c.URL("https://other.example.com/x", nil); got != "https://other.example.com/x" {
		t.Errorf("absolute URL not preserved: %s", got)
	}
	if got := c.URL("/api/v2/jobs/?page=2", nil); got != "https://awx.example.com/api/v2/jobs/?page=2" {
		t.Errorf("absolute path: %s", got)
	}
	if got := c.URL("hosts/", nil); got != "https://awx.example.com/api/v2/hosts/" {
		t.Errorf("short path: %s", got)
	}
}
