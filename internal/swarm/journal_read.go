package swarm

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sidecar indexes are named instance-*.jsonl.idx so filepath.Glob of
// instance-*.jsonl does not see them. They are a cache of line offsets.
// The JSONL bytes remain the only source of truth.

type indexEntry struct {
	Off  int64  `json:"off"`
	Len  int64  `json:"len"`
	End  int64  `json:"end"`
	Kind string `json:"kind,omitempty"`
	Team string `json:"team,omitempty"`
	Run  string `json:"run,omitempty"`
	At   int64  `json:"at,omitempty"`
	Sum  uint64 `json:"sum"`
	Ix   uint64 `json:"ix"`
	Gap  bool   `json:"gap,omitempty"`
}

type journalFile struct {
	path        string
	live        bool
	lockMissing bool
	truncated   bool
	size        int64
	info        os.FileInfo
	entries     []indexEntry
}

type verifiedJournal struct {
	size      int64
	info      os.FileInfo
	entries   []indexEntry
	truncated bool
}

type locatedRecord struct {
	record journalRecord
	file   string
	off    int64
}

var errIndexMismatch = errors.New("team journal index mismatch")

func (j *TeamJournal) warnIndex(err error) {
	if j == nil || err == nil {
		return
	}
	logger := j.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("team journal index update failed", "error", err)
}

func (j *TeamJournal) tryRetentionLock() (func(), bool, error) {
	release := func() {}
	if j == nil {
		return release, false, errors.New("team journal is closed")
	}
	file, err := os.OpenFile(filepath.Join(j.dir, "retention.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return release, false, fmt.Errorf("opening team journal retention lock: %w", err)
	}
	if err := lockTeamJournalFile(file); err != nil {
		_ = file.Close()
		if teamJournalLockBusy(err) {
			return release, false, nil
		}
		return release, false, fmt.Errorf("locking team journal retention: %w", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			_ = unlockTeamJournalFile(file)
			_ = file.Close()
		})
	}
	return release, true, nil
}

func journalIndexPath(jsonlPath string) string {
	return jsonlPath + ".idx"
}

func lineSum(line []byte) uint64 {
	sum := fnv.New64a()
	_, _ = sum.Write(line)
	return sum.Sum64()
}

func indexMAC(entry indexEntry) uint64 {
	sum := fnv.New64a()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(entry.Off))
	_, _ = sum.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(entry.Len))
	_, _ = sum.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(entry.End))
	_, _ = sum.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], entry.Sum)
	_, _ = sum.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(entry.At))
	_, _ = sum.Write(buf[:])
	_, _ = sum.Write([]byte(entry.Kind))
	_, _ = sum.Write([]byte{0})
	_, _ = sum.Write([]byte(entry.Team))
	_, _ = sum.Write([]byte{0})
	_, _ = sum.Write([]byte(entry.Run))
	_, _ = sum.Write([]byte{0})
	if entry.Gap {
		_, _ = sum.Write([]byte{1})
	} else {
		_, _ = sum.Write([]byte{0})
	}
	return sum.Sum64()
}

func stampEntry(entry indexEntry) indexEntry {
	entry.Ix = indexMAC(entry)
	return entry
}

