package session

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lserrors "localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"

	"github.com/google/uuid"
)

// SessionTimeout is the duration after which an inactive session is considered expired.
// Sessions that don't receive any file uploads within this time will be cleaned up.
const SessionTimeout = 1 * time.Minute

// activityReader wraps an io.Reader and updates a timestamp pointer periodically.
// This keeps the session alive during long file transfers without excessive writes.
type activityReader struct {
	r            io.Reader
	lastActivity *int64
	lastUpdate   int64 // local tracking to rate-limit updates
}

const activityUpdateInterval = 10 // seconds between lastActivity updates

func (ar *activityReader) Read(p []byte) (n int, err error) {
	n, err = ar.r.Read(p)
	if n > 0 {
		now := time.Now().Unix()
		// Only update if at least activityUpdateInterval seconds have passed
		if now-ar.lastUpdate >= activityUpdateInterval {
			atomic.StoreInt64(ar.lastActivity, now)
			ar.lastUpdate = now
		}
	}
	return n, err
}

type RecvSession struct {
	// filesCount must be first for 64-bit alignment on 32-bit ARM
	filesCount   int64
	lastActivity int64 // Unix timestamp in seconds, updated on each file save
	fileMetas    models.FileMetas
	fileTokens   models.FileTokens
	mu           sync.RWMutex
	id           string
	clientIP     string // IP address of the client that initiated the session (per protocol spec Section 4.2)
	started      atomic.Bool

	// Folder remapping for unique folder creation (computed once on first SaveFile)
	folderRemapOnce sync.Once
	folderRemapper  *utils.FolderRemapper
	folderRemapErr  error

	// Cached folder transfer detection (computed once)
	isFolderTransferOnce   sync.Once
	isFolderTransferCached bool
}

func NewRecvSession(sessionId string, clientIP string) (*RecvSession, error) {
	sess := &RecvSession{
		fileMetas:    make(models.FileMetas),
		fileTokens:   make(models.FileTokens),
		id:           sessionId,
		clientIP:     clientIP,
		lastActivity: time.Now().Unix(),
	}

	return sess, nil
}

