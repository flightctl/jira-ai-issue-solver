package models

import (
	"encoding/json"
	"testing"
)

func TestADFText_UnmarshalString(t *testing.T) {
	input := `"plain text description"`
	var a ADFText
	if err := json.Unmarshal([]byte(input), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(a) != "plain text description" {
		t.Errorf("got %q, want %q", a, "plain text description")
	}
}

func TestADFText_UnmarshalNull(t *testing.T) {
	input := `null`
	var a ADFText
	if err := json.Unmarshal([]byte(input), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(a) != "" {
		t.Errorf("got %q, want empty", a)
	}
}

func TestADFText_UnmarshalADF(t *testing.T) {
	input := `{
		"type": "doc",
		"version": 1,
		"content": [
			{
				"type": "paragraph",
				"content": [
					{"type": "text", "text": "First paragraph."}
				]
			},
			{
				"type": "paragraph",
				"content": [
					{"type": "text", "text": "Second paragraph."}
				]
			}
		]
	}`

	var a ADFText
	if err := json.Unmarshal([]byte(input), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "First paragraph.\nSecond paragraph."
	if string(a) != want {
		t.Errorf("got %q, want %q", a, want)
	}
}

func TestADFText_UnmarshalADF_WithHardBreak(t *testing.T) {
	input := `{
		"type": "doc",
		"version": 1,
		"content": [
			{
				"type": "paragraph",
				"content": [
					{"type": "text", "text": "line one"},
					{"type": "hardBreak"},
					{"type": "text", "text": "line two"}
				]
			}
		]
	}`

	var a ADFText
	if err := json.Unmarshal([]byte(input), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "line one\nline two"
	if string(a) != want {
		t.Errorf("got %q, want %q", a, want)
	}
}

func TestADFText_UnmarshalADF_NestedLists(t *testing.T) {
	input := `{
		"type": "doc",
		"version": 1,
		"content": [
			{
				"type": "bulletList",
				"content": [
					{
						"type": "listItem",
						"content": [
							{
								"type": "paragraph",
								"content": [{"type": "text", "text": "item one"}]
							}
						]
					},
					{
						"type": "listItem",
						"content": [
							{
								"type": "paragraph",
								"content": [{"type": "text", "text": "item two"}]
							}
						]
					}
				]
			}
		]
	}`

	var a ADFText
	if err := json.Unmarshal([]byte(input), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "item one\nitem two"
	if string(a) != want {
		t.Errorf("got %q, want %q", a, want)
	}
}

func TestADFText_InJiraFields(t *testing.T) {
	input := `{
		"summary": "Fix the bug",
		"description": {
			"type": "doc",
			"version": 1,
			"content": [
				{
					"type": "paragraph",
					"content": [{"type": "text", "text": "Steps to reproduce the issue."}]
				}
			]
		},
		"status": {"id": "1", "name": "Open"},
		"issuetype": {"id": "1", "name": "Bug"},
		"project": {"id": "1", "key": "PROJ", "name": "Project"}
	}`

	var fields JiraFields
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fields.Summary != "Fix the bug" {
		t.Errorf("Summary = %q, want %q", fields.Summary, "Fix the bug")
	}
	if string(fields.Description) != "Steps to reproduce the issue." {
		t.Errorf("Description = %q, want %q", fields.Description, "Steps to reproduce the issue.")
	}
}

func TestTextToADF(t *testing.T) {
	result := TextToADF("Hello world")

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Round-trip: unmarshal the ADF back through ADFText.
	var a ADFText
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatalf("failed to unmarshal ADF: %v", err)
	}

	if string(a) != "Hello world" {
		t.Errorf("round-trip got %q, want %q", a, "Hello world")
	}
}

func TestTextToADF_Multiline(t *testing.T) {
	result := TextToADF("line one\nline two\nline three")

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var a ADFText
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatalf("failed to unmarshal ADF: %v", err)
	}

	want := "line one\nline two\nline three"
	if string(a) != want {
		t.Errorf("round-trip got %q, want %q", a, want)
	}
}
