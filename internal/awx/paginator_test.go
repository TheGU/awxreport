package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// fakeAWX serves a paginated endpoint with N items, returning page_size at a
// time and a `next` URL that points back to itself with page=N+1.
func fakeAWX(t *testing.T, total int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/things/", func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			fmt.Sscanf(p, "%d", &page)
		}
		size := 5
		if s := r.URL.Query().Get("page_size"); s != "" {
			fmt.Sscanf(s, "%d", &size)
		}
		start := (page - 1) * size
		end := start + size
		if end > total {
			end = total
		}
		results := make([]map[string]int, 0, end-start)
		for i := start; i < end; i++ {
			results = append(results, map[string]int{"id": i + 1})
		}
		var next *string
		if end < total {
			n := fmt.Sprintf("/api/v2/things/?page=%d&page_size=%d", page+1, size)
			next = &n
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": total, "next": next, "previous": nil, "results": results,
		})
	})
	return httptest.NewServer(mux)
}

func TestPaginate_FollowsNext(t *testing.T) {
	srv := fakeAWX(t, 12)
	defer srv.Close()

	c, err := New(srv.URL, "tok", Options{PageSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var rows []map[string]int
	pages := 0
	err = c.Paginate(context.Background(), "things", "things/", url.Values{},
		func(_ context.Context, p Page) error {
			pages++
			var batch []map[string]int
			if err := json.Unmarshal(p.Results, &batch); err != nil {
				return err
			}
			rows = append(rows, batch...)
			return nil
		})
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if pages != 3 { // 12 / 5 = 2 full + 1 partial
		t.Errorf("pages = %d, want 3", pages)
	}
	if len(rows) != 12 {
		t.Errorf("rows = %d, want 12", len(rows))
	}
	for i, r := range rows {
		if r["id"] != i+1 {
			t.Errorf("row %d id = %d, want %d", i, r["id"], i+1)
		}
	}
}

func TestPaginate_StopsOnVisitorError(t *testing.T) {
	srv := fakeAWX(t, 50)
	defer srv.Close()

	c, _ := New(srv.URL, "tok", Options{PageSize: 5})
	defer c.Close()

	pages := 0
	err := c.Paginate(context.Background(), "things", "things/", url.Values{},
		func(_ context.Context, p Page) error {
			pages++
			if pages == 2 {
				return fmt.Errorf("stop")
			}
			return nil
		})
	if err == nil || err.Error() != "stop" {
		t.Errorf("err = %v, want 'stop'", err)
	}
	if pages != 2 {
		t.Errorf("pages = %d, want 2", pages)
	}
}

func TestClient_SendsBearerToken(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"next":null,"previous":null,"results":[]}`)
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "the-token", Options{})
	defer c.Close()

	_, err := c.Get(context.Background(), "x", srv.URL+"/api/v2/anything/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer the-token" {
		t.Errorf("Authorization = %q, want 'Bearer the-token'", got)
	}
}
