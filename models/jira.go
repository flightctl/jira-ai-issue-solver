package models

import (
	"encoding/json"
	"fmt"
	"strings"
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

// JiraFields represents the fields of a Jira issue.
// Jira Cloud API v3 returns description and comment bodies in Atlassian
// Document Format (ADF). The ADFText type handles extracting plain text.
type JiraFields struct {
	Summary     string           `json:"summary"`
	Description ADFText          `json:"description"`
	Status      JiraStatus       `json:"status"`
	IssueType   JiraIssueType    `json:"issuetype"`
	Project     JiraProject      `json:"project"`
	Components  []JiraComponent  `json:"components"`
	Labels      []string         `json:"labels"`
	Created     JiraTime         `json:"created"`
	Updated     JiraTime         `json:"updated"`
	Creator     JiraUser         `json:"creator"`
	Reporter    JiraUser         `json:"reporter"`
	Assignee    *JiraUser        `json:"assignee,omitempty"`
	Comment     JiraComments     `json:"comment,omitempty"`
	Security    *JiraSecurity    `json:"security,omitempty"`
	Attachment  []JiraAttachment `json:"attachment,omitempty"`
}

// JiraAttachment represents a file attached to a Jira issue.
type JiraAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Content  string `json:"content"` // download URL
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
	Body    ADFText  `json:"body"`
	Author  JiraUser `json:"author"`
	Created JiraTime `json:"created"`
	Updated JiraTime `json:"updated"`
}

// JiraSearchResponse represents the response from a Jira Cloud search
// (POST /rest/api/3/search/jql). Uses nextPageToken pagination.
type JiraSearchResponse struct {
	Issues        []JiraIssue `json:"issues"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
	IsLast        bool        `json:"isLast"`
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

// ADFText is a string type that transparently unmarshals from Jira
// Cloud's Atlassian Document Format (ADF). When the JSON value is a
// string (API v2 / tests), it stores it directly. When the value is
// an ADF object (API v3), it extracts the plain text content.
type ADFText string

func (a *ADFText) UnmarshalJSON(b []byte) error {
	// Null → empty string.
	if string(b) == "null" {
		*a = ""
		return nil
	}

	// If it's a JSON string, use it directly.
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = ADFText(s)
		return nil
	}

	// Otherwise it's an ADF object — extract text nodes.
	var doc adfNode
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("unmarshal ADF: %w", err)
	}

	*a = ADFText(extractADFText(&doc))
	return nil
}

// adfNode is the recursive structure of an Atlassian Document Format
// node. Only the fields needed for text extraction are represented.
type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text,omitempty"`
	Content []adfNode `json:"content,omitempty"`
}

// extractADFText walks an ADF tree and returns the concatenated text
// content. Paragraph and heading boundaries produce newlines;
// hardBreak nodes produce newlines.
func extractADFText(node *adfNode) string {
	if node == nil {
		return ""
	}

	if node.Type == "text" {
		return node.Text
	}

	if node.Type == "hardBreak" {
		return "\n"
	}

	var b strings.Builder
	for i, child := range node.Content {
		b.WriteString(extractADFText(&child))
		// Add newline after block-level nodes (paragraph, heading,
		// listItem, etc.) but not after the last one.
		if isBlockNode(child.Type) && i < len(node.Content)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func isBlockNode(nodeType string) bool {
	switch nodeType {
	case "paragraph", "heading", "blockquote", "codeBlock",
		"orderedList", "bulletList", "listItem", "rule",
		"table", "tableRow", "tableCell", "tableHeader",
		"mediaSingle", "panel":
		return true
	}
	return false
}

// TextToADF converts a plain text string to a minimal Atlassian
// Document Format object suitable for Jira Cloud API v3 write
// operations (comments, descriptions).
func TextToADF(text string) map[string]any {
	paragraphs := strings.Split(text, "\n")
	content := make([]map[string]any, 0, len(paragraphs))
	for _, line := range paragraphs {
		if line == "" {
			content = append(content, map[string]any{
				"type":    "paragraph",
				"content": []map[string]any{},
			})
			continue
		}
		content = append(content, map[string]any{
			"type": "paragraph",
			"content": []map[string]any{
				{"type": "text", "text": line},
			},
		})
	}
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": content,
	}
}
