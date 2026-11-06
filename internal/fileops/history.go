// Package fileops provides file operation utilities including history tracking,
// caching, detection, and context reading.
package fileops

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MaxSnapshots is the maximum number of snapshots retained before LRU eviction.
const MaxSnapshots = 100

// HistoryEntry records a single file modification event.
type HistoryEntry struct {
	Path          string    // Absolute path of the tracked file.
	ContentBefore []byte    // Content before the modification (nil if file was new).
	ContentAfter  []byte    // Content after the modification (nil if file was deleted).
	Timestamp     time.Time // When the modification was recorded.
	Tool          string    // Name of the tool that caused the change.
	Version       int       // Monotonically increasing version within this path.
}

// FileHistoryBackup is a snapshot entry for a single file.
type FileHistoryBackup struct {
	BackupFileName string    // Empty string means the file did not exist in this version.
	Version        int       // Version number of the backup.
	BackupTime     time.Time // When the backup was created.
	FileExisted    bool      // True if the file existed when backup was taken.
}

// FileHistorySnapshot captures the state of all tracked files at a point in time.
type FileHistorySnapshot struct {
	MessageID          string                        // Associated message ID.
	TrackedFileBackups map[string]*FileHistoryBackup // path → backup.
	Timestamp          time.Time
}

// DiffStats summarizes the changes between two snapshots.
type DiffStats struct {
	FilesChanged []string
	Insertions   int
	Deletions    int
}

// FileHistory tracks file modifications with backup and revert capabilities.
type FileHistory struct {
	mu               sync.RWMutex
	snapshots        []*FileHistorySnapshot
	trackedFiles     map[string]struct{}
	snapshotSequence int
	backupDir        string // Root directory for backup storage.
	workingDir       string // Working directory for relative path resolution.
	enabled          bool
}

// NewFileHistory creates a new FileHistory instance.
// backupDir is the directory where backup files are stored.
// workingDir is used to resolve relative file paths.
func NewFileHistory(backupDir, workingDir string) *FileHistory {
	return &FileHistory{
		snapshots:    make([]*FileHistorySnapshot, 0, MaxSnapshots),
		trackedFiles: make(map[string]struct{}),
		backupDir:    backupDir,
		workingDir:   workingDir,
		enabled:      true,
	}
}

// SetEnabled enables or disables file history tracking.
func (fh *FileHistory) SetEnabled(enabled bool) {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	fh.enabled = enabled
}

// IsEnabled returns whether file history tracking is active.
func (fh *FileHistory) IsEnabled() bool {
	fh.mu.RLock()
	defer fh.mu.RUnlock()
	return fh.enabled
}

// SnapshotSequence returns the monotonically increasing snapshot counter.
func (fh *FileHistory) SnapshotSequence() int {
	fh.mu.RLock()
	defer fh.mu.RUnlock()
	return fh.snapshotSequence
}

// RecordChange records a file modification by creating a backup before the change.
// This MUST be called before the file is actually modified.
func (fh *FileHistory) RecordChange(filePath, messageID, tool string) error {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	if !fh.enabled {
		return nil
	}

	trackingPath := fh.shortenPath(filePath)

	// Check if already tracked in the most recent snapshot.
	if len(fh.snapshots) > 0 {
		latest := fh.snapshots[len(fh.snapshots)-1]
		if _, exists := latest.TrackedFileBackups[trackingPath]; exists {
			return nil // Already tracked.
		}
	}

	// Create backup.
	backup, err := fh.createBackup(filePath, 1)
	if err != nil {
		return fmt.Errorf("fileHistory: backup failed for %s: %w", filePath, err)
	}

	// If no snapshot exists, create an initial one.
	if len(fh.snapshots) == 0 {
		fh.snapshots = append(fh.snapshots, &FileHistorySnapshot{
			MessageID:          messageID,
			TrackedFileBackups: make(map[string]*FileHistoryBackup),
			Timestamp:          time.Now(),
		})
	}

	// Add to the most recent snapshot.
	latest := fh.snapshots[len(fh.snapshots)-1]
	latest.TrackedFileBackups[trackingPath] = backup
	fh.trackedFiles[trackingPath] = struct{}{}

	return nil
}

