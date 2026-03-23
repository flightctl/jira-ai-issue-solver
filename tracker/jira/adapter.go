// Package jira implements tracker.IssueTracker for Atlassian Jira.
//
// The Adapter wraps a JiraClient implementation, translating between
// the generic domain model (WorkItem, SearchCriteria) and Jira-specific
// types and API calls.
package jira

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/tracker"
)

// JiraClient is the consumer-defined interface declaring only the Jira
// operations the adapter actually needs. Any concrete type whose methods
// match (e.g. *services.JiraServiceImpl) satisfies it implicitly.
type JiraClient interface {
	SearchTickets(jql string) (*models.JiraSearchResponse, error)
	GetTicket(key string) (*models.JiraTicketResponse, error)
	GetTicketSecurityLevel(key string) (*models.JiraSecurity, error)
	UpdateTicketStatus(key string, status string) error
	AddComment(key string, comment string) error
	UpdateTicketFieldByName(key string, fieldName string, value interface{}) error
	GetFieldIDByName(fieldName string) (string, error)
	DownloadAttachment(url string) ([]byte, error)
}

// Compile-time check that Adapter implements tracker.IssueTracker.
var _ tracker.IssueTracker = (*Adapter)(nil)

// Adapter implements tracker.IssueTracker by delegating to a JiraClient.
type Adapter struct {
	jira                JiraClient
	logger              *zap.Logger
	contributorFieldRef string
}

// NewAdapter creates a Jira issue tracker adapter that wraps the given
// JiraClient. At construction time it resolves the "Contributors" custom
// field ID so that JQL queries use the cf[ID] syntax, which is reliable
// across Jira Cloud instances where the display name may not match the
// JQL field name. If the lookup fails, it falls back to the display name.
func NewAdapter(jira JiraClient, logger *zap.Logger) (*Adapter, error) {
	if jira == nil {
		return nil, errors.New("jira service must not be nil")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}

	contributorRef := resolveContributorField(jira, logger)

	return &Adapter{
		jira:                jira,
		logger:              logger,
		contributorFieldRef: contributorRef,
	}, nil
}

// resolveContributorField looks up the "Contributors" field in Jira and
// returns a JQL-safe reference. Custom fields (customfield_NNNNN) are
// converted to cf[NNNNN] syntax; system fields use their ID directly.
// Falls back to "Contributors" if the lookup fails.
func resolveContributorField(client JiraClient, logger *zap.Logger) string {
	const fieldName = "Contributors"

	fieldID, err := client.GetFieldIDByName(fieldName)
	if err != nil {
		logger.Warn("Could not resolve Contributors field ID, falling back to display name",
			zap.Error(err))
		return fieldName
	}

	numericID, isCustom := strings.CutPrefix(fieldID, "customfield_")
	if isCustom {
		ref := fmt.Sprintf("cf[%s]", numericID)
		logger.Info("Resolved Contributors field",
			zap.String("fieldID", fieldID),
			zap.String("jqlRef", ref))
		return ref
	}

	return fieldID
}

func (a *Adapter) SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error) {
	if err := criteria.Validate(); err != nil {
		return nil, fmt.Errorf("search work items: %w", err)
	}

	jql := buildJQL(criteria, a.contributorFieldRef)
	a.logger.Debug("Searching work items", zap.String("jql", jql))

	resp, err := a.jira.SearchTickets(jql)
	if err != nil {
		return nil, fmt.Errorf("search work items: %w", err)
	}

	items := make([]models.WorkItem, 0, len(resp.Issues))
	for _, issue := range resp.Issues {
		// Search results include security level when Jira returns it in
		// the standard field. For guaranteed security level resolution
		// (including custom field fallback), use GetWorkItem.
		items = append(items, mapFieldsToWorkItem(issue.Key, issue.Fields, issue.Fields.Security))
	}
	return items, nil
}

func (a *Adapter) GetWorkItem(key string) (*models.WorkItem, error) {
	ticket, err := a.jira.GetTicket(key)
	if err != nil {
		return nil, fmt.Errorf("get work item %s: %w", key, err)
	}

	// GetTicketSecurityLevel performs a thorough check: it first looks at
	// the standard security field, then falls back to expanded field
	// lookup for instances that use custom security fields.
	security, err := a.jira.GetTicketSecurityLevel(key)
	if err != nil {
		return nil, fmt.Errorf("get security level for %s: %w", key, err)
	}

	item := mapFieldsToWorkItem(ticket.Key, ticket.Fields, security)
	return &item, nil
}

func (a *Adapter) TransitionStatus(key, status string) error {
	if err := a.jira.UpdateTicketStatus(key, status); err != nil {
		return fmt.Errorf("transition %s to %q: %w", key, status, err)
	}
	return nil
}

func (a *Adapter) AddComment(key, body string) error {
	if err := a.jira.AddComment(key, body); err != nil {
		return fmt.Errorf("add comment to %s: %w", key, err)
	}
	return nil
}

func (a *Adapter) DownloadAttachment(url string) ([]byte, error) {
	data, err := a.jira.DownloadAttachment(url)
	if err != nil {
		return nil, fmt.Errorf("download attachment: %w", err)
	}
	return data, nil
}

