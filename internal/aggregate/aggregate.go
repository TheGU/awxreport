// Package aggregate streams jobs + per-host summaries into in-memory counters.
//
// Memory model: three maps keyed by id. Pair entries (template_id, host_id)
// dominate; sparse worst case ~500k entries × ~120 B = ~60 MB.
//
// We never retain the raw rows. Callers feed jobs and summaries one at a
// time; the aggregator decides what to keep. Counts are int64 — the raw
// summary fields are int but multiplied by millions of summaries can overflow
// int32 in extreme environments.
package aggregate

import (
	"strings"
	"time"

	"github.com/TheGU/awxreport/internal/awx"
)

// ExcludeRules is the noisy-template filter from config.
type ExcludeRules struct {
	IDs          map[int]bool
	NameContains []string // already lower-cased
}

// NewExcludeRules normalises the config-level rules.
func NewExcludeRules(ids []int, nameContains []string) ExcludeRules {
	r := ExcludeRules{IDs: make(map[int]bool, len(ids))}
	for _, id := range ids {
		r.IDs[id] = true
	}
	for _, s := range nameContains {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			r.NameContains = append(r.NameContains, s)
		}
	}
	return r
}

func (r ExcludeRules) Matches(id int, name string) (bool, string) {
	if r.IDs[id] {
		return true, "id"
	}
	lname := strings.ToLower(name)
	for _, sub := range r.NameContains {
		if strings.Contains(lname, sub) {
			return true, "name:" + sub
		}
	}
	return false, ""
}

// PairKey identifies one (template, host) pair.
//
// HostID == 0 is reserved for hosts that came in with `host: null` in the
// summary (e.g. localhost in an ad-hoc play); their literal name lives in the
// host registry under a synthetic negative id.
type PairKey struct {
	TemplateID int
	HostID     int
}

type TemplateAgg struct {
	ID            int
	Name          string
	Playbook      string
	Excluded      bool
	ExcludeReason string

	Jobs         int64
	JobsOK       int64 // status == "successful"
	JobsFailed   int64 // status == "failed"
	JobsError    int64 // status == "error"
	JobsCanceled int64 // status == "canceled"

	HostRuns   int64 // total summary rows
	HostOK     int64 // rows where !failed
	HostFailed int64 // rows where failed
	HostDark   int64 // sum of `dark` field

	DistinctHosts map[int]struct{}
	LastRun       time.Time // any job, regardless of status
	LastOK        time.Time // last job with status=successful
	LastFailed    time.Time // last job with status in {failed, error}
}

type HostAgg struct {
	ID            int // 0 means synthesised (see Aggregator.synthHosts)
	Name          string
	AnsibleHost   string
	InventoryID   int
	InventoryName string
	Enabled       bool

	Runs   int64
	OK     int64 // !failed summaries
	Failed int64
	Dark   int64
	EverOK bool

	DistinctTemplates map[int]struct{}
	LastRun           time.Time // any summary
	LastOK            time.Time // summary where !failed
	LastFailed        time.Time // summary where failed
}

type PairAgg struct {
	Runs       int64
	OK         int64
	Failed     int64
	Changed    int64
	Dark       int64
	LastRun    time.Time
	LastOK     time.Time
	LastFailed time.Time
}

// Aggregator is the central state. Not safe for concurrent use — wrap with a
// mutex if callers ever go parallel (we don't, by design).
type Aggregator struct {
	Lookups *awx.Lookups
	Exclude ExcludeRules

	Templates map[int]*TemplateAgg
	Hosts     map[int]*HostAgg
	Pairs     map[PairKey]*PairAgg

	// Synthetic host bucket: when SummaryLite.Host is nil we still want to
	// account for the row. Keyed by host_name, value is a negative pseudo-id
	// we hand out so PairKey stays uniformly int.
	synthHosts map[string]int
	synthSeq   int

	// Counters for the report header / log.
	JobsSeen       int64
	SummariesSeen  int64
	UnknownTplJobs int64 // jobs whose template_id is not in lookups
}

func New(l *awx.Lookups, ex ExcludeRules) *Aggregator {
	a := &Aggregator{
		Lookups:    l,
		Exclude:    ex,
		Templates:  make(map[int]*TemplateAgg),
		Hosts:      make(map[int]*HostAgg),
		Pairs:      make(map[PairKey]*PairAgg),
		synthHosts: make(map[string]int),
	}
	// Pre-populate templates so excluded ones still appear with zero job
	// counts if no jobs ran for them in the window. Same for hosts — useful
	// for "ever_successful = false" semantic when a host had zero runs.
	for id, t := range l.Templates {
		a.Templates[id] = &TemplateAgg{
			ID: id, Name: t.Name, Playbook: t.Playbook,
			DistinctHosts: make(map[int]struct{}),
		}
		if hit, why := ex.Matches(id, t.Name); hit {
			a.Templates[id].Excluded = true
			a.Templates[id].ExcludeReason = why
		}
	}
	for id, h := range l.Hosts {
		invID, invName := l.HostInventoryName(id)
		a.Hosts[id] = &HostAgg{
			ID: id, Name: h.Name,
			AnsibleHost:       l.AnsibleHost[id],
			InventoryID:       invID,
			InventoryName:     invName,
			Enabled:           h.Enabled,
			DistinctTemplates: make(map[int]struct{}),
		}
	}
	return a
}

