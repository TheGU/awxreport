// Package main is the awxreport CLI entrypoint.
//
// Subcommands:
//
//	probe   — validate connectivity and the API shape against a controller.
//	report  — generate the monthly XLSX + CSV.
//	version — print build metadata.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/TheGU/awxreport/internal/config"
)

// Build metadata, populated via -ldflags at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// globalOpts mirrors the persistent flags shared by every subcommand.
type globalOpts struct {
	configPath string
	debugDir   string
	noColor    bool
	verbose    bool
}

// loadedConfig is a config.Config alias kept local so subcommands import one
// thing instead of two. Eventually we may add CLI-only fields here.
type loadedConfig = config.Config

func loadConfig(opts globalOpts) (*loadedConfig, error) {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return nil, err
	}
	if opts.debugDir != "" {
		cfg.DebugDir = opts.debugDir
	}
	return cfg, nil
}

func main() {
	app := &cli.Command{
		Name:    "awxreport",
		Usage:   "AWX/AAP monthly rollout report — XLSX + CSV from the controller REST API",
		Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		Description: `awxreport pulls an AWX or AAP controller and writes a monthly XLSX with
playbook and host activity, plus a flat CSV of every job_host_summary row.

The report is scoped by 'days_back' in the config (default 30). The token is
read from the AWX_TOKEN environment variable; it is never read from the file.

See https://github.com/TheGU/awxreport for full documentation.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Value:   "config.yaml",
				Usage:   "path to YAML config file",
				Sources: cli.EnvVars("AWXREPORT_CONFIG"),
			},
			&cli.StringFlag{
				Name:  "debug",
				Usage: "directory to dump every API JSON response into (overrides config)",
			},
			&cli.BoolFlag{
				Name:  "no-color",
				Usage: "disable ANSI colour even on a TTY",
			},
			&cli.BoolFlag{
				Name:  "verbose",
				Usage: "more verbose output",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "probe",
				Usage: "Validate connectivity, auth, and API shape (cheap; one page per endpoint)",
				Description: `Hits ping/, me/, and one page each of templates, inventories, hosts,
jobs, and per-job summaries. Prints a summary and exits non-zero on failure.

Run probe before scheduling 'report' on a new controller — it confirms the
token works and that the API root path matches the AWX/AAP version.`,
				Action: func(ctx context.Context, c *cli.Command) error {
					return withSignalContext(ctx, func(ctx context.Context) error {
						return runProbe(ctx, uiFromCmd(c), optsFromCmd(c))
					})
				},
			},
			{
				Name:  "report",
				Usage: "Generate the XLSX + CSV monthly report",
				Description: `Walks /api/v2/jobs/ within the configured window, fetches per-job
summaries, aggregates per-template and per-host counters, and writes:

  out/awx-rollout-<ts>.xlsx          5 sheets (Playbooks, Hosts,
                                     PlaybookHosts, Excluded, Meta)
  out/awx-rollout-detail-<ts>.csv    one row per job_host_summary

Configure noisy template filtering under 'exclude_templates' in config.yaml.`,
				Action: func(ctx context.Context, c *cli.Command) error {
					return withSignalContext(ctx, func(ctx context.Context) error {
						return runReport(ctx, uiFromCmd(c), optsFromCmd(c))
					})
				},
			},
			{
				Name:  "version",
				Usage: "Print build metadata and exit",
				Action: func(_ context.Context, c *cli.Command) error {
					u := uiFromCmd(c)
					u.table([][2]string{
						{"version", version},
						{"commit", commit},
						{"built", date},
						{"go", runtime.Version()},
						{"os/arch", runtime.GOOS + "/" + runtime.GOARCH},
					})
					return nil
				},
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func uiFromCmd(c *cli.Command) *ui {
	return newUI(os.Stdout, os.Stderr, !c.Bool("no-color"), c.Bool("verbose"))
}

func optsFromCmd(c *cli.Command) globalOpts {
	return globalOpts{
		configPath: c.String("config"),
		debugDir:   c.String("debug"),
		noColor:    c.Bool("no-color"),
		verbose:    c.Bool("verbose"),
	}
}

// withSignalContext wraps an action so Ctrl-C / SIGTERM cleanly cancels the
// inner context, letting in-flight HTTP requests bail and the writer flush.
func withSignalContext(parent context.Context, fn func(context.Context) error) error {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return fn(ctx)
}