func (a *Adapter) SetFieldValue(key, field, value string) error {
	// Jira Cloud API v3 requires Atlassian Document Format for text-type
	// custom fields. Wrap the plain string in ADF so the update succeeds.
	adfValue := models.TextToADF(value)
	if err := a.jira.UpdateTicketFieldByName(key, field, adfValue); err != nil {
		return fmt.Errorf("set field %q on %s: %w", field, key, err)
	}
	return nil
}

// jqlQuote wraps a value in double quotes for JQL, escaping any embedded
// double quotes to prevent malformed queries or JQL injection.
func jqlQuote(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// buildJQL converts a SearchCriteria into a Jira JQL query string.
//
// Conditions are emitted in a fixed order (project, type+status, status,
// contributor, labels) and joined with AND. Map keys are sorted to ensure
// deterministic output for testability.
//
// contributorFieldRef is the JQL field reference for the Contributors
// field (e.g., "cf[10466]" or "Contributors").
func buildJQL(criteria models.SearchCriteria, contributorFieldRef string) string {
	var conditions []string

	if len(criteria.ProjectKeys) > 0 {
		quoted := make([]string, len(criteria.ProjectKeys))
		for i, key := range criteria.ProjectKeys {
			quoted[i] = jqlQuote(key)
		}
		conditions = append(conditions, fmt.Sprintf("project IN (%s)", strings.Join(quoted, ", ")))
	}

	if len(criteria.StatusByType) > 0 {
		var typeConditions []string
		for _, ticketType := range slices.Sorted(maps.Keys(criteria.StatusByType)) {
			statuses := criteria.StatusByType[ticketType]
			if len(statuses) == 0 {
				continue
			}
			if len(statuses) == 1 {
				typeConditions = append(typeConditions,
					fmt.Sprintf("(issuetype = %s AND status = %s)", jqlQuote(ticketType), jqlQuote(statuses[0])))
			} else {
				quoted := make([]string, len(statuses))
				for i, s := range statuses {
					quoted[i] = jqlQuote(s)
				}
				typeConditions = append(typeConditions,
					fmt.Sprintf("(issuetype = %s AND status IN (%s))", jqlQuote(ticketType), strings.Join(quoted, ", ")))
			}
		}
		if len(typeConditions) > 0 {
			conditions = append(conditions, fmt.Sprintf("(%s)", strings.Join(typeConditions, " OR ")))
		}
	}

	if len(criteria.Statuses) > 0 {
		quoted := make([]string, len(criteria.Statuses))
		for i, s := range criteria.Statuses {
			quoted[i] = jqlQuote(s)
		}
		conditions = append(conditions, fmt.Sprintf("status IN (%s)", strings.Join(quoted, ", ")))
	}

	if criteria.ContributorIsCurrentUser {
		conditions = append(conditions, fmt.Sprintf("%s = currentUser()", contributorFieldRef))
	}

	if len(criteria.Labels) > 0 {
		quoted := make([]string, len(criteria.Labels))
		for i, l := range criteria.Labels {
			quoted[i] = jqlQuote(l)
		}
		conditions = append(conditions, fmt.Sprintf("labels IN (%s)", strings.Join(quoted, ", ")))
	}

	jql := strings.Join(conditions, " AND ")

	if criteria.OrderBy != "" {
		if jql != "" {
			jql += " "
		}
		jql += "ORDER BY " + criteria.OrderBy
	}

	return jql
}

// mapFieldsToWorkItem converts Jira fields into a WorkItem. Shared by both
// SearchWorkItems (from JiraIssue) and GetWorkItem (from JiraTicketResponse),
// since both use the same underlying JiraFields struct.
func mapFieldsToWorkItem(key string, fields models.JiraFields, security *models.JiraSecurity) models.WorkItem {
	components := make([]string, 0, len(fields.Components))
	for _, c := range fields.Components {
		components = append(components, c.Name)
	}

	labels := fields.Labels
	if labels == nil {
		labels = []string{}
	}

	var assignee *models.Author
	if fields.Assignee != nil {
		assignee = &models.Author{
			Name:     fields.Assignee.DisplayName,
			Email:    fields.Assignee.EmailAddress,
			Username: fields.Assignee.Name,
		}
	}

	var securityLevel string
	if security != nil && security.Name != "" && !strings.EqualFold(security.Name, "none") {
		securityLevel = security.Name
	}

	attachments := make([]models.Attachment, 0, len(fields.Attachment))
	for _, a := range fields.Attachment {
		attachments = append(attachments, models.Attachment{
			Filename: a.Filename,
			MimeType: a.MimeType,
			Size:     a.Size,
			URL:      a.Content,
		})
	}

	return models.WorkItem{
		Key:           key,
		Summary:       fields.Summary,
		Description:   string(fields.Description),
		Type:          fields.IssueType.Name,
		Status:        fields.Status.Name,
		ProjectKey:    fields.Project.Key,
		Components:    components,
		Labels:        labels,
		Assignee:      assignee,
		SecurityLevel: securityLevel,
		Attachments:   attachments,
	}
}
