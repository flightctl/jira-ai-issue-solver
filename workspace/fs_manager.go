package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// Compile-time check that FSManager implements Manager.
var _ Manager = (*FSManager)(nil)

// FSManager is a filesystem-backed workspace manager. Workspaces are stored
// as subdirectories of baseDir, named after the ticket key.
type FSManager struct {
	baseDir string
	cloner  Cloner
	logger  *zap.Logger
}

// NewFSManager creates a workspace manager that stores workspaces under
// baseDir. Returns an error if baseDir is empty, cloner is nil, or
// logger is nil.
func NewFSManager(baseDir string, cloner Cloner, logger *zap.Logger) (*FSManager, error) {
	if baseDir == "" {
		return nil, errors.New("workspace base directory must not be empty")
	}
	if cloner == nil {
		return nil, errors.New("cloner must not be nil")
	}
	if logger == nil {
		return nil, errors.New("logger must not be nil")
	}
	return &FSManager{
		baseDir: baseDir,
		cloner:  cloner,
		logger:  logger,
	}, nil
}

func (m *FSManager) Create(ticketKey, repoURL string) (string, error) {
	dir := m.workspacePath(ticketKey)

	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("workspace already exists for %s", ticketKey)
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return "", fmt.Errorf("create workspace base directory: %w", err)
	}

	if err := m.cloner.CloneRepository(repoURL, dir); err != nil {
		// Clean up the directory if clone left partial state.
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("clone repository for %s: %w", ticketKey, err)
	}

	m.logger.Info("Created workspace",
		zap.String("ticket", ticketKey),
		zap.String("path", dir))
	return dir, nil
}

func (m *FSManager) Find(ticketKey string) (string, bool) {
	dir := m.workspacePath(ticketKey)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

func (m *FSManager) FindOrCreate(ticketKey, repoURL string) (string, bool, error) {
	if dir, found := m.Find(ticketKey); found {
		m.logger.Debug("Reusing existing workspace",
			zap.String("ticket", ticketKey),
			zap.String("path", dir))
		return dir, true, nil
	}

	dir, err := m.Create(ticketKey, repoURL)
	if err != nil {
		return "", false, err
	}
	return dir, false, nil
}

func (m *FSManager) Cleanup(ticketKey string) error {
	dir := m.workspacePath(ticketKey)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove workspace for %s: %w", ticketKey, err)
	}
	m.logger.Info("Cleaned up workspace",
		zap.String("ticket", ticketKey),
		zap.String("path", dir))
	return nil
}

func (m *FSManager) CleanupStale(maxAge time.Duration) (int, error) {
	entries, err := m.listEntries()
	if err != nil {
		return 0, err
	}

	removed := 0
	now := time.Now()
	for _, entry := range entries {
		if now.Sub(entry.ModTime) <= maxAge {
			continue
		}
		if err := os.RemoveAll(entry.Path); err != nil {
			m.logger.Warn("Failed to remove stale workspace",
				zap.String("ticket", entry.TicketKey),
				zap.Error(err))
			continue
		}
		m.logger.Info("Removed stale workspace",
			zap.String("ticket", entry.TicketKey),
			zap.Duration("age", now.Sub(entry.ModTime)))
		removed++
	}
	return removed, nil
}

func (m *FSManager) CleanupByFilter(shouldRemove func(ticketKey string) bool) (int, error) {
	entries, err := m.listEntries()
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, entry := range entries {
		if !shouldRemove(entry.TicketKey) {
			continue
		}
		if err := os.RemoveAll(entry.Path); err != nil {
			m.logger.Warn("Failed to remove filtered workspace",
				zap.String("ticket", entry.TicketKey),
				zap.Error(err))
			continue
		}
		m.logger.Info("Removed workspace by filter",
			zap.String("ticket", entry.TicketKey))
		removed++
	}
	return removed, nil
}

func (m *FSManager) List() ([]Info, error) {
	return m.listEntries()
}

// workspacePath returns the absolute path for a ticket's workspace.
func (m *FSManager) workspacePath(ticketKey string) string {
	return filepath.Join(m.baseDir, ticketKey)
}

// listEntries reads the base directory and returns Info for each
// subdirectory. Non-directory entries are silently skipped. Returns an
// empty slice (not nil) when the base directory does not exist.
func (m *FSManager) listEntries() ([]Info, error) {
	dirEntries, err := os.ReadDir(m.baseDir)
	if errors.Is(err, os.ErrNotExist) {
		return []Info{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace base directory: %w", err)
	}

	entries := make([]Info, 0, len(dirEntries))
	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			m.logger.Warn("Failed to stat workspace directory",
				zap.String("name", de.Name()),
				zap.Error(err))
			continue
		}
		entries = append(entries, Info{
			TicketKey: de.Name(),
			Path:      filepath.Join(m.baseDir, de.Name()),
			ModTime:   fi.ModTime(),
		})
	}
	return entries, nil
}
