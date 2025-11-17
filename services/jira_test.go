package services

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

// RoundTripFunc is a function type that implements http.RoundTripper
type RoundTripFunc func(req *http.Request) (*http.Response, error)

// RoundTrip executes the mock round trip
func (f RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// NewTestClient returns a mock http.Client that will execute the provided function instead of making a real HTTP request
func NewTestClient(fn RoundTripFunc) *http.Client {
	return &http.Client{
		Transport: fn,
	}
}

// instantSleep returns a closed channel immediately, simulating instant sleep for tests
func instantSleep(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time)
	close(ch)
	return ch
}

func newTestJiraConfig() *models.Config {
	return &models.Config{
		Jira: models.JiraConfig{
			BaseURL:  "https://jira.example.com",
			APIToken: "test-token",
		},
	}
}

// TestGetTicket tests the GetTicket method
func TestGetTicket(t *testing.T) {
	// Test cases
	testCases := []struct {
		name           string
		key            string
		mockResponse   *http.Response
		mockError      error
		expectedTicket *models.JiraTicketResponse
		expectedError  bool
	}{
		{
			name: "successful request",
			key:  "TEST-123",
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"id": "12345",
					"key": "TEST-123",
					"self": "https://jira.example.com/rest/api/2/issue/12345",
					"fields": {
						"summary": "Test ticket",
						"description": "This is a test ticket",
						"status": {
							"id": "1",
							"name": "Open"
						},
						"project": {
							"id": "10000",
							"key": "TEST",
							"name": "Test Project",
							"properties": {
								"ai.bot.github.repo": "https://github.com/example/repo.git"
							}
						},
						"labels": ["good-for-ai"],
						"created": "2023-01-01T00:00:00.000Z",
						"updated": "2023-01-02T00:00:00.000Z"
					}
				}`))),
			},
			mockError: nil,
			expectedTicket: &models.JiraTicketResponse{
				ID:   "12345",
				Key:  "TEST-123",
				Self: "https://jira.example.com/rest/api/2/issue/12345",
				Fields: models.JiraFields{
					Summary:     "Test ticket",
					Description: "This is a test ticket",
					Status: models.JiraStatus{
						ID:   "1",
						Name: "Open",
					},
					Project: models.JiraProject{
						ID:   "10000",
						Key:  "TEST",
						Name: "Test Project",
						Properties: map[string]string{
							"ai.bot.github.repo": "https://github.com/example/repo.git",
						},
					},
					Labels: []string{"good-for-ai"},
				},
			},
			expectedError: false,
		},
		{
			name: "error response",
			key:  "TEST-456",
			mockResponse: &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`))),
			},
			mockError:      nil,
			expectedTicket: nil,
			expectedError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock HTTP client
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				return tc.mockResponse, tc.mockError
			})

			// Create a JiraService with the mock client
			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			// Call the method being tested
			ticket, err := service.GetTicket(tc.key)

			// Check the results
			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tc.expectedTicket != nil {
				if ticket == nil {
					t.Errorf("Expected a ticket but got nil")
				} else {
					if ticket.ID != tc.expectedTicket.ID {
						t.Errorf("Expected ticket ID %s but got %s", tc.expectedTicket.ID, ticket.ID)
					}
					if ticket.Key != tc.expectedTicket.Key {
						t.Errorf("Expected ticket Key %s but got %s", tc.expectedTicket.Key, ticket.Key)
					}
					// Add more assertions for other fields as needed
				}
			}
		})
	}
}

