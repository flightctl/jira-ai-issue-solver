package models

// WorkItem is the tracker-agnostic representation of a unit of work.
// It captures the fields the system needs to process a ticket regardless
// of whether the source is Jira, GitHub Issues, or another tracker.
//
// Adapters are responsible for mapping their native types to WorkItem,
// normalizing nil slices to empty slices, and resolving security levels.
type WorkItem struct {
	// Key is the unique identifier (e.g., "PROJ-123").
	Key string

	// Summary is the short title.
	Summary string

	// Description is the full description.
	Description string

	// Type is the work item category (e.g., "Bug", "Story", "Task").
	Type string

	// Status is the current workflow status (e.g., "Open", "In Progress").
	Status string

	// ProjectKey identifies the project (e.g., "PROJ").
	ProjectKey string

	// Components lists the component names associated with this work item.
	// Always non-nil; empty slice when no components are set.
	Components []string

	// Labels lists the labels applied to this work item.
	// Always non-nil; empty slice when no labels are set.
	Labels []string

	// Assignee is the person assigned, or nil if unassigned.
	Assignee *Author

	// SecurityLevel is the security level name, or empty if none is set.
	// A level named "None" (case-insensitive) is treated as no security level.
	SecurityLevel string

	// Attachments lists files attached to the work item.
	// Always non-nil; empty slice when no attachments are present.
	Attachments []Attachment
}

// Attachment represents a file attached to a work item.
type Attachment struct {
	// Filename is the original name of the file.
	Filename string

	// MimeType is the MIME type (e.g., "text/plain", "image/png").
	MimeType string

	// Size is the file size in bytes.
	Size int64

	// URL is the tracker-specific download URL.
	URL string
}

// HasSecurityLevel reports whether this work item has a security level set.
func (w WorkItem) HasSecurityLevel() bool {
	return w.SecurityLevel != ""
}
