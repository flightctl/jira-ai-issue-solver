package services

import (
	"testing"
)

func TestParseGroupSections(t *testing.T) {
	tests := []struct {
		name     string
		logText  string
		wantKeys []string
		wantLen  map[string]bool // key exists check
	}{
		{
			name: "parses grouped steps",
			logText: "2024-01-15T10:30:00.1234567Z ##[group]Run tests\n" +
				"2024-01-15T10:30:01.1234567Z FAIL pkg/foo\n" +
				"2024-01-15T10:30:02.1234567Z ##[endgroup]\n" +
				"2024-01-15T10:30:03.1234567Z ##[group]Run lint\n" +
				"2024-01-15T10:30:04.1234567Z error: unused var\n" +
				"2024-01-15T10:30:05.1234567Z ##[endgroup]\n",
			wantKeys: []string{"Run tests", "Run lint"},
			wantLen:  map[string]bool{"Run tests": true, "Run lint": true},
		},
		{
			name:     "empty log",
			logText:  "",
			wantKeys: nil,
		},
		{
			name: "no groups",
			logText: "2024-01-15T10:30:00.1234567Z some output\n" +
				"2024-01-15T10:30:01.1234567Z more output\n",
			wantKeys: nil,
		},
		{
			name: "unclosed group",
			logText: "2024-01-15T10:30:00.1234567Z ##[group]Build\n" +
				"2024-01-15T10:30:01.1234567Z compiling...\n",
			wantKeys: []string{"Build"},
			wantLen:  map[string]bool{"Build": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sections := parseGroupSections(tt.logText)

			if tt.wantKeys == nil {
				if len(sections) != 0 {
					t.Fatalf("expected no sections, got %d", len(sections))
				}
				return
			}

			for _, key := range tt.wantKeys {
				if _, ok := sections[key]; !ok {
					t.Errorf("missing section %q", key)
				}
			}
		})
	}
}

func TestStripTimestamp(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "standard timestamp",
			line: "2024-01-15T10:30:00.1234567Z some output",
			want: "some output",
		},
		{
			name: "no timestamp",
			line: "plain line",
			want: "plain line",
		},
		{
			name: "group marker with timestamp",
			line: "2024-01-15T10:30:00.1234567Z ##[group]Run tests",
			want: "##[group]Run tests",
		},
		{
			name: "empty line",
			line: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTimestamp(tt.line)
			if got != tt.want {
				t.Errorf("stripTimestamp(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestTruncateTail(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{
			name:     "short input not truncated",
			input:    "hello",
			maxBytes: 100,
			want:     "hello",
		},
		{
			name:     "truncated at line boundary",
			input:    "line1\nline2\nline3\nline4\n",
			maxBytes: 12,
			want:     "line4\n",
		},
		{
			name:     "truncated mid-line snaps to next newline",
			input:    "aaaa\nbbbb\ncccc\n",
			maxBytes: 7,
			want:     "cccc\n",
		},
		{
			name:     "exact fit",
			input:    "abc",
			maxBytes: 3,
			want:     "abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateTail(tt.input, tt.maxBytes)
			if got != tt.want {
				t.Errorf("truncateTail(%q, %d) = %q, want %q",
					tt.input, tt.maxBytes, got, tt.want)
			}
		})
	}
}

func TestExtractFailedStepLogs(t *testing.T) {
	logText := "2024-01-15T10:30:00.1234567Z ##[group]Setup\n" +
		"2024-01-15T10:30:01.1234567Z ok\n" +
		"2024-01-15T10:30:02.1234567Z ##[endgroup]\n" +
		"2024-01-15T10:30:03.1234567Z ##[group]Run tests\n" +
		"2024-01-15T10:30:04.1234567Z FAIL TestFoo\n" +
		"2024-01-15T10:30:05.1234567Z exit code 1\n" +
		"2024-01-15T10:30:06.1234567Z ##[endgroup]\n"

	t.Run("extracts matching step", func(t *testing.T) {
		steps := extractFailedStepLogs(logText, []string{"Run tests"}, "build", 4096)
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}
		if steps[0].StepName != "Run tests" {
			t.Errorf("step name = %q, want %q", steps[0].StepName, "Run tests")
		}
		if steps[0].JobName != "build" {
			t.Errorf("job name = %q, want %q", steps[0].JobName, "build")
		}
		if steps[0].Log == "" {
			t.Error("log should not be empty")
		}
	})

	t.Run("unmatched step falls back to log tail", func(t *testing.T) {
		steps := extractFailedStepLogs(logText, []string{"Unknown Step"}, "build", 4096)
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}
		if steps[0].Log == "" {
			t.Error("fallback log should not be empty")
		}
	})

	t.Run("empty log returns no steps", func(t *testing.T) {
		steps := extractFailedStepLogs("", []string{"Run tests"}, "build", 4096)
		if len(steps) != 0 {
			t.Errorf("expected 0 steps, got %d", len(steps))
		}
	})
}

func TestFailedStepNamesFromJob_NilSteps(t *testing.T) {
	// Ensure nil Steps doesn't panic.
	names := failedStepNamesFromJob(nil)
	if len(names) != 0 {
		t.Errorf("expected empty slice, got %v", names)
	}
}
