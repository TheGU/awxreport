package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// listEnvelope matches the standard AWX list response shape.
//
// `Results` stays as raw JSON so the caller can decode rows on its own schema.
// `Next` is either a relative path (e.g. /api/v2/jobs/?page=2) or null.
type listEnvelope struct {
	Count   int             `json:"count"`
	Next    *string         `json:"next"`
	Results json.RawMessage `json:"results"`
}

// Page is a decoded page handed to the visitor.
type Page struct {
	// Index is 1-based.
	Index int
	// Count is the total record count from the envelope (same on every page).
	Count int
	// Results is the raw JSON array of rows. Decode with json.Unmarshal.
	Results json.RawMessage
}

// PageVisitor receives each page in order. Return an error to abort iteration.
//
// The visitor MUST decode and aggregate before returning — Results is reused.
type PageVisitor func(ctx context.Context, p Page) error

// Paginate iterates an AWX list endpoint, calling visit for every page.
//
// `endpoint` is a short label (used for debug filenames + logs). `path` is the
// initial URL path (relative to API root, or absolute). `query` is applied to
// the first request only — subsequent pages follow the server's `next` URL,
// which already carries forward the filters.
func (c *Client) Paginate(ctx context.Context, endpoint, path string, query url.Values, visit PageVisitor) error {
	if query == nil {
		query = url.Values{}
	}
	if query.Get("page_size") == "" {
		query.Set("page_size", fmt.Sprintf("%d", c.PageSize))
	}

	u := c.URL(path, query)
	idx := 0
	for {
		idx++
		body, err := c.Get(ctx, endpoint, u)
		if err != nil {
			return fmt.Errorf("%s page %d: %w", endpoint, idx, err)
		}

		var env listEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return fmt.Errorf("%s page %d decode envelope: %w", endpoint, idx, err)
		}

		if err := visit(ctx, Page{Index: idx, Count: env.Count, Results: env.Results}); err != nil {
			return err
		}

		if env.Next == nil || *env.Next == "" {
			return nil
		}
		u = c.URL(*env.Next, nil)
	}
}