// TestUpdateTicketLabels tests the UpdateTicketLabels method
func TestUpdateTicketLabels(t *testing.T) {
	// Test cases
	testCases := []struct {
		name          string
		key           string
		addLabels     []string
		removeLabels  []string
		mockResponses []*http.Response
		mockErrors    []error
		expectedError bool
	}{
		{
			name:         "successful update",
			key:          "TEST-123",
			addLabels:    []string{"ai-in-progress"},
			removeLabels: []string{"good-for-ai"},
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12345",
						"key": "TEST-123",
						"self": "https://jira.example.com/rest/api/2/issue/12345",
						"fields": {
							"labels": ["good-for-ai"]
						}
					}`))),
				},
				{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(bytes.NewReader([]byte(``))),
				},
			},
			mockErrors:    []error{nil, nil},
			expectedError: false,
		},
		{
			name:         "error getting ticket",
			key:          "TEST-456",
			addLabels:    []string{"ai-in-progress"},
			removeLabels: []string{"good-for-ai"},
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`))),
				},
			},
			mockErrors:    []error{nil},
			expectedError: true,
		},
		{
			name:         "error updating ticket",
			key:          "TEST-789",
			addLabels:    []string{"ai-in-progress"},
			removeLabels: []string{"good-for-ai"},
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12345",
						"key": "TEST-789",
						"self": "https://jira.example.com/rest/api/2/issue/12345",
						"fields": {
							"labels": ["good-for-ai"]
						}
					}`))),
				},
				{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Error updating labels"],"errors":{}}`))),
				},
			},
			mockErrors:    []error{nil, nil},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock HTTP client with a counter for multiple requests
			callCount := 0
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				if callCount >= len(tc.mockResponses) {
					t.Fatalf("Unexpected request: %s %s", req.Method, req.URL.String())
				}
				response := tc.mockResponses[callCount]
				err := tc.mockErrors[callCount]
				callCount++
				return response, err
			})

			// Create a JiraService with the mock client
			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			// Call the method being tested
			err := service.UpdateTicketLabels(tc.key, tc.addLabels, tc.removeLabels)

			// Check the results
			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// TestGetTicket_RateLimiting tests rate limiting retry logic
func TestGetTicket_RateLimiting(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		mockResponses []*http.Response
		expectedError bool
		expectedKey   string
	}{
		{
			name: "succeeds after transient rate limit with retry-after header",
			key:  "TEST-123",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Retry-After": []string{"0"},
					},
					Body: io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12345",
						"key": "TEST-123",
						"self": "https://jira.example.com/rest/api/2/issue/12345",
						"fields": {
							"summary": "Test ticket",
							"description": "This is a test ticket"
						}
					}`))),
				},
			},
			expectedError: false,
			expectedKey:   "TEST-123",
		},
		{
			name: "succeeds after transient rate limit without retry-after header",
			key:  "TEST-456",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					// No Retry-After header
					Body: io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12345",
						"key": "TEST-456",
						"self": "https://jira.example.com/rest/api/2/issue/12345",
						"fields": {
							"summary": "Test ticket",
							"description": "This is a test ticket"
						}
					}`))),
				},
			},
			expectedError: false,
			expectedKey:   "TEST-456",
		},
		{
			name: "fails when rate limit persists",
			key:  "TEST-789",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Retry-After": []string{"0"},
					},
					Body: io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Retry-After": []string{"0"},
					},
					Body: io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Retry-After": []string{"0"},
					},
					Body: io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callIndex := 0

			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				if callIndex >= len(tc.mockResponses) {
					// Return the last response if we run out
					return tc.mockResponses[len(tc.mockResponses)-1], nil
				}
				response := tc.mockResponses[callIndex]
				callIndex++
				return response, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			ticket, err := service.GetTicket(tc.key)

			// Verify contract behavior
			if tc.expectedError {
				if err == nil {
					t.Errorf("Expected an error but got nil")
				}
				if ticket != nil {
					t.Errorf("Expected nil ticket on error, got: %+v", ticket)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if ticket == nil {
					t.Errorf("Expected a ticket but got nil")
				} else if ticket.Key != tc.expectedKey {
					t.Errorf("Expected ticket key %s, got: %s", tc.expectedKey, ticket.Key)
				}
			}
		})
	}
}

// TestAddComment_RateLimiting tests that AddComment handles transient rate limit errors
func TestAddComment_RateLimiting(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		comment       string
		mockResponses []*http.Response
		expectedError bool
	}{
		{
			name:    "succeeds after transient rate limit with retry-after header",
			key:     "TEST-123",
			comment: "Test comment",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					Header: http.Header{
						"Retry-After": []string{"0"},
					},
					Body: io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusCreated,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"12345","body":"Test comment"}`))),
				},
			},
			expectedError: false,
		},
		{
			name:    "succeeds after transient rate limit without header",
			key:     "TEST-456",
			comment: "Another test comment",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusCreated,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"12346","body":"Another test comment"}`))),
				},
			},
			expectedError: false,
		},
		{
			name:    "fails when rate limit persists",
			key:     "TEST-789",
			comment: "Yet another comment",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
				{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Rate limit exceeded"],"errors":{}}`))),
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callIndex := 0

			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				if callIndex >= len(tc.mockResponses) {
					// Return the last response if we run out
					return tc.mockResponses[len(tc.mockResponses)-1], nil
				}
				response := tc.mockResponses[callIndex]
				callIndex++
				return response, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			err := service.AddComment(tc.key, tc.comment)

			// Verify contract behavior
			if tc.expectedError {
				if err == nil {
					t.Errorf("Expected an error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

// TestGetTicketWithComments tests fetching a ticket with comments
func TestGetTicketWithComments(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		mockResponse  *http.Response
		expectedError bool
	}{
		{
			name: "successful request with comments",
			key:  "TEST-123",
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"id": "12345",
					"key": "TEST-123",
					"fields": {
						"summary": "Test ticket",
						"comment": {
							"comments": [
								{"body": "First comment"},
								{"body": "Second comment"}
							]
						}
					}
				}`))),
			},
			expectedError: false,
		},
		{
			name: "error response",
			key:  "TEST-456",
			mockResponse: &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Issue not found"],"errors":{}}`))),
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				return tc.mockResponse, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			ticket, err := service.GetTicketWithComments(tc.key)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if ticket == nil {
					t.Errorf("Expected a ticket but got nil")
				}
			}
		})
	}
}

