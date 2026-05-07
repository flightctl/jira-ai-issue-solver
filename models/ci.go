package models

// CheckRunFailure represents a single failed check run with its
// extracted diagnostic information.
type CheckRunFailure struct {
	ID          int64
	Name        string
	HTMLURL     string
	Conclusion  string // "failure", "timed_out", "cancelled"
	Summary     string
	Text        string
	Annotations []CheckAnnotation
	FailedSteps []FailedStep
}

// CheckAnnotation is a structured diagnostic from a check run
// with file and line location.
type CheckAnnotation struct {
	Path      string
	StartLine int
	EndLine   int
	Level     string // "failure", "warning", "notice"
	Message   string
	Title     string
}

// FailedStep holds the name and truncated log output from a
// workflow job step that failed.
type FailedStep struct {
	JobName  string
	StepName string
	Log      string
}
