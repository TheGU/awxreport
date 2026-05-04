package main

import (
	"context"
	"fmt"
	"time"

	"github.com/TheGU/awxreport/internal/awx"
)

func runProbe(ctx context.Context, u *ui, opts globalOpts) (retErr error) {
	cfg, client, err := loadAndConnect(opts)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close client: %w", err)
		}
	}()

	u.banner(fmt.Sprintf("awxreport probe — %s%s", cfg.BaseURL, cfg.APIRoot))
	u.table([][2]string{
		{"window", fmt.Sprintf("%d days", cfg.DaysBack)},
		{"pacing", fmt.Sprintf("%dms", cfg.RequestPacingMS)},
		{"page size", fmt.Sprintf("%d", cfg.PageSize)},
		{"debug dir", emptyDash(cfg.DebugDir)},
	})

	u.section("Probing endpoints…")
	res, err := client.Probe(ctx, cfg.DaysBack)
	if err != nil {
		u.failf("probe failed: %v", err)
		return err
	}

	u.ok("ping")
	if res.MeUsername != "" {
		u.ok("auth as %s", res.MeUsername)
	} else {
		u.warn("auth ok but /me/ returned no user")
	}
	u.ok("job_templates: %d", res.JobTemplates)
	u.ok("inventories: %d", res.Inventories)
	u.ok("hosts: %d", res.Hosts)
	u.ok("jobs in last %dd: %d  (since %s)", cfg.DaysBack, res.JobsInWindow, res.WindowFrom)
	if res.SampleJobID != 0 {
		u.ok("sample job: id=%d %q status=%s — %d host summaries",
			res.SampleJobID, res.SampleJobName, res.SampleJobStatus, res.SampleSummariesGot)
	} else {
		u.warn("no completed sample job in window — summaries shape was not exercised")
	}

	if cfg.DebugDir != "" {
		u.section("Debug dumps")
		u.infof("  inspect %s/<endpoint>/page-00001.json to verify field names\n", cfg.DebugDir)
	}
	return nil
}

func loadAndConnect(opts globalOpts) (*loadedConfig, *awx.Client, error) {
	cfg, err := loadConfig(opts)
	if err != nil {
		return nil, nil, err
	}
	client, err := awx.New(cfg.BaseURL, cfg.Token, awx.Options{
		APIRoot:            cfg.APIRoot,
		PageSize:           cfg.PageSize,
		Pacing:             time.Duration(cfg.RequestPacingMS) * time.Millisecond,
		MaxRetry:           cfg.MaxRetries,
		HTTPTimeout:        time.Duration(cfg.HTTPTimeoutSec) * time.Second,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		DebugDir:           cfg.DebugDir,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("client: %w", err)
	}
	return cfg, client, nil
}

func emptyDash(s string) string {
	if s == "" {
		return "(disabled)"
	}
	return s
}