// TestGetTicketWithExpandedFields tests fetching a ticket with expanded fields
func TestGetTicketWithExpandedFields(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		mockResponse  *http.Response
		expectedError bool
	}{
		{
			name: "successful request",
			key:  "TEST-123",
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"fields": {
						"summary": "Test ticket",
						"customfield_10001": "Custom value"
					},
					"names": {
						"summary": "Summary",
						"customfield_10001": "Custom Field"
					}
				}`))),
			},
			expectedError: false,
		},
		{
			name: "error response",
			key:  "TEST-456",
			mockResponse: &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Issue not found"],"errors":{}}`))),
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				return tc.mockResponse, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			fields, names, err := service.GetTicketWithExpandedFields(tc.key)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if fields == nil || names == nil {
					t.Errorf("Expected fields and names but got nil")
				}
			}
		})
	}
}

// TestUpdateTicketStatus tests updating ticket status
func TestUpdateTicketStatus(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		status        string
		mockResponses []*http.Response
		expectedError bool
	}{
		{
			name:   "successful status update with HTTP 204",
			key:    "TEST-123",
			status: "In Progress",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"transitions": [
							{
								"id": "11",
								"name": "Start Progress",
								"to": {"name": "In Progress"}
							}
						]
					}`))),
				},
				{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(bytes.NewReader([]byte(``))),
				},
			},
			expectedError: false,
		},
		{
			name:   "successful status update with HTTP 200",
			key:    "TEST-124",
			status: "Done",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"transitions": [
							{
								"id": "31",
								"name": "Done",
								"to": {"name": "Done"}
							}
						]
					}`))),
				},
				{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
				},
			},
			expectedError: false,
		},
		{
			name:   "successful status update with HTTP 201",
			key:    "TEST-125",
			status: "Closed",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"transitions": [
							{
								"id": "41",
								"name": "Close",
								"to": {"name": "Closed"}
							}
						]
					}`))),
				},
				{
					StatusCode: http.StatusCreated,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
				},
			},
			expectedError: false,
		},
		{
			name:   "transition not found",
			key:    "TEST-456",
			status: "Invalid Status",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"transitions": [
							{
								"id": "11",
								"name": "Start Progress",
								"to": {"name": "In Progress"}
							}
						]
					}`))),
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callCount := 0
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				if callCount >= len(tc.mockResponses) {
					t.Fatalf("Unexpected request: %s %s", req.Method, req.URL.String())
				}
				response := tc.mockResponses[callCount]
				callCount++
				return response, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			err := service.UpdateTicketStatus(tc.key, tc.status)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// TestAddComment tests adding a comment to a ticket
