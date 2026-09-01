package awx

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a thin AWX/AAP REST client with retry, pacing, and optional debug dump.
//
// Pacing is enforced across the whole client (not per-endpoint), so concurrent
// callers all share the same rate budget — desirable since AWX rate-limiting
// is global to the user/token.
type Client struct {
	BaseURL string // e.g. https://awx.example.local
	APIRoot string // e.g. /api/v2
	Token   string

	HTTP     *http.Client
	PageSize int
	Pacing   time.Duration
	MaxRetry int

	// Debug dumping.
	DebugDir string
	dumpSeq  map[string]*atomic.Int64 // per-endpoint counters, lazy-init
	dumpMu   sync.Mutex

	// Request log.
	logFile *os.File
	logMu   sync.Mutex

	// Pacing gate.
	paceMu   sync.Mutex
	lastCall time.Time
}

type Options struct {
	APIRoot            string
	PageSize           int
	Pacing             time.Duration
	MaxRetry           int
	HTTPTimeout        time.Duration
	InsecureSkipVerify bool
	DebugDir           string
}

func New(baseURL, token string, opts Options) (*Client, error) {
	if opts.APIRoot == "" {
		opts.APIRoot = "/api/v2"
	}
	if opts.PageSize == 0 {
		opts.PageSize = 200
	}
	if opts.HTTPTimeout == 0 {
		opts.HTTPTimeout = 60 * time.Second
	}
	if opts.MaxRetry == 0 {
		opts.MaxRetry = 5
	}

	tr := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: opts.InsecureSkipVerify},
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   8, // Go's default of 2 would force a TLS handshake per request under concurrency
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: opts.HTTPTimeout,
	}
	c := &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		APIRoot:  strings.TrimRight(opts.APIRoot, "/"),
		Token:    token,
		HTTP:     &http.Client{Transport: tr, Timeout: opts.HTTPTimeout},
		PageSize: opts.PageSize,
		Pacing:   opts.Pacing,
		MaxRetry: opts.MaxRetry,
		DebugDir: opts.DebugDir,
		dumpSeq:  make(map[string]*atomic.Int64),
	}

	if opts.DebugDir != "" {
		if err := os.MkdirAll(opts.DebugDir, 0o755); err != nil {
			return nil, fmt.Errorf("create debug dir: %w", err)
		}
		f, err := os.OpenFile(filepath.Join(opts.DebugDir, "requests.log"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open requests.log: %w", err)
		}
		c.logFile = f
	}
	return c, nil
}

func (c *Client) Close() error {
	if c.logFile != nil {
		return c.logFile.Close()
	}
	return nil
}

// URL builds an absolute URL from a path. Path may be:
//   - already absolute (http...) — returned as-is (the AWX `next` field)
//   - relative starting with the API root (e.g. /api/v2/jobs/) — joined with BaseURL
//   - a short form (e.g. job_templates/) — joined with BaseURL + APIRoot
func (c *Client) URL(path string, query url.Values) string {
	var u string
	switch {
	case strings.HasPrefix(path, "http://"), strings.HasPrefix(path, "https://"):
		u = path
	case strings.HasPrefix(path, "/"):
		u = c.BaseURL + path
	default:
		u = c.BaseURL + c.APIRoot + "/" + strings.TrimLeft(path, "/")
	}
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u = u + sep + query.Encode()
	}
	return u
}