func (j *TeamJournal) listFiles(trust bool, publishDead bool) ([]journalFile, error) {
	paths, err := filepath.Glob(filepath.Join(j.dir, "instance-*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("listing team journals: %w", err)
	}
	files := make([]journalFile, 0, len(paths))
	for _, path := range paths {
		file, ok, err := j.inspectFile(path, trust, publishDead)
		if err != nil {
			return nil, err
		}
		if ok {
			files = append(files, file)
		}
	}
	return files, nil
}

func (j *TeamJournal) inspectFile(path string, trust bool, publishDead bool) (meta journalFile, ok bool, err error) {
	handle, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return journalFile{}, false, nil
	}
	if err != nil {
		return journalFile{}, false, fmt.Errorf("opening team journal: %w", err)
	}
	defer func() {
		if closeErr := handle.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("closing team journal: %w", closeErr)
			ok = false
		}
	}()
	info, err := handle.Stat()
	if err != nil {
		return journalFile{}, false, fmt.Errorf("opening team journal: %w", err)
	}
	meta = journalFile{path: path, size: info.Size(), info: info}
	if path == j.path {
		meta.live = true
	} else if err := j.markForeignLiveness(&meta); err != nil {
		return journalFile{}, false, err
	}
	if trust {
		if verified, hit := j.verified[path]; hit && verified.info != nil && os.SameFile(verified.info, info) {
			// The owner only appends. Equal size and mtime means the validated
			// in-memory metadata still describes this inode. Our own writer also
			// updates its cache immediately after each append.
			if verified.size == meta.size && verified.info.ModTime().Equal(info.ModTime()) {
				meta.entries = verified.entries
				meta.truncated = verified.truncated
				return meta, true, nil
			}
			// A live foreign owner can only append to its file. Scan just the
			// new complete lines, retaining the previously validated prefix.
			if meta.live && !verified.truncated && verified.size < meta.size && entriesCover(verified.entries, verified.size) {
				tail, end, scanErr := scanJournal(handle, verified.size)
				if scanErr != nil {
					return journalFile{}, false, scanErr
				}
				meta.entries = append(verified.entries, tail...)
				meta.truncated = end < meta.size
				j.remember(meta)
				return meta, true, nil
			}
		}
		if entries, covered, ok := trustedPrefix(handle, path, meta.size); ok {
			meta.entries = entries
			if covered < meta.size {
				tail, end, scanErr := scanJournal(handle, covered)
				if scanErr != nil {
					return journalFile{}, false, scanErr
				}
				if len(tail) > 0 {
					meta.entries = append(append([]indexEntry{}, entries...), tail...)
					j.maybePublish(meta, meta.entries, publishDead)
				}
				meta.truncated = end < meta.size
			}
			j.remember(meta)
			return meta, true, nil
		}
	}
	entries, end, err := scanJournal(handle, 0)
	if err != nil {
		return journalFile{}, false, err
	}
	meta.entries = entries
	meta.truncated = end < meta.size
	j.remember(meta)
	j.maybePublish(meta, entries, publishDead)
	return meta, true, nil
}

func (j *TeamJournal) markForeignLiveness(meta *journalFile) error {
	lockPath := strings.TrimSuffix(meta.path, ".jsonl") + ".lock"
	live, missing, err := teamJournalLockState(lockPath)
	if err != nil {
		return fmt.Errorf("checking team journal owner: %w", err)
	}
	if missing {
		// A missing lock is not proof the owner exited. The process may still
		// hold the flock on an unlinked inode, so prune must not rewrite the JSONL.
		meta.lockMissing = true
		return nil
	}
	meta.live = live
	return nil
}

// teamJournalLockState reports whether the lock file is held. missing is true
// only when the open itself returns ENOENT. teamJournalOwnerLive keeps the
// older (false, nil) result for that case.
func teamJournalLockState(path string) (live bool, missing bool, err error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	defer file.Close()
	if err := lockTeamJournalFile(file); err != nil {
		if teamJournalLockBusy(err) {
			return true, false, nil
		}
		return false, false, err
	}
	if err := unlockTeamJournalFile(file); err != nil {
		return false, false, err
	}
	return false, false, nil
}

func indexExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func (j *TeamJournal) remember(meta journalFile) {
	if j.verified == nil {
		j.verified = make(map[string]verifiedJournal)
	}
	j.verified[meta.path] = verifiedJournal{
		size:      meta.size,
		info:      meta.info,
		entries:   meta.entries,
		truncated: meta.truncated,
	}
}

func (j *TeamJournal) maybePublish(meta journalFile, entries []indexEntry, publishDead bool) {
	own := meta.path == j.path
	if own {
		if err := publishJournalIndex(meta.path, entries); err != nil {
			j.warnIndex(err)
			j.indexReady = false
		} else if end, ok := indexCoverage(entries); ok {
			j.indexReady = true
			j.indexEnd = end
		}
		return
	}
	if meta.live || meta.lockMissing || !publishDead {
		return
	}
	if err := publishJournalIndex(meta.path, entries); err != nil {
		j.warnIndex(err)
	}
}

func trustedPrefix(file *os.File, path string, size int64) ([]indexEntry, int64, bool) {
	entries, ok := loadIndex(journalIndexPath(path))
	if !ok {
		return nil, 0, false
	}
	covered, ok := indexCoverage(entries)
	if !ok || covered > size {
		return nil, 0, false
	}
	if !sumsMatch(file, entries) {
		return nil, 0, false
	}
	return entries, covered, true
}

func indexCoverage(entries []indexEntry) (int64, bool) {
	var expect int64
	for _, entry := range entries {
		if entry.Ix != indexMAC(entry) || entry.Off != expect || entry.Len <= 0 || entry.End != entry.Off+entry.Len {
			return 0, false
		}
		if entry.Len > maxJournalRecord {
			return 0, false
		}
		expect = entry.End
	}
	return expect, true
}

