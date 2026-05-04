package awx

import "time"

// JobLite is the minimal slice of /api/v2/jobs/ we keep per row. We do NOT
// retain the full job record — it's heavy and the summaries already carry the
// template name. We only need the fields the aggregator counts.
type JobLite struct {
	ID         int       `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Failed     bool      `json:"failed"`
	Started    time.Time `json:"started"`
	Finished   time.Time `json:"finished"`
	JobTemplate int      `json:"job_template"`
}

// SummaryLite is one row from /api/v2/jobs/{id}/job_host_summaries/.
//
// `host` may be null (e.g. localhost / ad-hoc); in that case host_name still
// has the literal string used by the play, and we synthesise an ID downstream.
//
// summary_fields.job_template carries the canonical template id+name even
// when the job's job_template field is missing on certain launch types.
type SummaryLite struct {
	ID         int       `json:"id"`
	Job        int       `json:"job"`
	Host       *int      `json:"host"`
	HostName   string    `json:"host_name"`
	Created    time.Time `json:"created"`
	Modified   time.Time `json:"modified"`
	Changed    int       `json:"changed"`
	Dark       int       `json:"dark"`
	Failures   int       `json:"failures"`
	OK         int       `json:"ok"`
	Processed  int       `json:"processed"`
	Skipped    int       `json:"skipped"`
	Failed     bool      `json:"failed"`
	Ignored    int       `json:"ignored"`
	Rescued    int       `json:"rescued"`
	SummaryFields struct {
		Host *struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"host"`
		Job *struct {
			ID              int    `json:"id"`
			Name            string `json:"name"`
			Status          string `json:"status"`
			Failed          bool   `json:"failed"`
			JobTemplateID   int    `json:"job_template_id"`
			JobTemplateName string `json:"job_template_name"`
		} `json:"job"`
	} `json:"summary_fields"`
}

// HostLite is what we cache from /api/v2/hosts/.
type HostLite struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Variables   string `json:"variables"` // YAML or JSON text — may contain ansible_host
	Inventory   int    `json:"inventory"`
	Enabled     bool   `json:"enabled"`
	SummaryFields struct {
		Inventory *struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"inventory"`
	} `json:"summary_fields"`
}

// InventoryLite is what we cache from /api/v2/inventories/.
type InventoryLite struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// TemplateLite is what we cache from /api/v2/job_templates/.
type TemplateLite struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Playbook string `json:"playbook"`
}