// Get executes a GET with retry/backoff and returns the raw response body.
//
// `endpoint` is a short label (e.g. "job_host_summaries") used for debug
// filenames and the request log. It does NOT have to match the URL path.
func (c *Client) Get(ctx context.Context, endpoint, urlStr string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetry; attempt++ {
		// Under fan-out, a queued request would otherwise pay a full pacing
		// interval per attempt even after the caller's context is cancelled.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			// Jitter within [backoff/2, backoff] so concurrent workers that
			// all hit a shared 429 do not retry in lockstep.
			half := backoff / 2
			jittered := half + time.Duration(rand.Int64N(int64(half)+1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(jittered):
			}
		}
		// Paced on every attempt (not just the first) so retries from one
		// caller and requests from concurrent callers all share the same
		// rate budget.
		c.pace()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Accept", "application/json")

		start := time.Now()
		resp, err := c.HTTP.Do(req)
		latency := time.Since(start)

		if err != nil {
			c.logRequest(endpoint, urlStr, 0, latency, err)
			lastErr = err
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr == nil && closeErr != nil {
			readErr = closeErr
		}
		c.logRequest(endpoint, urlStr, resp.StatusCode, latency, readErr)
		if readErr != nil {
			lastErr = readErr
			continue
		}

		switch {
		case resp.StatusCode == 200:
			c.dumpDebug(endpoint, body)
			return body, nil
		case resp.StatusCode == 429:
			// Honor Retry-After if present, else fall through to backoff.
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, perr := strconv.Atoi(ra); perr == nil {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(time.Duration(secs) * time.Second):
					}
				}
			}
			lastErr = fmt.Errorf("429 rate limited: %s", truncate(body, 200))
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("%d server error: %s", resp.StatusCode, truncate(body, 200))
		case resp.StatusCode == 401, resp.StatusCode == 403:
			return nil, fmt.Errorf("auth error %d: check AWX_TOKEN — %s", resp.StatusCode, truncate(body, 200))
		default:
			return nil, &StatusError{Code: resp.StatusCode, Status: resp.Status, Body: truncate(body, 200)}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("exhausted retries")
	}
	return nil, lastErr
}

// StatusError carries an HTTP status code from a non-2xx response that Get
// did not already classify with its own error type (429, 5xx, 401/403 all
// get their own handling above; this is everything else, notably 400).
// Callers that need to distinguish a specific status (e.g. FetchActiveHostIDs
// treating 400 as "filter unsupported") can errors.As into this type.
type StatusError struct {
	Code   int
	Status string
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Code, e.Status, e.Body)
}

// GetJSON executes Get and decodes JSON into out.
func (c *Client) GetJSON(ctx context.Context, endpoint, urlStr string, out any) error {
	body, err := c.Get(ctx, endpoint, urlStr)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

// pace enforces the global minimum delay between requests. It does not take
// a context, so under concurrency a ctx cancellation can be delayed by up to
// workers*Pacing before the last goroutine waiting here notices.
func (c *Client) pace() {
	if c.Pacing <= 0 {
		return
	}
	c.paceMu.Lock()
	defer c.paceMu.Unlock()
	if !c.lastCall.IsZero() {
		wait := c.Pacing - time.Since(c.lastCall)
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	c.lastCall = time.Now()
}

func (c *Client) logRequest(endpoint, urlStr string, status int, latency time.Duration, err error) {
	if c.logFile == nil {
		return
	}
	c.logMu.Lock()
	defer c.logMu.Unlock()
	errStr := ""
	if err != nil {
		errStr = " err=" + err.Error()
	}
	_, _ = fmt.Fprintf(c.logFile, "%s endpoint=%s status=%d latency_ms=%d url=%s%s\n",
		time.Now().UTC().Format(time.RFC3339Nano), endpoint, status, latency.Milliseconds(), urlStr, errStr)
}

func (c *Client) dumpDebug(endpoint string, body []byte) {
	if c.DebugDir == "" {
		return
	}
	c.dumpMu.Lock()
	seq, ok := c.dumpSeq[endpoint]
	if !ok {
		seq = &atomic.Int64{}
		c.dumpSeq[endpoint] = seq
	}
	c.dumpMu.Unlock()
	n := seq.Add(1)

	dir := filepath.Join(c.DebugDir, sanitize(endpoint))
	_ = os.MkdirAll(dir, 0o755)
	fname := filepath.Join(dir, fmt.Sprintf("page-%05d.json", n))
	_ = os.WriteFile(fname, body, 0o644)
}

func sanitize(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "?", "_", "&", "_", "=", "_", " ", "_")
	return r.Replace(s)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...[truncated]"
}