func sumsMatch(file *os.File, entries []indexEntry) bool {
	for _, entry := range entries {
		if !entryMatchesJSONL(file, entry) {
			return false
		}
	}
	return true
}

func entryMatchesJSONL(file *os.File, entry indexEntry) bool {
	buf, err := readEntryBytes(file, entry)
	if err != nil {
		return false
	}
	if entry.Gap {
		return len(bytes.TrimSpace(buf)) == 0 && entry.Kind == "" && entry.Team == "" && entry.Run == ""
	}
	var event journalEvent
	if err := json.Unmarshal(bytes.TrimSpace(buf), &event); err != nil {
		return false
	}
	return event.Version == teamJournalVersion && event.Kind == entry.Kind &&
		event.Team == entry.Team && event.RunID == entry.Run && event.At.UnixNano() == entry.At
}

func loadIndex(path string) ([]indexEntry, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64<<10)
	var entries []indexEntry
	for {
		line, readErr := reader.ReadSlice('\n')
		if errors.Is(readErr, io.EOF) {
			// A torn final line must not be treated as a clean prefix.
			// Appending after it would glue the next record onto the torn bytes.
			if len(line) > 0 {
				return nil, false
			}
			return entries, true
		}
		if readErr != nil {
			return nil, false
		}
		line = bytes.TrimSpace(append([]byte(nil), line...))
		if len(line) == 0 {
			continue
		}
		var entry indexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, false
		}
		entries = append(entries, entry)
	}
}

func scanJournal(file *os.File, start int64) ([]indexEntry, int64, error) {
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("seeking team journal: %w", err)
	}
	reader := bufio.NewReaderSize(file, maxJournalRecord)
	pos := start
	var entries []indexEntry
	for {
		line, readErr := reader.ReadSlice('\n')
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("reading bounded team journal record: %w", readErr)
		}
		raw := append([]byte(nil), line...)
		startOff := pos
		pos += int64(len(raw))
		if len(bytes.TrimSpace(raw)) == 0 {
			entries = append(entries, stampEntry(indexEntry{
				Off: startOff, Len: int64(len(raw)), End: pos, Gap: true, Sum: lineSum(raw),
			}))
			continue
		}
		var event journalEvent
		if err := json.Unmarshal(bytes.TrimSpace(raw), &event); err != nil {
			return nil, 0, fmt.Errorf("decoding team journal: %w", err)
		}
		if event.Version != teamJournalVersion {
			return nil, 0, fmt.Errorf("unsupported team journal version %d", event.Version)
		}
		entries = append(entries, stampEntry(indexEntry{
			Off: startOff, Len: int64(len(raw)), End: pos,
			Kind: event.Kind, Team: event.Team, Run: event.RunID,
			At: event.At.UnixNano(), Sum: lineSum(raw),
		}))
	}
	return entries, pos, nil
}

func publishJournalIndex(jsonlPath string, entries []indexEntry) error {
	if _, ok := indexCoverage(entries); !ok && len(entries) > 0 {
		return errors.New("refusing to publish a gapped team journal index")
	}
	dir := filepath.Dir(jsonlPath)
	tmp, err := os.CreateTemp(dir, ".journal-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := tmp.Write(append(data, '\n')); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceJournalFile(tmpName, journalIndexPath(jsonlPath)); err != nil {
		return err
	}
	success = true
	return syncTeamJournalDir(dir)
}

func (j *TeamJournal) noteIndexAppend(off int64, line []byte, event journalEvent) {
	entry := stampEntry(indexEntry{
		Off: off, Len: int64(len(line)), End: off + int64(len(line)),
		Kind: event.Kind, Team: event.Team, Run: event.RunID,
		At: event.At.UnixNano(), Sum: lineSum(line),
	})
	if j.indexReady {
		if err := j.appendIndexLine(off, entry); err != nil {
			j.warnIndex(err)
			j.indexReady = false
		}
	}
	if j.verified == nil {
		j.verified = make(map[string]verifiedJournal)
	}
	current := j.verified[j.path]
	if current.truncated || current.size != off {
		// The cached prefix does not meet this line. Extending it would hide
		// a torn tail that the next line-oriented read must still reject.
		delete(j.verified, j.path)
		return
	}
	current.entries = append(current.entries, entry)
	current.size = entry.End
	current.truncated = false
	if info, err := os.Stat(j.path); err == nil {
		current.info = info
	} else {
		delete(j.verified, j.path)
		return
	}
	j.verified[j.path] = current
}

