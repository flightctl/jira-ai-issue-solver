package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v75/github"
	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
)

// ListCheckRunsForRef returns failed check runs for a commit ref (SHA,
// branch, or tag). The second return value is true when all check runs
// have completed — false means at least one is still pending or
// in-progress.
func (s *GitHubServiceImpl) ListCheckRunsForRef(
	owner, repo, ref string,
) ([]models.CheckRunFailure, bool, error) {
	client, err := s.ghClientForRepo(owner, repo)
	if err != nil {
		return nil, false, err
	}

	allCompleted := true
	var failures []models.CheckRunFailure

	opts := &github.ListCheckRunsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
		result, resp, err := client.Checks.ListCheckRunsForRef(
			ctx, owner, repo, ref, opts)
		cancel()
		if err != nil {
			return nil, false, fmt.Errorf("list check runs for %s: %w", ref, err)
		}

		for _, cr := range result.CheckRuns {
			if cr.GetStatus() != "completed" {
				allCompleted = false
				continue
			}

			conclusion := cr.GetConclusion()
			switch conclusion {
			case "failure", "timed_out", "cancelled":
			default:
				continue
			}

			f := models.CheckRunFailure{
				ID:         cr.GetID(),
				Name:       cr.GetName(),
				HTMLURL:    cr.GetHTMLURL(),
				Conclusion: conclusion,
			}
			if o := cr.GetOutput(); o != nil {
				f.Summary = o.GetSummary()
				f.Text = o.GetText()
			}
			failures = append(failures, f)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if failures == nil {
		failures = []models.CheckRunFailure{}
	}
	return failures, allCompleted, nil
}

// ListCheckRunAnnotations returns annotations for a single check run.
func (s *GitHubServiceImpl) ListCheckRunAnnotations(
	owner, repo string, checkRunID int64,
) ([]models.CheckAnnotation, error) {
	client, err := s.ghClientForRepo(owner, repo)
	if err != nil {
		return nil, err
	}

	var annotations []models.CheckAnnotation
	opts := &github.ListOptions{PerPage: 100}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
		anns, resp, err := client.Checks.ListCheckRunAnnotations(
			ctx, owner, repo, checkRunID, opts)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("list annotations for check run %d: %w", checkRunID, err)
		}

		for _, a := range anns {
			annotations = append(annotations, models.CheckAnnotation{
				Path:      a.GetPath(),
				StartLine: a.GetStartLine(),
				EndLine:   a.GetEndLine(),
				Level:     a.GetAnnotationLevel(),
				Message:   a.GetMessage(),
				Title:     a.GetTitle(),
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if annotations == nil {
		annotations = []models.CheckAnnotation{}
	}
	return annotations, nil
}

// GetFailedJobLogs returns truncated log output from failed workflow job
// steps, keyed by job name. Each job name maps to its failed steps with
// log output truncated to maxBytesPerStep from the tail.
func (s *GitHubServiceImpl) GetFailedJobLogs(
	owner, repo, headSHA string, maxBytesPerStep int,
) (map[string][]models.FailedStep, error) {
	client, err := s.ghClientForRepo(owner, repo)
	if err != nil {
		return nil, err
	}

	runsCtx, runsCancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer runsCancel()
	runs, _, err := client.Actions.ListRepositoryWorkflowRuns(
		runsCtx, owner, repo,
		&github.ListWorkflowRunsOptions{
			HeadSHA:     headSHA,
			ListOptions: github.ListOptions{PerPage: 50},
		})
	if err != nil {
		return nil, fmt.Errorf("list workflow runs for %s: %w", headSHA, err)
	}

	result := map[string][]models.FailedStep{}

	for _, run := range runs.WorkflowRuns {
		if run.GetStatus() != "completed" {
			continue
		}
		if run.GetConclusion() != "failure" {
			continue
		}

		jobsCtx, jobsCancel := context.WithTimeout(context.Background(), githubAPITimeout)
		jobs, _, err := client.Actions.ListWorkflowJobs(
			jobsCtx, owner, repo, run.GetID(),
			&github.ListWorkflowJobsOptions{
				Filter:      "latest",
				ListOptions: github.ListOptions{PerPage: 100},
			})
		if err != nil {
			jobsCancel()
			s.logger.Warn("Failed to list workflow jobs",
				zap.Int64("run_id", run.GetID()),
				zap.Error(err))
			continue
		}

		jobsCancel()

		for _, job := range jobs.Jobs {
			if job.GetConclusion() != "failure" {
				continue
			}

			failedStepNames := failedStepNamesFromJob(job)
			if len(failedStepNames) == 0 {
				continue
			}

			logText, err := s.fetchJobLog(client, owner, repo, job.GetID())
			if err != nil {
				s.logger.Warn("Failed to fetch job log",
					zap.String("job", job.GetName()),
					zap.Error(err))
				continue
			}

			steps := extractFailedStepLogs(logText, failedStepNames, job.GetName(), maxBytesPerStep)
			if len(steps) > 0 {
				result[job.GetName()] = steps
			}
		}
	}

	return result, nil
}

// ghClientForRepo returns the go-github client for the given repo.
func (s *GitHubServiceImpl) ghClientForRepo(owner, repo string) (*github.Client, error) {
	installationID, err := s.getInstallationIDForRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("get installation ID: %w", err)
	}
	client, err := s.getInstallationGitHubClient(installationID)
	if err != nil {
		return nil, fmt.Errorf("get GitHub client: %w", err)
	}
	return client, nil
}

// fetchJobLog downloads the plain-text log for a workflow job.
func (s *GitHubServiceImpl) fetchJobLog(
	client *github.Client, owner, repo string, jobID int64,
) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), githubAPITimeout)
	defer cancel()

	logURL, _, err := client.Actions.GetWorkflowJobLogs(
		ctx, owner, repo, jobID, 3)
	if err != nil {
		return "", fmt.Errorf("get job log URL: %w", err)
	}

	const logFetchTimeout = 60 * time.Second
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), logFetchTimeout)
	defer fetchCancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, logURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create log request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch job log: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch job log: unexpected status %d", resp.StatusCode)
	}

	const maxLogBytes = 512 * 1024 // 512KB safety limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLogBytes))
	if err != nil {
		return "", fmt.Errorf("read job log: %w", err)
	}

	return string(body), nil
}

