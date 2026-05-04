package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
)

// ui holds output sinks and colour helpers. It auto-detects TTY at startup
// and respects the --no-color flag.
type ui struct {
	out, err io.Writer
	color    bool
	verbose  bool

	// progress de-duping: don't reprint the same line N times.
	progressMu sync.Mutex
	lastProg   string
}

func newUI(out, errOut io.Writer, useColor, verbose bool) *ui {
	if useColor {
		// Honour NO_COLOR env (https://no-color.org) and force-disable when not a TTY.
		if os.Getenv("NO_COLOR") != "" {
			useColor = false
		} else if f, ok := out.(*os.File); ok && !isatty.IsTerminal(f.Fd()) && !isatty.IsCygwinTerminal(f.Fd()) {
			useColor = false
		}
	}
	color.NoColor = !useColor
	return &ui{out: out, err: errOut, color: useColor, verbose: verbose}
}

func writef(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func writeln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, a...)
}

func (u *ui) infof(format string, a ...any) {
	writef(u.out, format, a...)
}

func (u *ui) banner(title string) {
	bar := strings.Repeat("─", len(title)+4)
	if u.color {
		c := color.New(color.FgCyan, color.Bold).SprintFunc()
		writef(u.out, "%s\n%s\n%s\n", c(bar), c("  "+title), c(bar))
	} else {
		writef(u.out, "%s\n  %s\n%s\n", bar, title, bar)
	}
}

func (u *ui) section(label string) {
	if u.color {
		writeln(u.out, color.New(color.FgCyan, color.Bold).Sprint("\n"+label))
	} else {
		writeln(u.out, "\n"+label)
	}
}

func (u *ui) ok(format string, a ...any) {
	prefix := "OK"
	if u.color {
		prefix = color.GreenString("OK")
	}
	writef(u.out, "  [%s] %s\n", prefix, fmt.Sprintf(format, a...))
}

func (u *ui) warn(format string, a ...any) {
	prefix := "WARN"
	if u.color {
		prefix = color.YellowString("WARN")
	}
	writef(u.err, "  [%s] %s\n", prefix, fmt.Sprintf(format, a...))
}

func (u *ui) failf(format string, a ...any) {
	prefix := "FAIL"
	if u.color {
		prefix = color.RedString("FAIL")
	}
	writef(u.err, "  [%s] %s\n", prefix, fmt.Sprintf(format, a...))
}

// progress prints a single transient line if stdout is a TTY, else falls
// through to a plain line per call. We don't fight the terminal — if the
// caller is piping to a file, every call gets its own line.
func (u *ui) progress(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	u.progressMu.Lock()
	defer u.progressMu.Unlock()

	if u.isTTY() {
		// Erase the previous line and write the new one. Keep cursor on the
		// same row by emitting CR (no LF) so the next progress call overwrites.
		writef(u.out, "\r\033[2K  %s", line)
		u.lastProg = line
	} else {
		writef(u.out, "  %s\n", line)
	}
}

// progressDone finalises an in-place progress line with a newline so
// subsequent output starts on a fresh line.
func (u *ui) progressDone() {
	u.progressMu.Lock()
	defer u.progressMu.Unlock()
	if u.isTTY() && u.lastProg != "" {
		writeln(u.out)
	}
	u.lastProg = ""
}

func (u *ui) isTTY() bool {
	f, ok := u.out.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// table prints a borderless aligned key/value table. Used for run summaries.
func (u *ui) table(rows [][2]string) {
	maxK := 0
	for _, r := range rows {
		if len(r[0]) > maxK {
			maxK = len(r[0])
		}
	}
	for _, r := range rows {
		key := r[0]
		if u.color {
			key = color.New(color.FgHiBlack).Sprint(key)
			key += strings.Repeat(" ", maxK-len(r[0]))
		} else {
			key = key + strings.Repeat(" ", maxK-len(r[0]))
		}
		writef(u.out, "  %s  %s\n", key, r[1])
	}
}

// fmtRate is a small helper for ETA strings.
func fmtRate(count int64, elapsed time.Duration) string {
	if count == 0 || elapsed == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f/s", float64(count)/elapsed.Seconds())
}
