package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"jira-ai-issue-solver/workspace"
)

// stubCloner is a test double for workspace.Cloner.
type stubCloner struct {
	cloneFunc func(repoURL, directory string) error
}

func (s *stubCloner) CloneRepository(repoURL, directory string) error {
	if s.cloneFunc != nil {
		return s.cloneFunc(repoURL, directory)
	}
	// Simulate a successful clone by creating the directory and a .git marker.
	if err := os.MkdirAll(filepath.Join(directory, ".git"), 0o750); err != nil {
		return err
	}
	return nil
}

func newTestLogger() *zap.Logger {
	return zap.NewNop()
}

// --- NewFSManager ---

func TestNewFSManager_RejectsEmptyBaseDir(t *testing.T) {
	_, err := workspace.NewFSManager("", &stubCloner{}, newTestLogger())
	if err == nil {
		t.Fatal("expected error for empty base dir, got nil")
	}
}

func TestNewFSManager_RejectsNilCloner(t *testing.T) {
	_, err := workspace.NewFSManager("/tmp/ws", nil, newTestLogger())
	if err == nil {
		t.Fatal("expected error for nil cloner, got nil")
	}
}

func TestNewFSManager_RejectsNilLogger(t *testing.T) {
	_, err := workspace.NewFSManager("/tmp/ws", &stubCloner{}, nil)
	if err == nil {
		t.Fatal("expected error for nil logger, got nil")
	}
}

func TestNewFSManager_ValidInputs(t *testing.T) {
	mgr, err := workspace.NewFSManager("/tmp/ws", &stubCloner{}, newTestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

// --- Create ---

func TestCreate_ClonesAndReturnsPath(t *testing.T) {
	baseDir := t.TempDir()

	var clonedURL, clonedDir string
	cloner := &stubCloner{
		cloneFunc: func(repoURL, directory string) error {
			clonedURL = repoURL
			clonedDir = directory
			return os.MkdirAll(filepath.Join(directory, ".git"), 0o750)
		},
	}

	mgr := mustNewManager(t, baseDir, cloner)

	path, err := mgr.Create("PROJ-123", "https://github.com/org/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(baseDir, "PROJ-123")
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}
	if clonedURL != "https://github.com/org/repo.git" {
		t.Errorf("clone URL = %q, want repo URL", clonedURL)
	}
	if clonedDir != expected {
		t.Errorf("clone dir = %q, want %q", clonedDir, expected)
	}

	// Verify the directory exists.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("workspace directory does not exist: %v", err)
	}
}

func TestCreate_ErrorsWhenWorkspaceAlreadyExists(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	// Pre-create the directory.
	if err := os.MkdirAll(filepath.Join(baseDir, "PROJ-456"), 0o750); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Create("PROJ-456", "https://github.com/org/repo.git")
	if err == nil {
		t.Fatal("expected error for existing workspace, got nil")
	}
}

func TestCreate_CleansUpOnCloneFailure(t *testing.T) {
	baseDir := t.TempDir()

	cloneErr := errors.New("clone failed: network timeout")
	cloner := &stubCloner{
		cloneFunc: func(_, directory string) error {
			// Simulate partial clone that created the directory.
			_ = os.MkdirAll(directory, 0o750)
			return cloneErr
		},
	}

	mgr := mustNewManager(t, baseDir, cloner)

	_, err := mgr.Create("PROJ-789", "https://github.com/org/repo.git")
	if err == nil {
		t.Fatal("expected error from failed clone, got nil")
	}

	// Verify the directory was cleaned up.
	dir := filepath.Join(baseDir, "PROJ-789")
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("expected workspace directory to be removed after clone failure, stat error: %v", statErr)
	}
}

// --- Find ---

