package report

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/TheGU/awxreport/internal/awx"
)

// DetailCSV streams every job_host_summary row to a CSV alongside the XLSX.
//
// The XLSX has aggregated views; this file is the raw data so downstream
// programs can pivot without re-fetching from AWX.
//
// Open the file, then call Write() once per summary, then Close().
type DetailCSV struct {
	f *os.File
	w *csv.Writer
}

func NewDetailCSV(outDir string, stamp time.Time) (*DetailCSV, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(outDir, fmt.Sprintf("awx-rollout-detail-%s.csv", stamp.UTC().Format("20060102-150405")))
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := csv.NewWriter(f)
	header := []string{
		"summary_id", "modified_utc", "job_id", "job_status",
		"template_id", "template_name",
		"host_id", "host_name", "ansible_host",
		"inventory_id", "inventory_name",
		"ok", "changed", "failures", "dark", "processed", "skipped",
		"failed", "ignored", "rescued",
		"template_excluded",
	}
	if err := w.Write(header); err != nil {
		f.Close()
		return nil, err
	}
	return &DetailCSV{f: f, w: w}, nil
}

// Path returns the file path so the caller can log it.
func (d *DetailCSV) Path() string { return d.f.Name() }

func (d *DetailCSV) Write(s awx.SummaryLite, ansibleHost string,
	invID int, invName string, excluded bool) error {
	tplID, tplName, jobStatus := 0, "", ""
	if s.SummaryFields.Job != nil {
		tplID = s.SummaryFields.Job.JobTemplateID
		tplName = s.SummaryFields.Job.JobTemplateName
		jobStatus = s.SummaryFields.Job.Status
	}
	hostID := 0
	if s.Host != nil {
		hostID = *s.Host
	}

	return d.w.Write([]string{
		strconv.Itoa(s.ID),
		s.Modified.UTC().Format(time.RFC3339),
		strconv.Itoa(s.Job),
		jobStatus,
		strconv.Itoa(tplID),
		tplName,
		strconv.Itoa(hostID),
		s.HostName,
		ansibleHost,
		strconv.Itoa(invID),
		invName,
		strconv.Itoa(s.OK),
		strconv.Itoa(s.Changed),
		strconv.Itoa(s.Failures),
		strconv.Itoa(s.Dark),
		strconv.Itoa(s.Processed),
		strconv.Itoa(s.Skipped),
		strconv.FormatBool(s.Failed),
		strconv.Itoa(s.Ignored),
		strconv.Itoa(s.Rescued),
		strconv.FormatBool(excluded),
	})
}

func (d *DetailCSV) Close() error {
	d.w.Flush()
	if err := d.w.Error(); err != nil {
		d.f.Close()
		return err
	}
	return d.f.Close()
}