func (sess *RecvSession) AcceptFile(fileId string, fileMeta models.FileMeta) error {
	// unlikely, but check it anyway
	if fileId != fileMeta.Id {
		return lserrors.ErrUnknown
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	// Check started INSIDE the lock to prevent TOCTOU race
	// (previously checked before lock, allowing race with Start())
	if sess.started.Load() {
		return lserrors.ErrBlockedByOthers
	}

	// Prevent DoS via excessive file count
	if len(sess.fileMetas) >= lserrors.MaxFilesPerSession {
		return lserrors.ErrTooManyFiles
	}

	// store the file metadata
	sess.fileMetas[fileId] = fileMeta

	// generate file token
	sess.fileTokens[fileId] = uuid.NewString()

	// increment files count (inside lock to prevent race condition)
	atomic.AddInt64(&sess.filesCount, 1)

	return nil
}

func (sess *RecvSession) Start() {
	sess.started.Store(true)
}

// prepareFolderRemap computes folder remapping for the session.
// This finds unique names for root folders that already exist in saveDir.
// Called once on first SaveFile via sync.Once.
func (sess *RecvSession) prepareFolderRemap(saveDir string) error {
	sess.folderRemapOnce.Do(func() {
		// Collect filenames from fileMetas
		sess.mu.RLock()
		filenames := make([]string, 0, len(sess.fileMetas))
		for _, meta := range sess.fileMetas {
			filenames = append(filenames, meta.Filename)
		}
		sess.mu.RUnlock()

		remapper, err := utils.NewFolderRemapper(saveDir, filenames)
		if err != nil {
			sess.folderRemapErr = err
			return
		}

		// Log remapped folders
		for orig, unique := range remapper.GetRemap() {
			slog.Info("Remapping folder for uniqueness", "original", orig, "unique", unique)
		}

		sess.folderRemapper = remapper
	})
	return sess.folderRemapErr
}

// applyFolderRemap applies the folder remap to a sanitized path.
// If the path's root folder has been remapped, returns the remapped path.
func (sess *RecvSession) applyFolderRemap(sanitizedPath string) string {
	if sess.folderRemapper == nil {
		return sanitizedPath
	}
	return sess.folderRemapper.Apply(sanitizedPath)
}

func (sess *RecvSession) SaveFile(saveToDir string, fileId string, token string, clientIP string, fileData io.Reader) (string, error) {
	if sess.id == "" || fileId == "" || token == "" {
		return "", lserrors.ErrInvalidBody
	}

	// if a session is not started, it means the session is invalid
	if !sess.started.Load() {
		return "", lserrors.ErrRejected
	}

	// Validate client IP per protocol spec Section 4.2:
	// Return 403 for "Invalid token or IP address"
	if sess.clientIP != "" && clientIP != sess.clientIP {
		return "", lserrors.ErrRejected
	}

	sess.mu.RLock()
	expectedMeta, metaExist := sess.fileMetas[fileId]
	expectedToken, tokenExist := sess.fileTokens[fileId]
	sess.mu.RUnlock()

	// validate (constant-time comparison to prevent timing attacks on file tokens)
	if !metaExist || !tokenExist || subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) != 1 {
		return "", lserrors.ErrRejected
	}

	// Sanitize filename to allow subdirectories but prevent directory traversal attacks.
	// A malicious client could send "../../../etc/passwd" to write outside saveToDir.
	sanitizedPath, sanitizeErr := utils.SanitizePathWithFallback(expectedMeta.Filename)
	if sanitizeErr != nil {
		return "", lserrors.ErrInvalidBody
	}

	// Prepare folder remap on first SaveFile (for unique folder names)
	if err := sess.prepareFolderRemap(saveToDir); err != nil {
		slog.Error("Failed to prepare folder remap", "error", err)
		return "", lserrors.ErrFileIO
	}

	// Apply folder remap if the root folder was renamed for uniqueness
	sanitizedPath = sess.applyFolderRemap(sanitizedPath)

	// Split into directory and filename components
	subDir := filepath.Dir(sanitizedPath)
	baseName := filepath.Base(sanitizedPath)

	// Determine full save directory (includes any subdirectories from the path)
	fullSaveDir := saveToDir
	if subDir != "." && subDir != "" {
		fullSaveDir = filepath.Join(saveToDir, subDir)
	}

	// Ensure the directory exists (including any subdirectories)
	if err := os.MkdirAll(fullSaveDir, 0755); err != nil {
		slog.Error("Failed to create save directory", "dir", fullSaveDir, "error", err)
		return "", lserrors.ErrFileIO
	}

	// Atomically create a file with a unique name (prevents race conditions)
	file, saveAs, err := utils.CreateUniqueFile(fullSaveDir, baseName)
	if err != nil {
		slog.Error("Failed to create unique file", "error", err)
		return "", lserrors.ErrFileIO
	}

	// Track success for cleanup - remove partial file on any error
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			if removeErr := os.Remove(saveAs); removeErr != nil {
				slog.Warn("Failed to remove partial file", "path", saveAs, "error", removeErr)
			} else {
				slog.Debug("Removed partial file after error", "path", saveAs)
			}
		}
	}()

	hasher := sha256.New()

	// Wrap the reader to update lastActivity during transfer, keeping session alive
	activeReader := &activityReader{r: fileData, lastActivity: &sess.lastActivity}

	writer := io.MultiWriter(file, hasher)
	expectedSize := expectedMeta.Size
	written, err := io.Copy(writer, io.LimitReader(activeReader, expectedSize+1))
	if err != nil {
		return "", lserrors.ErrFileIO
	}
	if written != expectedSize {
		return "", lserrors.ErrInvalidBody
	}

	// calculate checksum if it's provided
	if expectedMeta.Checksum != "" {
		checksum := hex.EncodeToString(hasher.Sum(nil))

		if checksum != expectedMeta.Checksum {
			return "", lserrors.ErrChecksum
		}
	}

	// All validations passed - mark as success to prevent cleanup
	success = true

	slog.Info("Recv file", "file", saveAs, "session", sess.id)

	// remove finished file
	atomic.AddInt64(&sess.filesCount, -1)

	// end this session if it is the last file it received
	if count := atomic.LoadInt64(&sess.filesCount); count == 0 {
		sess.End()
	}

	// Return the actual saved filename (may differ from original if renamed due to conflict).
	// Include the subdirectory path if present.
	savedFilename := filepath.Base(saveAs)
	if subDir != "." && subDir != "" {
		savedFilename = filepath.Join(subDir, savedFilename)
		// Convert to forward slashes for consistency with protocol
		savedFilename = filepath.ToSlash(savedFilename)
	}
	return savedFilename, nil
}

func (sess *RecvSession) FileTokens() models.FileTokens {
	sess.mu.RLock()
	defer sess.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(models.FileTokens, len(sess.fileTokens))
	for k, v := range sess.fileTokens {
		result[k] = v
	}
	return result
}

func (sess *RecvSession) GetFileMeta(fileId string) (models.FileMeta, bool) {
	sess.mu.RLock()
	defer sess.mu.RUnlock()

	meta, ok := sess.fileMetas[fileId]
	return meta, ok
}

// IsFolderTransfer returns true if this session contains folder transfers
// (any file with subdirectory structure in its path).
// The result is cached for efficiency.
func (sess *RecvSession) IsFolderTransfer() bool {
	sess.isFolderTransferOnce.Do(func() {
		sess.mu.RLock()
		defer sess.mu.RUnlock()

		for _, meta := range sess.fileMetas {
			if strings.Contains(meta.Filename, "/") {
				sess.isFolderTransferCached = true
				return
			}
		}
		sess.isFolderTransferCached = false
	})
	return sess.isFolderTransferCached
}

// End ends the session if it's still active.
// Returns true if this call ended the session, false if already ended.
// Uses CompareAndSwap for thread-safe atomic check-and-set.
func (sess *RecvSession) End() bool {
	if sess.started.CompareAndSwap(true, false) {
		atomic.StoreInt64(&sess.filesCount, 0)
		slog.Info("Session done", "session", sess.id)
		return true
	}
	return false
}

func (sess *RecvSession) Stopped() bool {
	fileLefts := atomic.LoadInt64(&sess.filesCount)

	// Check if session has timed out due to inactivity
	lastActivity := atomic.LoadInt64(&sess.lastActivity)
	if time.Since(time.Unix(lastActivity, 0)) > SessionTimeout {
		return true
	}

	return (!sess.started.Load()) || (fileLefts == 0)
}
