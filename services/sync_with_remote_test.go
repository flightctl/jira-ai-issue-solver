package services_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/services"
)

// newTestGitHubServiceForGit creates a GitHubService suitable for testing
// git-only operations (no GitHub API calls). It generates a temporary RSA
// key so the constructor's GitHub App transport initialization succeeds.
// When no executor is provided, it defaults to using /usr/bin/git to
// bypass any wrapper scripts on PATH.
func newTestGitHubServiceForGit(t *testing.T, executor ...models.CommandExecutor) *services.GitHubServiceImpl {
	t.Helper()

	keyFile := generateTempRSAKey(t)
	config := &models.Config{}
	config.GitHub.AppID = 1
	config.GitHub.PrivateKeyPath = keyFile

	if len(executor) == 0 {
		executor = []models.CommandExecutor{
			func(name string, args ...string) *exec.Cmd {
				if name == "git" {
					name = "/usr/bin/git"
				}
				return exec.Command(name, args...)
			},
		}
	}

	return services.NewGitHubService(config, zap.NewNop(), executor...)
}

func generateTempRSAKey(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	})

	keyFile := filepath.Join(t.TempDir(), "test-key.pem")
	if err := os.WriteFile(keyFile, pemData, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return keyFile
}

// --- Unit tests: verify correct git commands ---

func TestSyncWithRemote_ExecutesCorrectCommands(t *testing.T) {
	workDir := t.TempDir()

	var calls [][]string
	mockExecutor := func(name string, args ...string) *exec.Cmd {
		call := append([]string{name}, args...)
		calls = append(calls, call)
		return exec.Command("true")
	}

	svc := newTestGitHubServiceForGit(t, mockExecutor)

	err := svc.SyncWithRemote(workDir, "feature-branch", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 commands, got %d: %v", len(calls), calls)
	}

	// First command: git fetch origin
	assertCommand(t, calls[0], "git", "fetch", "origin")

	// Second command: git reset --hard origin/<branch>
	assertCommand(t, calls[1], "git", "reset", "--hard", "origin/feature-branch")
}

func TestSyncWithRemote_FetchFailure(t *testing.T) {
	workDir := t.TempDir()

	callCount := 0
	mockExecutor := func(name string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			// Fail the fetch.
			return exec.Command("false")
		}
		return exec.Command("true")
	}

	svc := newTestGitHubServiceForGit(t, mockExecutor)

	err := svc.SyncWithRemote(workDir, "main", nil)
	if err == nil {
		t.Fatal("expected error from failed fetch, got nil")
	}

	// Should not proceed to reset.
	if callCount != 1 {
		t.Errorf("expected 1 command (fetch only), got %d", callCount)
	}
}

func TestSyncWithRemote_ResetFailure(t *testing.T) {
	workDir := t.TempDir()

	callCount := 0
	mockExecutor := func(name string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 2 {
			// Fail the reset.
			return exec.Command("false")
		}
		return exec.Command("true")
	}

	svc := newTestGitHubServiceForGit(t, mockExecutor)

	err := svc.SyncWithRemote(workDir, "main", nil)
	if err == nil {
		t.Fatal("expected error from failed reset, got nil")
	}

	if callCount != 2 {
		t.Errorf("expected 2 commands, got %d", callCount)
	}
}

// --- Integration test: real git repo ---

func TestSyncWithRemote_PreservesUntrackedFiles(t *testing.T) {
	// Set up a bare repo as the "remote".
	bareDir := t.TempDir()
	gitInit(t, bareDir, "--bare")

	// Clone it to a workspace.
	workDir := filepath.Join(t.TempDir(), "workspace")
	gitRun(t, "", "clone", bareDir, workDir)

	// Create an initial commit so the branch exists.
	writeFile(t, filepath.Join(workDir, "tracked.txt"), "initial content")
	gitRun(t, workDir, "add", "tracked.txt")
	gitRun(t, workDir, "commit", "-m", "initial commit")
	gitRun(t, workDir, "push", "origin", "main")

	// Simulate an API-created commit by pushing directly to the bare repo
	// from a separate clone.
	apiClone := filepath.Join(t.TempDir(), "api-clone")
	gitRun(t, "", "clone", bareDir, apiClone)
	writeFile(t, filepath.Join(apiClone, "tracked.txt"), "updated by API")
	writeFile(t, filepath.Join(apiClone, "new-file.txt"), "created by API")
	gitRun(t, apiClone, "add", ".")
	gitRun(t, apiClone, "commit", "-m", "API commit")
	gitRun(t, apiClone, "push", "origin", "main")

	// Add untracked files to the workspace (simulating AI-generated artifacts).
	artifactDir := filepath.Join(workDir, ".ai-bot", "cache")
	if err := os.MkdirAll(artifactDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(artifactDir, "index.json"), `{"cached": true}`)
	writeFile(t, filepath.Join(workDir, "build-output.log"), "build log data")

	// Call SyncWithRemote.
	svc := newTestGitHubServiceForGit(t)
	if err := svc.SyncWithRemote(workDir, "main", nil); err != nil {
		t.Fatalf("SyncWithRemote: %v", err)
	}

	// Verify tracked files were updated.
	assertFileContent(t, filepath.Join(workDir, "tracked.txt"), "updated by API")
	assertFileContent(t, filepath.Join(workDir, "new-file.txt"), "created by API")

	// Verify untracked files were preserved.
	assertFileContent(t, filepath.Join(artifactDir, "index.json"), `{"cached": true}`)
	assertFileContent(t, filepath.Join(workDir, "build-output.log"), "build log data")
}

func TestSyncWithRemote_BranchDoesNotExist(t *testing.T) {
	bareDir := t.TempDir()
	gitInit(t, bareDir, "--bare")

	workDir := filepath.Join(t.TempDir(), "workspace")
	gitRun(t, "", "clone", bareDir, workDir)

	// Create an initial commit on main so there's a ref to work with.
	writeFile(t, filepath.Join(workDir, "file.txt"), "content")
	gitRun(t, workDir, "add", "file.txt")
	gitRun(t, workDir, "commit", "-m", "initial")
	gitRun(t, workDir, "push", "origin", "main")

	svc := newTestGitHubServiceForGit(t)

	// Try to sync with a non-existent branch.
	err := svc.SyncWithRemote(workDir, "nonexistent-branch", nil)
	if err == nil {
		t.Fatal("expected error for non-existent branch, got nil")
	}
}

// --- test helpers ---

func assertCommand(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("command = %v, want %v", got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("command[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
			return
		}
	}
}

func gitInit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"init"}, args...)
	cmd := exec.Command("/usr/bin/git", cmdArgs...)
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git init %v in %s: %v\n%s", args, dir, err, out)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("/usr/bin/git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = gitTestEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func gitTestEnv() []string {
	env := os.Environ()
	env = append(env,
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	return env
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test helper reads test-created files
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != expected {
		t.Errorf("%s content = %q, want %q", filepath.Base(path), string(data), expected)
	}
}
