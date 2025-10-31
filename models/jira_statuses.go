package models

// JiraTicketStatus represents the possible statuses for a Jira ticket
type JiraTicketStatus string

// Jira ticket statuses
const (
	// StatusInProgress indicates that the ticket is being worked on
	StatusInProgress JiraTicketStatus = "In Progress"

	// StatusInReview indicates that the ticket is ready for review
	StatusInReview JiraTicketStatus = "In Review"
)

// String returns the string representation of a JiraTicketStatus
func (s JiraTicketStatus) String() string {
	return string(s)
}
