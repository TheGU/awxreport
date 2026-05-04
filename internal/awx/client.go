package awx

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	c.pace()

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetry; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

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
			return nil, fmt.Errorf("%d %s: %s", resp.StatusCode, resp.Status, truncate(body, 200))
		}
	}
	if lastErr == nil {
		lastErr = errors.New("exhausted retries")
	}
	return nil, lastErr
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