func (j *TeamJournal) appendIndexLine(off int64, entry indexEntry) error {
	if !j.indexReady || j.indexEnd != off {
		return errIndexMismatch
	}
	path := journalIndexPath(j.path)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	j.indexEnd = entry.End
	return nil
}

func (j *TeamJournal) latestRecordsLocked() ([]journalRecord, error) {
	located, err := j.withLocated(func(files []journalFile) (func(indexEntry) bool, error) {
		return j.latestSelector(files)
	})
	if err != nil {
		return nil, err
	}
	records := make([]journalRecord, len(located))
	for i := range located {
		records[i] = located[i].record
	}
	return records, nil
}

func (j *TeamJournal) latestSelector(files []journalFile) (func(indexEntry) bool, error) {
	latest := make(map[string]runRef)
	for _, file := range files {
		for _, entry := range file.entries {
			if entry.Gap || entry.Kind != "created" {
				continue
			}
			ref := runRef{team: entry.Team, run: entry.Run, file: filepath.Base(file.path), off: entry.Off, at: time.Unix(0, entry.At)}
			if old, ok := latest[entry.Team]; !ok || newerRun(ref, old) {
				latest[entry.Team] = ref
			}
		}
	}
	return func(entry indexEntry) bool {
		ref, ok := latest[entry.Team]
		return ok && entry.Run == ref.run
	}, nil
}

func (j *TeamJournal) withLocated(selectFn func([]journalFile) (func(indexEntry) bool, error)) ([]locatedRecord, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt == 1 {
			j.verified = nil
		}
		files, err := j.listFiles(attempt == 0, false)
		if err != nil {
			return nil, err
		}
		want, err := selectFn(files)
		if errors.Is(err, errIndexMismatch) {
			last = err
			continue
		}
		if err != nil {
			return nil, err
		}
		records, err := j.locatedRecords(files, want)
		if err == nil {
			return records, nil
		}
		if !errors.Is(err, errIndexMismatch) {
			return nil, err
		}
		last = err
	}
	return nil, fmt.Errorf("reading team journal: %w", last)
}

func (j *TeamJournal) locatedRecords(files []journalFile, want func(indexEntry) bool) ([]locatedRecord, error) {
	if want == nil {
		return nil, nil
	}
	var records []locatedRecord
	for i := range files {
		meta := &files[i]
		var wanted []indexEntry
		for _, entry := range meta.entries {
			if entry.Gap || !want(entry) {
				continue
			}
			wanted = append(wanted, entry)
		}
		if len(wanted) == 0 {
			continue
		}
		handle, err := os.Open(meta.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, errIndexMismatch
		}
		if err != nil {
			return nil, fmt.Errorf("opening team journal: %w", err)
		}
		base := filepath.Base(meta.path)
		for _, entry := range wanted {
			record, readErr := readIndexedRecord(handle, entry, meta.live)
			if readErr != nil {
				_ = handle.Close()
				return nil, readErr
			}
			record.self = meta.path == j.path
			records = append(records, locatedRecord{record: record, file: base, off: entry.Off})
		}
		if err := handle.Close(); err != nil {
			return nil, fmt.Errorf("closing team journal: %w", err)
		}
	}
	return records, nil
}

func readEntryBytes(file *os.File, entry indexEntry) ([]byte, error) {
	if entry.Len <= 0 || entry.Len > maxJournalRecord || entry.Off < 0 {
		return nil, errIndexMismatch
	}
	buf := make([]byte, entry.Len)
	n, err := file.ReadAt(buf, entry.Off)
	if int64(n) != entry.Len || (err != nil && !errors.Is(err, io.EOF)) {
		return nil, errIndexMismatch
	}
	if buf[len(buf)-1] != '\n' || lineSum(buf) != entry.Sum || entry.Ix != indexMAC(entry) {
		return nil, errIndexMismatch
	}
	return buf, nil
}

func readIndexedRecord(file *os.File, entry indexEntry, live bool) (journalRecord, error) {
	buf, err := readEntryBytes(file, entry)
	if err != nil {
		return journalRecord{}, err
	}
	var event journalEvent
	if err := json.Unmarshal(bytes.TrimSpace(buf), &event); err != nil {
		return journalRecord{}, fmt.Errorf("decoding team journal: %w", err)
	}
	if event.Version != teamJournalVersion {
		return journalRecord{}, fmt.Errorf("unsupported team journal version %d", event.Version)
	}
	if event.Kind != entry.Kind || event.Team != entry.Team || event.RunID != entry.Run || event.At.UnixNano() != entry.At {
		return journalRecord{}, errIndexMismatch
	}
	return journalRecord{event: event, ownerLive: live}, nil
}