// MakeSnapshot creates a new snapshot, backing up any modified tracked files.
func (fh *FileHistory) MakeSnapshot(messageID string) error {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	if !fh.enabled {
		return nil
	}

	trackedBackups := make(map[string]*FileHistoryBackup)

	// Back up all tracked files.
	if len(fh.snapshots) > 0 {
		mostRecent := fh.snapshots[len(fh.snapshots)-1]
		for trackingPath := range fh.trackedFiles {
			filePath := fh.expandPath(trackingPath)
			latestBackup := mostRecent.TrackedFileBackups[trackingPath]

			nextVersion := 1
			if latestBackup != nil {
				nextVersion = latestBackup.Version + 1
			}

			// Stat the file.
			info, statErr := os.Stat(filePath)
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					trackedBackups[trackingPath] = &FileHistoryBackup{
						BackupFileName: "",
						Version:        nextVersion,
						BackupTime:     time.Now(),
						FileExisted:    false,
					}
					continue
				}
				// Skip on other errors.
				continue
			}

			// Check if file changed since last backup.
			if latestBackup != nil && latestBackup.FileExisted {
				changed, err := fh.checkOriginChanged(filePath, latestBackup.BackupFileName, info)
				if err == nil && !changed {
					trackedBackups[trackingPath] = latestBackup
					continue
				}
			}

			// Create new backup.
			backup, err := fh.createBackup(filePath, nextVersion)
			if err != nil {
				continue // Skip on error.
			}
			trackedBackups[trackingPath] = backup
		}
	}

	// Inherit any tracked files that were added during async operations.
	if len(fh.snapshots) > 0 {
		lastSnapshot := fh.snapshots[len(fh.snapshots)-1]
		for trackingPath := range fh.trackedFiles {
			if _, exists := trackedBackups[trackingPath]; exists {
				continue
			}
			if inherited, ok := lastSnapshot.TrackedFileBackups[trackingPath]; ok {
				trackedBackups[trackingPath] = inherited
			}
		}
	}

	newSnapshot := &FileHistorySnapshot{
		MessageID:          messageID,
		TrackedFileBackups: trackedBackups,
		Timestamp:          time.Now(),
	}

	fh.snapshots = append(fh.snapshots, newSnapshot)
	fh.snapshotSequence++

	// Evict old snapshots.
	if len(fh.snapshots) > MaxSnapshots {
		fh.snapshots = fh.snapshots[len(fh.snapshots)-MaxSnapshots:]
	}

	return nil
}

// Revert restores the filesystem to a previous snapshot.
func (fh *FileHistory) Revert(messageID string) ([]string, error) {
	fh.mu.Lock()
	defer fh.mu.Unlock()

	if !fh.enabled {
		return nil, errors.New("fileHistory: disabled")
	}

	target := fh.findSnapshot(messageID)
	if target == nil {
		return nil, fmt.Errorf("fileHistory: snapshot %s not found", messageID)
	}

	return fh.applySnapshot(target)
}

// GetHistory returns all history entries for a given file path.
func (fh *FileHistory) GetHistory(filePath string) []*FileHistoryBackup {
	fh.mu.RLock()
	defer fh.mu.RUnlock()

	trackingPath := fh.shortenPath(filePath)
	var entries []*FileHistoryBackup

	for _, snap := range fh.snapshots {
		if backup, ok := snap.TrackedFileBackups[trackingPath]; ok {
			entries = append(entries, backup)
		}
	}
	return entries
}

// CanRestore returns whether a snapshot for the given messageID exists.
func (fh *FileHistory) CanRestore(messageID string) bool {
	fh.mu.RLock()
	defer fh.mu.RUnlock()
	return fh.findSnapshot(messageID) != nil
}

// GetDiffStats computes the diff stats for reverting to the given snapshot.
func (fh *FileHistory) GetDiffStats(messageID string) (*DiffStats, error) {
	fh.mu.RLock()
	defer fh.mu.RUnlock()

	target := fh.findSnapshot(messageID)
	if target == nil {
		return nil, nil
	}

	stats := &DiffStats{}
	for trackingPath := range fh.trackedFiles {
		filePath := fh.expandPath(trackingPath)
		backup := target.TrackedFileBackups[trackingPath]
		if backup == nil {
			// Look for the first version.
			backup = fh.findFirstVersion(trackingPath)
		}
		if backup == nil {
			continue
		}

		if !backup.FileExisted {
			// File should not exist. Check if it does now.
			if _, err := os.Stat(filePath); err == nil {
				stats.FilesChanged = append(stats.FilesChanged, filePath)
			}
			continue
		}

		changed, err := fh.checkOriginChanged(filePath, backup.BackupFileName, nil)
		if err != nil || changed {
			stats.FilesChanged = append(stats.FilesChanged, filePath)
		}
	}

	return stats, nil
}

// SnapshotCount returns the number of stored snapshots.
func (fh *FileHistory) SnapshotCount() int {
	fh.mu.RLock()
	defer fh.mu.RUnlock()
	return len(fh.snapshots)
}

// TrackedFileCount returns the number of tracked files.
func (fh *FileHistory) TrackedFileCount() int {
	fh.mu.RLock()
	defer fh.mu.RUnlock()
	return len(fh.trackedFiles)
}

// --- Internal helpers ---

func (fh *FileHistory) shortenPath(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(fh.workingDir, p)
	if err == nil && len(rel) < len(p) {
		return rel
	}
	return p
}

func (fh *FileHistory) expandPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(fh.workingDir, p)
}