func TestAddComment(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		comment       string
		mockResponse  *http.Response
		expectedError bool
	}{
		{
			name:    "successful comment with HTTP 200",
			key:     "TEST-123",
			comment: "This is a test comment",
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"12345","body":"This is a test comment"}`))),
			},
			expectedError: false,
		},
		{
			name:    "successful comment with HTTP 201 Created",
			key:     "TEST-124",
			comment: "Another test comment",
			mockResponse: &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"12346","body":"Another test comment"}`))),
			},
			expectedError: false,
		},
		{
			name:    "successful comment with HTTP 204 No Content",
			key:     "TEST-125",
			comment: "Yet another test comment",
			mockResponse: &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(bytes.NewReader([]byte(``))),
			},
			expectedError: false,
		},
		{
			name:    "error adding comment",
			key:     "TEST-456",
			comment: "This is a test comment",
			mockResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Invalid request"],"errors":{}}`))),
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				return tc.mockResponse, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			err := service.AddComment(tc.key, tc.comment)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// TestUpdateTicketField tests updating a ticket field
func TestUpdateTicketField(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		fieldID       string
		value         interface{}
		mockResponse  *http.Response
		expectedError bool
	}{
		{
			name:    "successful field update",
			key:     "TEST-123",
			fieldID: "customfield_10001",
			value:   "New value",
			mockResponse: &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(bytes.NewReader([]byte(``))),
			},
			expectedError: false,
		},
		{
			name:    "error updating field",
			key:     "TEST-456",
			fieldID: "customfield_10001",
			value:   "New value",
			mockResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Invalid field"],"errors":{}}`))),
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				return tc.mockResponse, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			err := service.UpdateTicketField(tc.key, tc.fieldID, tc.value)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// TestGetFieldIDByName tests resolving a field name to its ID
func TestGetFieldIDByName(t *testing.T) {
	testCases := []struct {
		name            string
		fieldName       string
		mockResponse    *http.Response
		expectedFieldID string
		expectedError   bool
	}{
		{
			name:      "field found",
			fieldName: "Custom Field",
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`[
					{"id":"customfield_10001","name":"Custom Field"},
					{"id":"customfield_10002","name":"Another Field"}
				]`))),
			},
			expectedFieldID: "customfield_10001",
			expectedError:   false,
		},
		{
			name:      "field not found",
			fieldName: "Nonexistent Field",
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`[
					{"id":"customfield_10001","name":"Custom Field"}
				]`))),
			},
			expectedFieldID: "",
			expectedError:   true,
		},
		{
			name:      "error response",
			fieldName: "Custom Field",
			mockResponse: &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Server error"],"errors":{}}`))),
			},
			expectedFieldID: "",
			expectedError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				return tc.mockResponse, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			fieldID, err := service.GetFieldIDByName(tc.fieldName)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if fieldID != tc.expectedFieldID {
					t.Errorf("Expected field ID %s but got %s", tc.expectedFieldID, fieldID)
				}
			}
		})
	}
}

// TestUpdateTicketFieldByName tests updating a field by name
func TestUpdateTicketFieldByName(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		fieldName     string
		value         interface{}
		mockResponses []*http.Response
		expectedError bool
	}{
		{
			name:      "successful update by name",
			key:       "TEST-123",
			fieldName: "Custom Field",
			value:     "New value",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`[
						{"id":"customfield_10001","name":"Custom Field"}
					]`))),
				},
				{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(bytes.NewReader([]byte(``))),
				},
			},
			expectedError: false,
		},
		{
			name:      "field name not found",
			key:       "TEST-456",
			fieldName: "Nonexistent Field",
			value:     "New value",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`[
						{"id":"customfield_10001","name":"Custom Field"}
					]`))),
				},
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callCount := 0
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				if callCount >= len(tc.mockResponses) {
					t.Fatalf("Unexpected request: %s %s", req.Method, req.URL.String())
				}
				response := tc.mockResponses[callCount]
				callCount++
				return response, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			err := service.UpdateTicketFieldByName(tc.key, tc.fieldName, tc.value)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// TestSearchTickets tests searching for tickets using JQL