// AddJob records a job's status against its template. Templates that aren't
// in the lookup table get a placeholder entry created lazily — this happens
// for jobs whose template was deleted between job-run time and report-run
// time.
func (a *Aggregator) AddJob(j awx.JobLite) {
	a.JobsSeen++
	if j.JobTemplate == 0 {
		a.UnknownTplJobs++
		return
	}
	t := a.getOrInitTemplate(j.JobTemplate, "")
	t.Jobs++
	switch j.Status {
	case "successful":
		t.JobsOK++
		if j.Finished.After(t.LastOK) {
			t.LastOK = j.Finished
		}
	case "failed":
		t.JobsFailed++
		if j.Finished.After(t.LastFailed) {
			t.LastFailed = j.Finished
		}
	case "error":
		t.JobsError++
		if j.Finished.After(t.LastFailed) {
			t.LastFailed = j.Finished
		}
	case "canceled":
		t.JobsCanceled++
	}
	if j.Finished.After(t.LastRun) {
		t.LastRun = j.Finished
	}
}

// AddSummary records one job_host_summary row. The job parameter supplies
// the status when summary_fields.job is missing (older AWX behavior).
func (a *Aggregator) AddSummary(s awx.SummaryLite) {
	a.SummariesSeen++

	// Resolve template id+name. Prefer summary_fields (always present in 24.x).
	tplID := 0
	tplName := ""
	if s.SummaryFields.Job != nil {
		tplID = s.SummaryFields.Job.JobTemplateID
		tplName = s.SummaryFields.Job.JobTemplateName
	}
	if tplID == 0 {
		// We can't bucket this row anywhere meaningful. Skip silently —
		// counted in SummariesSeen but not reflected per-template.
		return
	}
	t := a.getOrInitTemplate(tplID, tplName)

	// Resolve host id (0 = synthetic).
	hostID := 0
	hostName := s.HostName
	if s.Host != nil {
		hostID = *s.Host
	} else if s.SummaryFields.Host != nil {
		hostID = s.SummaryFields.Host.ID
	}
	if hostID == 0 {
		hostID = a.synthHostID(hostName)
	}
	h := a.getOrInitHost(hostID, hostName)

	// Pair entry.
	key := PairKey{TemplateID: tplID, HostID: hostID}
	p, ok := a.Pairs[key]
	if !ok {
		p = &PairAgg{}
		a.Pairs[key] = p
	}
	p.Runs++
	if s.Failed {
		p.Failed++
		if s.Modified.After(p.LastFailed) {
			p.LastFailed = s.Modified
		}
	} else {
		p.OK++
		if s.Modified.After(p.LastOK) {
			p.LastOK = s.Modified
		}
	}
	p.Changed += int64(s.Changed)
	p.Dark += int64(s.Dark)
	if s.Modified.After(p.LastRun) {
		p.LastRun = s.Modified
	}

	// Template summary-level counters.
	t.HostRuns++
	if s.Failed {
		t.HostFailed++
	} else {
		t.HostOK++
	}
	t.HostDark += int64(s.Dark)
	t.DistinctHosts[hostID] = struct{}{}

	// Host counters.
	h.Runs++
	if s.Failed {
		h.Failed++
		if s.Modified.After(h.LastFailed) {
			h.LastFailed = s.Modified
		}
	} else {
		h.OK++
		h.EverOK = true
		if s.Modified.After(h.LastOK) {
			h.LastOK = s.Modified
		}
	}
	h.Dark += int64(s.Dark)
	h.DistinctTemplates[tplID] = struct{}{}
	if s.Modified.After(h.LastRun) {
		h.LastRun = s.Modified
	}
}

func (a *Aggregator) getOrInitTemplate(id int, hintName string) *TemplateAgg {
	t, ok := a.Templates[id]
	if ok {
		if t.Name == "" && hintName != "" {
			t.Name = hintName
			if hit, why := a.Exclude.Matches(id, hintName); hit {
				t.Excluded = true
				t.ExcludeReason = why
			}
		}
		return t
	}
	t = &TemplateAgg{
		ID: id, Name: hintName,
		DistinctHosts: make(map[int]struct{}),
	}
	if hit, why := a.Exclude.Matches(id, hintName); hit {
		t.Excluded = true
		t.ExcludeReason = why
	}
	a.Templates[id] = t
	return t
}

func (a *Aggregator) getOrInitHost(id int, hintName string) *HostAgg {
	h, ok := a.Hosts[id]
	if ok {
		if h.Name == "" && hintName != "" {
			h.Name = hintName
		}
		return h
	}
	h = &HostAgg{
		ID: id, Name: hintName,
		DistinctTemplates: make(map[int]struct{}),
	}
	a.Hosts[id] = h
	return h
}

func (a *Aggregator) synthHostID(name string) int {
	if id, ok := a.synthHosts[name]; ok {
		return id
	}
	a.synthSeq--
	a.synthHosts[name] = a.synthSeq
	return a.synthSeq
}