func (fh *FileHistory) backupFileName(filePath string, version int) string {
	h := sha256.Sum256([]byte(filePath))
	return fmt.Sprintf("%s@v%d", hex.EncodeToString(h[:8]), version)
}

func (fh *FileHistory) resolveBackupPath(backupFileName string) string {
	return filepath.Join(fh.backupDir, backupFileName)
}

func (fh *FileHistory) createBackup(filePath string, version int) (*FileHistoryBackup, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &FileHistoryBackup{
				BackupFileName: "",
				Version:        version,
				BackupTime:     time.Now(),
				FileExisted:    false,
			}, nil
		}
		return nil, err
	}

	backupName := fh.backupFileName(filePath, version)
	backupPath := fh.resolveBackupPath(backupName)

	// Ensure directory exists.
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return nil, err
	}

	// Copy the file.
	if err := copyFileContents(filePath, backupPath); err != nil {
		return nil, err
	}

	// Preserve permissions.
	if err := os.Chmod(backupPath, info.Mode()); err != nil {
		// Non-fatal, continue.
		_ = err
	}

	return &FileHistoryBackup{
		BackupFileName: backupName,
		Version:        version,
		BackupTime:     time.Now(),
		FileExisted:    true,
	}, nil
}

func (fh *FileHistory) checkOriginChanged(filePath, backupFileName string, origInfo os.FileInfo) (bool, error) {
	if backupFileName == "" {
		_, err := os.Stat(filePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil // Both don't exist.
			}
			return true, err
		}
		return true, nil // File exists but backup says it shouldn't.
	}

	backupPath := fh.resolveBackupPath(backupFileName)

	if origInfo == nil {
		var err error
		origInfo, err = os.Stat(filePath)
		if err != nil {
			return true, err
		}
	}

	backupInfo, err := os.Stat(backupPath)
	if err != nil {
		return true, err
	}

	// Quick checks: mode and size.
	if origInfo.Mode() != backupInfo.Mode() || origInfo.Size() != backupInfo.Size() {
		return true, nil
	}

	// Optimization: if original is older than backup, content hasn't changed.
	if origInfo.ModTime().Before(backupInfo.ModTime()) {
		return false, nil
	}

	// Content comparison.
	return !filesEqual(filePath, backupPath), nil
}

func (fh *FileHistory) findSnapshot(messageID string) *FileHistorySnapshot {
	// Search backwards for the most recent match.
	for i := len(fh.snapshots) - 1; i >= 0; i-- {
		if fh.snapshots[i].MessageID == messageID {
			return fh.snapshots[i]
		}
	}
	return nil
}

func (fh *FileHistory) findFirstVersion(trackingPath string) *FileHistoryBackup {
	for _, snap := range fh.snapshots {
		if b, ok := snap.TrackedFileBackups[trackingPath]; ok && b.Version == 1 {
			return b
		}
	}
	return nil
}

func (fh *FileHistory) applySnapshot(target *FileHistorySnapshot) ([]string, error) {
	var filesChanged []string
	for trackingPath := range fh.trackedFiles {
		filePath := fh.expandPath(trackingPath)
		backup := target.TrackedFileBackups[trackingPath]
		if backup == nil {
			backup = fh.findFirstVersion(trackingPath)
		}
		if backup == nil {
			continue
		}

		if !backup.FileExisted {
			// File should not exist; delete if present.
			if err := os.Remove(filePath); err == nil {
				filesChanged = append(filesChanged, filePath)
			}
			continue
		}

		changed, _ := fh.checkOriginChanged(filePath, backup.BackupFileName, nil)
		if changed {
			if err := fh.restoreBackup(filePath, backup.BackupFileName); err == nil {
				filesChanged = append(filesChanged, filePath)
			}
		}
	}
	return filesChanged, nil
}

func (fh *FileHistory) restoreBackup(filePath, backupFileName string) error {
	backupPath := fh.resolveBackupPath(backupFileName)

	backupInfo, err := os.Stat(backupPath)
	if err != nil {
		return fmt.Errorf("backup not found: %s", backupPath)
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}

	if err := copyFileContents(backupPath, filePath); err != nil {
		return err
	}

	return os.Chmod(filePath, backupInfo.Mode())
}

// copyFileContents copies file from src to dst.
func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// filesEqual returns true if two files have identical content.
func filesEqual(a, b string) bool {
	fa, err := os.Open(a)
	if err != nil {
		return false
	}
	defer fa.Close()

	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()

	const bufSize = 32 * 1024
	bufA := make([]byte, bufSize)
	bufB := make([]byte, bufSize)

	for {
		nA, errA := fa.Read(bufA)
		nB, errB := fb.Read(bufB)

		if nA != nB || string(bufA[:nA]) != string(bufB[:nB]) {
			return false
		}
		if errA == io.EOF && errB == io.EOF {
			return true
		}
		if errA != nil || errB != nil {
			return false
		}
	}
}