func TestSearchTickets(t *testing.T) {
	testCases := []struct {
		name          string
		jql           string
		mockResponse  *http.Response
		expectedError bool
	}{
		{
			name: "successful search with HTTP 200",
			jql:  "project = TEST AND status = Open",
			mockResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"total": 2,
					"issues": [
						{
							"id": "12345",
							"key": "TEST-123",
							"fields": {"summary": "First ticket"}
						},
						{
							"id": "12346",
							"key": "TEST-124",
							"fields": {"summary": "Second ticket"}
						}
					]
				}`))),
			},
			expectedError: false,
		},
		{
			name: "successful search with HTTP 201",
			jql:  "project = TEST",
			mockResponse: &http.Response{
				StatusCode: http.StatusCreated,
				Body: io.NopCloser(bytes.NewReader([]byte(`{
					"total": 1,
					"issues": [
						{
							"id": "12345",
							"key": "TEST-123",
							"fields": {"summary": "Ticket"}
						}
					]
				}`))),
			},
			expectedError: false,
		},
		{
			name: "successful search with HTTP 204",
			jql:  "project = EMPTY",
			mockResponse: &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"total": 0, "issues": []}`))),
			},
			expectedError: false,
		},
		{
			name: "error search",
			jql:  "invalid JQL",
			mockResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"errorMessages":["Invalid JQL"],"errors":{}}`))),
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				return tc.mockResponse, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			result, err := service.SearchTickets(tc.jql)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if result == nil {
					t.Errorf("Expected search results but got nil")
				}
			}
		})
	}
}

// TestHasSecurityLevel tests checking if a ticket has a security level
func TestHasSecurityLevel(t *testing.T) {
	testCases := []struct {
		name           string
		key            string
		mockResponses  []*http.Response
		expectedHasSec bool
		expectedError  bool
	}{
		{
			name: "ticket with security level",
			key:  "TEST-123",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12345",
						"key": "TEST-123",
						"fields": {
							"security": {
								"id": "10001",
								"name": "Confidential"
							}
						}
					}`))),
				},
			},
			expectedHasSec: true,
			expectedError:  false,
		},
		{
			name: "ticket without security level",
			key:  "TEST-456",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12346",
						"key": "TEST-456",
						"fields": {}
					}`))),
				},
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"fields": {},
						"names": {}
					}`))),
				},
			},
			expectedHasSec: false,
			expectedError:  false,
		},
		{
			name: "ticket with None security level",
			key:  "TEST-789",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12347",
						"key": "TEST-789",
						"fields": {
							"security": {
								"id": "10000",
								"name": "None"
							}
						}
					}`))),
				},
			},
			expectedHasSec: false,
			expectedError:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callCount := 0
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				if callCount >= len(tc.mockResponses) {
					t.Fatalf("Unexpected request: %s %s", req.Method, req.URL.String())
				}
				response := tc.mockResponses[callCount]
				callCount++
				return response, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			hasSec, err := service.HasSecurityLevel(tc.key)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if hasSec != tc.expectedHasSec {
					t.Errorf("Expected HasSecurityLevel=%v but got %v", tc.expectedHasSec, hasSec)
				}
			}
		})
	}
}

// TestGetTicketSecurityLevel tests getting the security level of a ticket
func TestGetTicketSecurityLevel(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		mockResponses []*http.Response
		expectedSec   *models.JiraSecurity
		expectedError bool
	}{
		{
			name: "ticket with security in standard fields",
			key:  "TEST-123",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12345",
						"key": "TEST-123",
						"fields": {
							"security": {
								"id": "10001",
								"name": "Confidential",
								"description": "Confidential level"
							}
						}
					}`))),
				},
			},
			expectedSec: &models.JiraSecurity{
				ID:          "10001",
				Name:        "Confidential",
				Description: "Confidential level",
			},
			expectedError: false,
		},
		{
			name: "ticket with security in expanded fields",
			key:  "TEST-456",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12346",
						"key": "TEST-456",
						"fields": {}
					}`))),
				},
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"fields": {
							"security": {
								"id": "10002",
								"name": "Internal"
							}
						},
						"names": {
							"security": "Security Level"
						}
					}`))),
				},
			},
			expectedSec: &models.JiraSecurity{
				ID:   "10002",
				Name: "Internal",
			},
			expectedError: false,
		},
		{
			name: "ticket without security",
			key:  "TEST-789",
			mockResponses: []*http.Response{
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"id": "12347",
						"key": "TEST-789",
						"fields": {}
					}`))),
				},
				{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(bytes.NewReader([]byte(`{
						"fields": {},
						"names": {}
					}`))),
				},
			},
			expectedSec:   nil,
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callCount := 0
			mockClient := NewTestClient(func(req *http.Request) (*http.Response, error) {
				if callCount >= len(tc.mockResponses) {
					t.Fatalf("Unexpected request: %s %s", req.Method, req.URL.String())
				}
				response := tc.mockResponses[callCount]
				callCount++
				return response, nil
			})

			service := NewJiraServiceForTest(newTestJiraConfig(), mockClient, zap.NewNop(), instantSleep, execCommand)

			security, err := service.GetTicketSecurityLevel(tc.key)

			if tc.expectedError && err == nil {
				t.Errorf("Expected an error but got nil")
			}
			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if tc.expectedSec == nil && security != nil {
					t.Errorf("Expected nil security but got %v", security)
				}
				if tc.expectedSec != nil {
					if security == nil {
						t.Errorf("Expected security but got nil")
					} else {
						if security.ID != tc.expectedSec.ID {
							t.Errorf("Expected security ID %s but got %s", tc.expectedSec.ID, security.ID)
						}
						if security.Name != tc.expectedSec.Name {
							t.Errorf("Expected security name %s but got %s", tc.expectedSec.Name, security.Name)
						}
					}
				}
			}
		})
	}
}
