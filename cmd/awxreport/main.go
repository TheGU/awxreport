package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TheGU/awxreport/internal/aggregate"
	"github.com/TheGU/awxreport/internal/awx"
	"github.com/TheGU/awxreport/internal/config"
	"github.com/TheGU/awxreport/internal/report"
)

const usage = `awxreport — AWX/AAP rollout report generator

Usage:
  awxreport <subcommand> [flags]

Subcommands:
  probe    Validate connectivity, auth, and API shape (cheap; one page each)
  report   Generate the monthly XLSX + CSV report

Common flags:
  -config PATH   Path to config YAML (default: ./config.yaml)
  -debug PATH    Override debug_dir from config (dumps every JSON page here)

Environment:
  AWX_TOKEN      OAuth2 personal token (required). Never read from the config file.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	sub := os.Args[1]
	args := os.Args[2:]

	switch sub {
	case "probe":
		os.Exit(runProbe(args))
	case "report":
		os.Exit(runReport(args))
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n%s", sub, usage)
		os.Exit(2)
	}
}

func runProbe(args []string) int {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config YAML")
	debugOverride := fs.String("debug", "", "override debug_dir (dump JSON pages here)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	if *debugOverride != "" {
		cfg.DebugDir = *debugOverride
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
		fmt.Fprintf(os.Stderr, "client: %v\n", err)
		return 1
	}
	defer client.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("probe: %s%s  (window=%dd, pacing=%dms)\n",
		cfg.BaseURL, cfg.APIRoot, cfg.DaysBack, cfg.RequestPacingMS)
	if cfg.DebugDir != "" {
		fmt.Printf("debug: dumping JSON to %s\n", cfg.DebugDir)
	}

	res, err := client.Probe(ctx, cfg.DaysBack)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe failed: %v\n", err)
		return 1
	}

	fmt.Println()
	fmt.Println("--- probe result ---")
	fmt.Printf("ping ok                  : %v\n", res.PingOK)
	fmt.Printf("authenticated as         : %s\n", res.MeUsername)
	fmt.Printf("job_templates total      : %d\n", res.JobTemplates)
	fmt.Printf("inventories total        : %d\n", res.Inventories)
	fmt.Printf("hosts total              : %d\n", res.Hosts)
	fmt.Printf("jobs in last %dd         : %d  (since %s)\n", cfg.DaysBack, res.JobsInWindow, res.WindowFrom)
	if res.SampleJobID != 0 {
		fmt.Printf("sample job               : id=%d name=%q status=%s\n",
			res.SampleJobID, res.SampleJobName, res.SampleJobStatus)
		fmt.Printf("  host summaries on job  : %d\n", res.SampleSummariesGot)
	} else {
		fmt.Println("sample job               : (none — window is empty or all jobs are still running)")
	}
	fmt.Println()
	if cfg.DebugDir != "" {
		fmt.Printf("Inspect %s/<endpoint>/page-00001.json to verify field names.\n", cfg.DebugDir)
	}
	return 0
}

func runReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config YAML")
	debugOverride := fs.String("debug", "", "override debug_dir (dump JSON pages here)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	if *debugOverride != "" {
		cfg.DebugDir = *debugOverride
	}

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		return 1
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
		fmt.Fprintf(os.Stderr, "client: %v\n", err)
		return 1
	}
	defer client.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	now := time.Now().UTC()
	since := now.AddDate(0, 0, -cfg.DaysBack)

	fmt.Printf("report: %s%s\n", cfg.BaseURL, cfg.APIRoot)
	fmt.Printf("window: %s .. %s (%d days)\n", since.Format(time.RFC3339), now.Format(time.RFC3339), cfg.DaysBack)
	fmt.Printf("output: %s    pacing=%dms    page_size=%d\n", cfg.OutputDir, cfg.RequestPacingMS, cfg.PageSize)
	if cfg.DebugDir != "" {
		fmt.Printf("debug: dumping JSON to %s\n", cfg.DebugDir)
	}

	t0 := time.Now()
	fmt.Println("\n[1/3] fetching lookup tables (templates, inventories, hosts)...")
	lookups, err := client.FetchLookups(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lookups: %v\n", err)
		return 1
	}
	fmt.Printf("      templates=%d  inventories=%d  hosts=%d  ansible_host=%d  (%.1fs)\n",
		len(lookups.Templates), len(lookups.Inventories), len(lookups.Hosts),
		len(lookups.AnsibleHost), time.Since(t0).Seconds())

	excl := aggregate.NewExcludeRules(cfg.ExcludeTemplates.IDs, cfg.ExcludeTemplates.NameContains)
	agg := aggregate.New(lookups, excl)

	csvOut, err := report.NewDetailCSV(cfg.OutputDir, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open detail csv: %v\n", err)
		return 1
	}

	t1 := time.Now()
	fmt.Printf("\n[2/3] iterating jobs since %s ...\n", since.Format(time.RFC3339))
	progressEvery := 50
	err = client.IterateJobsWithSummaries(ctx, since,
		func(j awx.JobLite) error {
			agg.AddJob(j)
			if int(agg.JobsSeen)%progressEvery == 0 {
				fmt.Printf("      jobs=%d  summaries=%d  pairs=%d  elapsed=%.0fs\n",
					agg.JobsSeen, agg.SummariesSeen, len(agg.Pairs), time.Since(t1).Seconds())
			}
			return nil
		},
		func(s awx.SummaryLite) error {
			agg.AddSummary(s)

			ansHost := ""
			invID := 0
			invName := ""
			excluded := false
			if s.Host != nil {
				if h, ok := lookups.Hosts[*s.Host]; ok {
					ansHost = lookups.AnsibleHost[h.ID]
					iid, iname := lookups.HostInventoryName(h.ID)
					invID, invName = iid, iname
				}
			}
			if s.SummaryFields.Job != nil {
				if t := agg.Templates[s.SummaryFields.Job.JobTemplateID]; t != nil {
					excluded = t.Excluded
				}
			}
			return csvOut.Write(s, ansHost, invID, invName, excluded)
		},
	)
	if err != nil {
		_ = csvOut.Close()
		fmt.Fprintf(os.Stderr, "\niteration failed: %v\n", err)
		return 1
	}
	if err := csvOut.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close detail csv: %v\n", err)
		return 1
	}
	fmt.Printf("      jobs=%d  summaries=%d  pairs=%d  unknown_tpl=%d  (%.1fs)\n",
		agg.JobsSeen, agg.SummariesSeen, len(agg.Pairs), agg.UnknownTplJobs, time.Since(t1).Seconds())
	fmt.Printf("      detail csv: %s\n", csvOut.Path())

	t2 := time.Now()
	fmt.Println("\n[3/3] rendering xlsx...")
	xlsxPath, err := report.WriteXLSX(cfg.OutputDir, agg, since, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "write xlsx: %v\n", err)
		return 1
	}
	fmt.Printf("      xlsx: %s  (%.1fs)\n", xlsxPath, time.Since(t2).Seconds())

	fmt.Printf("\ndone in %s\n", time.Since(t0).Truncate(time.Second))
	return 0
}
