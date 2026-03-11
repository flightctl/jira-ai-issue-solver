package models

import (
	"fmt"
	"time"
)

// JiraTime handles Jira's custom date format
// Example: 2025-07-07T08:29:32.000+0000
// Go format: 2006-01-02T15:04:05.000-0700
type JiraTime struct {
	time.Time
}

func (jt *JiraTime) UnmarshalJSON(b []byte) error {
	// Remove quotes
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "null" || s == "" {
		jt.Time = time.Time{}
		return nil
	}
	// Try Jira format first
	t, err := time.Parse("2006-01-02T15:04:05.000-0700", s)
	if err == nil {
		jt.Time = t
		return nil
	}
	// Try RFC3339 with milliseconds and Z
	t, err = time.Parse("2006-01-02T15:04:05.000Z", s)
	if err == nil {
		jt.Time = t
		return nil
	}
	return fmt.Errorf("could not parse JiraTime: %w", err)
}

// JiraIssue represents a Jira issue
type JiraIssue struct {
	ID     string     `json:"id"`
	Self   string     `json:"self"`
	Key    string     `json:"key"`
	Fields JiraFields `json:"fields"`
}

// JiraFields represents the fields of a Jira issue
type JiraFields struct {
	Summary     string          `json:"summary"`
	Description string          `json:"description"`
	Status      JiraStatus      `json:"status"`
	IssueType   JiraIssueType   `json:"issuetype"`
	Project     JiraProject     `json:"project"`
	Components  []JiraComponent `json:"components"`
	Labels      []string        `json:"labels"`
	Created     JiraTime        `json:"created"`
	Updated     JiraTime        `json:"updated"`
	Creator     JiraUser        `json:"creator"`
	Reporter    JiraUser        `json:"reporter"`
	Assignee    *JiraUser       `json:"assignee,omitempty"`
	Comment     JiraComments    `json:"comment,omitempty"`
	Security    *JiraSecurity   `json:"security,omitempty"`
}

// JiraSecurity represents the security level of a Jira issue
type JiraSecurity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// JiraStatus represents the status of a Jira issue
type JiraStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type JiraIssueType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IconURL     string `json:"iconUrl"`
}

// JiraProject represents a Jira project
type JiraProject struct {
	ID         string            `json:"id"`
	Key        string            `json:"key"`
	Name       string            `json:"name"`
	Properties map[string]string `json:"properties,omitempty"`
}

// JiraUser represents a Jira user
type JiraUser struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}

// JiraComments represents comments on a Jira issue
type JiraComments struct {
	Comments   []JiraComment `json:"comments"`
	MaxResults int           `json:"maxResults"`
	Total      int           `json:"total"`
	StartAt    int           `json:"startAt"`
}

// JiraComment represents a comment on a Jira issue
type JiraComment struct {
	ID      string   `json:"id"`
	Body    string   `json:"body"`
	Author  JiraUser `json:"author"`
	Created JiraTime `json:"created"`
	Updated JiraTime `json:"updated"`
}

// JiraSearchResponse represents the response from a Jira search
type JiraSearchResponse struct {
	Expand     string      `json:"expand"`
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
	Issues     []JiraIssue `json:"issues"`
}

// JiraTicketResponse represents the response from getting a single Jira ticket
type JiraTicketResponse struct {
	ID     string     `json:"id"`
	Self   string     `json:"self"`
	Key    string     `json:"key"`
	Fields JiraFields `json:"fields"`
}

// JiraChangelog represents the changelog of a Jira issue
type JiraChangelog struct {
	ID    string `json:"id"`
	Items []struct {
		Field      string `json:"field"`
		FieldType  string `json:"fieldtype"`
		From       string `json:"from"`
		FromString string `json:"fromString"`
		To         string `json:"to"`
		ToString   string `json:"toString"`
	} `json:"items"`
}

// JiraComponent represents a Jira component
type JiraComponent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
