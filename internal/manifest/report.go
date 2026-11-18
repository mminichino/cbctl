package manifest

import "time"

// Step statuses. Every step of a Report has exactly one.
const (
	// StepDone means the step changed something.
	StepDone = "done"
	// StepUnchanged means the step found its goal already reached and wrote nothing.
	StepUnchanged = "unchanged"
	// StepSkipped means the step did not run (disabled by the manifest, or nothing to do).
	StepSkipped = "skipped"
	// StepPlanned means a dry run would run the step.
	StepPlanned = "planned"
	// StepFailed means the step ran and failed; later steps are not run.
	StepFailed = "failed"
)

// Document statuses of a Report.
const (
	// ReportApplied means every step ran (changes or not).
	ReportApplied = "applied"
	// ReportUnchanged means no step changed anything.
	ReportUnchanged = "unchanged"
	// ReportPlanned means a dry run: nothing was written.
	ReportPlanned = "planned"
	// ReportFailed means a step failed.
	ReportFailed = "failed"
	// ReportNotRun means an earlier document failed, so this one was not attempted.
	ReportNotRun = "not-run"
	// ReportInterrupted means Ctrl-C stopped the document; its completed steps stay in place.
	ReportInterrupted = "interrupted"
)

// Step is one unit of work of an apply: for Server a node, the rebalance, a bucket, scope,
// collection or import.
type Step struct {
	// Name is the kind of step ("bootstrap", "join", "rebalance", "bucket", "scope", "collection",
	// "import").
	Name string `json:"name"`
	// Target says what the step acts on ("10.0.0.2", "orders", "orders.sales.items").
	Target string `json:"target,omitempty"`
	// Status is one of the Step* constants.
	Status string `json:"status"`
	// Detail adds a sentence of context: why a step was skipped, how many documents were imported.
	Detail string `json:"detail,omitempty"`
	// Count is the number of items the step handled (imported documents); 0 when not applicable.
	Count int `json:"count,omitempty"`
	// Error is the failure message of a failed step.
	Error string `json:"error,omitempty"`
	// Duration is how long the step took. It is rendered as durationMs.
	Duration time.Duration `json:"-"`
	// DurationMs is Duration in milliseconds, for JSON.
	DurationMs int64 `json:"durationMs,omitempty"`
}

// Report is the outcome of applying one manifest document.
type Report struct {
	// Kind and Name identify the document ("CouchbaseServer", "demo").
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Source is the manifest file the document came from ("<stdin>" for stdin).
	Source string `json:"source,omitempty"`
	// Status is one of the Report* constants.
	Status string `json:"status"`
	// Steps lists what ran, in order. A failed document ends with its failed step.
	Steps []Step `json:"steps"`
	// Summary says what the document holds ("3 nodes, 1 bucket, 2 collections").
	Summary string `json:"summary,omitempty"`
	// Warnings are the non-fatal problems found (cluster nodes not in the manifest, a bucket
	// quota that differs from the manifest).
	Warnings []string `json:"warnings,omitempty"`
	// Error is the failure message of a failed document.
	Error string `json:"error,omitempty"`
}

// Add appends a step, filling DurationMs from Duration.
func (r *Report) Add(s Step) {
	if s.Duration > 0 && s.DurationMs == 0 {
		s.DurationMs = s.Duration.Milliseconds()
	}
	r.Steps = append(r.Steps, s)
}

// Warn records a warning.
func (r *Report) Warn(msg string) { r.Warnings = append(r.Warnings, msg) }

// Finish sets Status from the steps: failed if any step failed, planned if any is planned,
// unchanged if every step is unchanged or skipped, and applied otherwise.
func (r *Report) Finish() {
	if r.Status == ReportFailed || r.Status == ReportInterrupted {
		return
	}
	status := ReportUnchanged
	for _, s := range r.Steps {
		switch s.Status {
		case StepFailed:
			r.Status = ReportFailed
			return
		case StepPlanned:
			status = ReportPlanned
		case StepDone:
			if status != ReportPlanned {
				status = ReportApplied
			}
		}
	}
	r.Status = status
}

// ApplyReport is the JSON document `cbctl apply -o json` prints: one Report per manifest
// document, in apply order.
type ApplyReport struct {
	// APIVersion is APIVersion of the manifest format.
	APIVersion string `json:"apiVersion"`
	// DryRun is true when nothing was written.
	DryRun bool `json:"dryRun"`
	// Status is "applied", "unchanged", "planned" or "failed".
	Status string `json:"status"`
	// Documents holds one report per manifest document.
	Documents []*Report `json:"documents"`
}