// failedStepNamesFromJob returns the names of steps that failed.
func failedStepNamesFromJob(job *github.WorkflowJob) []string {
	if job == nil {
		return nil
	}
	var names []string
	for _, step := range job.Steps {
		if step.GetConclusion() == "failure" {
			names = append(names, step.GetName())
		}
	}
	return names
}

// extractFailedStepLogs parses a job log to extract the output for
// named failed steps, truncated to maxBytes from the tail.
//
// GitHub Actions job logs use two formats:
//   - Grouped: "##[group]Step Name" ... "##[endgroup]"
//   - Timestamped: "2024-01-15T10:30:00.1234567Z <content>"
//
// We parse for group markers first since they provide clear step
// boundaries. When a step name can't be matched to a group, the
// entire log tail is returned as a fallback.
func extractFailedStepLogs(
	logText string, failedStepNames []string, jobName string, maxBytes int,
) []models.FailedStep {
	stepSections := parseGroupSections(logText)

	steps := []models.FailedStep{}
	for _, name := range failedStepNames {
		section, ok := stepSections[name]
		if !ok {
			section = truncateTail(logText, maxBytes)
		} else {
			section = truncateTail(section, maxBytes)
		}
		if section == "" {
			continue
		}
		steps = append(steps, models.FailedStep{
			JobName:  jobName,
			StepName: name,
			Log:      section,
		})
	}
	return steps
}

// parseGroupSections splits a log by ##[group] / ##[endgroup] markers
// and returns a map of step name → log content.
func parseGroupSections(logText string) map[string]string {
	sections := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(logText))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var currentGroup string
	var buf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		stripped := stripTimestamp(line)

		if strings.HasPrefix(stripped, "##[group]") {
			if currentGroup != "" {
				sections[currentGroup] = buf.String()
			}
			currentGroup = strings.TrimPrefix(stripped, "##[group]")
			buf.Reset()
			continue
		}
		if stripped == "##[endgroup]" {
			if currentGroup != "" {
				sections[currentGroup] = buf.String()
				currentGroup = ""
				buf.Reset()
			}
			continue
		}
		if currentGroup != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	if currentGroup != "" {
		sections[currentGroup] = buf.String()
	}
	if scanner.Err() != nil {
		return map[string]string{}
	}

	return sections
}

// stripTimestamp removes the leading ISO-8601 timestamp from a GitHub
// Actions log line. Lines without timestamps are returned unchanged.
func stripTimestamp(line string) string {
	// Format: "2024-01-15T10:30:00.1234567Z rest of line"
	// The 'Z' position varies with fractional second precision.
	if len(line) >= 20 && line[4] == '-' && line[10] == 'T' {
		if idx := strings.IndexByte(line, 'Z'); idx > 18 && idx < 32 {
			rest := line[idx+1:]
			if len(rest) > 0 && rest[0] == ' ' {
				return rest[1:]
			}
			return rest
		}
	}
	return line
}

// truncateTail returns the last maxBytes bytes of s, splitting at a
// line boundary.
func truncateTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	truncated := s[len(s)-maxBytes:]
	if idx := strings.Index(truncated, "\n"); idx >= 0 {
		truncated = truncated[idx+1:]
	}
	return truncated
}