func TestFind_ReturnsTrueWhenWorkspaceExists(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	wsDir := filepath.Join(baseDir, "PROJ-100")
	if err := os.MkdirAll(wsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	path, found := mgr.Find("PROJ-100")
	if !found {
		t.Fatal("expected workspace to be found")
	}
	if path != wsDir {
		t.Errorf("path = %q, want %q", path, wsDir)
	}
}

func TestFind_ReturnsFalseWhenNotFound(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	path, found := mgr.Find("PROJ-999")
	if found {
		t.Fatal("expected workspace to not be found")
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
}

func TestFind_ReturnsFalseForFile(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	// Create a file (not directory) with the ticket key name.
	filePath := filepath.Join(baseDir, "PROJ-100")
	if err := os.WriteFile(filePath, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, found := mgr.Find("PROJ-100")
	if found {
		t.Fatal("expected file to not be treated as a workspace")
	}
}

// --- FindOrCreate ---

func TestFindOrCreate_ReusesExistingWorkspace(t *testing.T) {
	baseDir := t.TempDir()

	cloneCalled := false
	cloner := &stubCloner{
		cloneFunc: func(_, _ string) error {
			cloneCalled = true
			return nil
		},
	}

	mgr := mustNewManager(t, baseDir, cloner)

	// Pre-create the workspace directory.
	wsDir := filepath.Join(baseDir, "PROJ-200")
	if err := os.MkdirAll(wsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	path, reused, err := mgr.FindOrCreate("PROJ-200", "https://github.com/org/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reused {
		t.Error("expected reused = true")
	}
	if path != wsDir {
		t.Errorf("path = %q, want %q", path, wsDir)
	}
	if cloneCalled {
		t.Error("clone should not be called when workspace exists")
	}
}

func TestFindOrCreate_CreatesWhenNotFound(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	path, reused, err := mgr.FindOrCreate("PROJ-300", "https://github.com/org/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reused {
		t.Error("expected reused = false for new workspace")
	}

	expected := filepath.Join(baseDir, "PROJ-300")
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}
}

// --- Cleanup ---

func TestCleanup_RemovesExistingWorkspace(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	wsDir := filepath.Join(baseDir, "PROJ-400")
	if err := os.MkdirAll(filepath.Join(wsDir, "subdir"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Cleanup("PROJ-400"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(wsDir); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected workspace directory to be removed")
	}
}

func TestCleanup_NoErrorWhenNotExists(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	if err := mgr.Cleanup("PROJ-NONEXISTENT"); err != nil {
		t.Fatalf("unexpected error cleaning nonexistent workspace: %v", err)
	}
}

// --- CleanupStale ---

func TestCleanupStale_RemovesOldWorkspaces(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	// Create two workspaces.
	oldDir := filepath.Join(baseDir, "OLD-1")
	newDir := filepath.Join(baseDir, "NEW-1")
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Back-date the old workspace.
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	removed, err := mgr.CleanupStale(24 * time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	// Old should be gone.
	if _, err := os.Stat(oldDir); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected old workspace to be removed")
	}
	// New should remain.
	if _, err := os.Stat(newDir); err != nil {
		t.Error("expected new workspace to be preserved")
	}
}

func TestCleanupStale_PreservesRecentWorkspaces(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	wsDir := filepath.Join(baseDir, "RECENT-1")
	if err := os.MkdirAll(wsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	removed, err := mgr.CleanupStale(24 * time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestCleanupStale_EmptyBaseDir(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "nonexistent")
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	removed, err := mgr.CleanupStale(24 * time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// --- CleanupByFilter ---

func TestCleanupByFilter_RemovesMatchingWorkspaces(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	// Create workspaces.
	for _, key := range []string{"DONE-1", "ACTIVE-2", "DONE-3"} {
		if err := os.MkdirAll(filepath.Join(baseDir, key), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := mgr.CleanupByFilter(func(key string) bool {
		return key == "DONE-1" || key == "DONE-3"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}

	// DONE workspaces should be gone.
	for _, key := range []string{"DONE-1", "DONE-3"} {
		if _, err := os.Stat(filepath.Join(baseDir, key)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected %s to be removed", key)
		}
	}
	// ACTIVE should remain.
	if _, err := os.Stat(filepath.Join(baseDir, "ACTIVE-2")); err != nil {
		t.Error("expected ACTIVE-2 to be preserved")
	}
}

func TestCleanupByFilter_PreservesAllWhenFilterReturnsFalse(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	if err := os.MkdirAll(filepath.Join(baseDir, "KEEP-1"), 0o750); err != nil {
		t.Fatal(err)
	}

	removed, err := mgr.CleanupByFilter(func(_ string) bool { return false })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// --- List ---

func TestList_ReturnsAllWorkspaces(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	keys := []string{"PROJ-1", "PROJ-2", "PROJ-3"}
	for _, key := range keys {
		if err := os.MkdirAll(filepath.Join(baseDir, key), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	infos, err := mgr.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("len(infos) = %d, want 3", len(infos))
	}

	foundKeys := make(map[string]bool)
	for _, info := range infos {
		foundKeys[info.TicketKey] = true
		expectedPath := filepath.Join(baseDir, info.TicketKey)
		if info.Path != expectedPath {
			t.Errorf("info.Path = %q, want %q", info.Path, expectedPath)
		}
		if info.ModTime.IsZero() {
			t.Error("expected non-zero ModTime")
		}
	}
	for _, key := range keys {
		if !foundKeys[key] {
			t.Errorf("expected key %s in list results", key)
		}
	}
}

func TestList_SkipsNonDirectoryEntries(t *testing.T) {
	baseDir := t.TempDir()
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	// Create a directory and a file.
	if err := os.MkdirAll(filepath.Join(baseDir, "PROJ-1"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "some-file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	infos, err := mgr.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len(infos) = %d, want 1 (directory only)", len(infos))
	}
	if infos[0].TicketKey != "PROJ-1" {
		t.Errorf("TicketKey = %q, want PROJ-1", infos[0].TicketKey)
	}
}

func TestList_EmptyWhenBaseDirNotExists(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "nonexistent")
	cloner := &stubCloner{}
	mgr := mustNewManager(t, baseDir, cloner)

	infos, err := mgr.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if infos == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(infos) != 0 {
		t.Errorf("len(infos) = %d, want 0", len(infos))
	}
}

// --- helpers ---

func mustNewManager(t *testing.T, baseDir string, cloner workspace.Cloner) workspace.Manager {
	t.Helper()
	mgr, err := workspace.NewFSManager(baseDir, cloner, newTestLogger())
	if err != nil {
		t.Fatalf("NewFSManager: %v", err)
	}
	return mgr
}
