// Package jira implements tracker.IssueTracker for Atlassian Jira.
//
// The Adapter wraps the existing JiraService, translating between the
// generic domain model (WorkItem, SearchCriteria) and Jira-specific types
// and API calls. This is a strangler fig adapter — it delegates all
// operations to the existing service while presenting the new interface.
package jira

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/services"
	"jira-ai-issue-solver/tracker"
)

// Compile-time check that Adapter implements tracker.IssueTracker.
var _ tracker.IssueTracker = (*Adapter)(nil)

// Adapter implements tracker.IssueTracker by delegating to the existing
// JiraService. It is a transitional component — once the migration to
// the new architecture is complete, this adapter can be replaced with a
// direct Jira API client that speaks the IssueTracker interface natively.
type Adapter struct {
	jira   services.JiraService
	logger *zap.Logger
}

// NewAdapter creates a Jira issue tracker adapter that wraps the given
// JiraService. Returns an error if jira or logger is nil.
func NewAdapter(jira services.JiraService, logger *zap.Logger) (*Adapter, error) {
	if jira == nil {
		return nil, errors.New("jira service must not be nil")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}
	return &Adapter{
		jira:   jira,
		logger: logger,
	}, nil
}

func (a *Adapter) SearchWorkItems(criteria models.SearchCriteria) ([]models.WorkItem, error) {
	if err := criteria.Validate(); err != nil {
		return nil, fmt.Errorf("search work items: %w", err)
	}

	jql := buildJQL(criteria)
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

func (a *Adapter) GetFieldValue(key, field string) (string, error) {
	fields, names, err := a.jira.GetTicketWithExpandedFields(key)
	if err != nil {
		return "", fmt.Errorf("get field %q for %s: %w", field, key, err)
	}

	// The names map is fieldID → human-readable name. We need to reverse-
	// look up to find the field ID for the requested name. Jira allows
	// duplicate display names across custom fields, so we warn if multiple
	// IDs match.
	var fieldID string
	for id, name := range names {
		if name == field {
			if fieldID != "" {
				a.logger.Warn("multiple field IDs share the same display name",
					zap.String("field", field),
					zap.String("ticket", key),
					zap.String("firstID", fieldID),
					zap.String("duplicateID", id))
			}
			fieldID = id
		}
	}
	if fieldID == "" {
		return "", fmt.Errorf("field %q not found on %s", field, key)
	}

	value, ok := fields[fieldID]
	if !ok || value == nil {
		return "", nil
	}

	if s, ok := value.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", value), nil
}

func (a *Adapter) SetFieldValue(key, field, value string) error {
	if err := a.jira.UpdateTicketFieldByName(key, field, value); err != nil {
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
// assignee, labels) and joined with AND. Map keys are sorted to ensure
// deterministic output for testability.
func buildJQL(criteria models.SearchCriteria) string {
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

	if criteria.AssignedTo != "" {
		conditions = append(conditions, fmt.Sprintf("assignee = %s", jqlQuote(criteria.AssignedTo)))
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

	return models.WorkItem{
		Key:           key,
		Summary:       fields.Summary,
		Description:   fields.Description,
		Type:          fields.IssueType.Name,
		Status:        fields.Status.Name,
		ProjectKey:    fields.Project.Key,
		Components:    components,
		Labels:        labels,
		Assignee:      assignee,
		SecurityLevel: securityLevel,
	}
}
