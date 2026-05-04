package awx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Lookups holds the small reference tables fetched once at startup.
//
// 10k hosts × ~600 B each = ~6 MB resident. We trim heavy fields we don't
// need (notes, descriptions) by accepting them into HostLite and ignoring.
type Lookups struct {
	Hosts       map[int]HostLite
	Inventories map[int]InventoryLite
	Templates   map[int]TemplateLite

	// AnsibleHost extracted from each host's `variables` payload. We keep
	// this in a separate map so callers don't have to re-parse YAML.
	AnsibleHost map[int]string
}

// FetchLookups pulls the three reference tables. Each is paginated; combined
// requests scale with table size, not with job/summary volume.
func (c *Client) FetchLookups(ctx context.Context) (*Lookups, error) {
	out := &Lookups{
		Hosts:       make(map[int]HostLite),
		Inventories: make(map[int]InventoryLite),
		Templates:   make(map[int]TemplateLite),
		AnsibleHost: make(map[int]string),
	}

	if err := c.Paginate(ctx, "inventories", "inventories/", nil, func(_ context.Context, p Page) error {
		var rows []InventoryLite
		if err := json.Unmarshal(p.Results, &rows); err != nil {
			return fmt.Errorf("decode inventories: %w", err)
		}
		for _, r := range rows {
			out.Inventories[r.ID] = r
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := c.Paginate(ctx, "job_templates", "job_templates/", nil, func(_ context.Context, p Page) error {
		var rows []TemplateLite
		if err := json.Unmarshal(p.Results, &rows); err != nil {
			return fmt.Errorf("decode job_templates: %w", err)
		}
		for _, r := range rows {
			out.Templates[r.ID] = r
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := c.Paginate(ctx, "hosts", "hosts/", nil, func(_ context.Context, p Page) error {
		var rows []HostLite
		if err := json.Unmarshal(p.Results, &rows); err != nil {
			return fmt.Errorf("decode hosts: %w", err)
		}
		for _, r := range rows {
			out.Hosts[r.ID] = r
			if ah := extractAnsibleHost(r.Variables); ah != "" {
				out.AnsibleHost[r.ID] = ah
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return out, nil
}

// extractAnsibleHost parses the host `variables` blob (YAML or JSON) and
// returns the value of the `ansible_host` key. Empty string if absent or
// unparseable — we never fail the report just because one host has bad YAML.
func extractAnsibleHost(vars string) string {
	vars = strings.TrimSpace(vars)
	if vars == "" || vars == "---" {
		return ""
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(vars), &m); err != nil {
		return ""
	}
	v, ok := m["ansible_host"]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// HostInventoryName returns the inventory name for a given host_id, falling
// back to the inventories table if the host's summary_fields didn't carry it
// (rare, but cheap to handle).
func (l *Lookups) HostInventoryName(hostID int) (invID int, invName string) {
	h, ok := l.Hosts[hostID]
	if !ok {
		return 0, ""
	}
	if h.SummaryFields.Inventory != nil {
		return h.SummaryFields.Inventory.ID, h.SummaryFields.Inventory.Name
	}
	if inv, ok := l.Inventories[h.Inventory]; ok {
		return inv.ID, inv.Name
	}
	return h.Inventory, ""
}
