package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MaxUniquePathAttempts is the maximum number of attempts to find a unique path.
const MaxUniquePathAttempts = 10000

// FindUniqueFolderName finds a unique folder name in saveDir, appending a counter if needed.
// For example: "Photos" -> "Photos (1)" -> "Photos (2)" if folders already exist.
func FindUniqueFolderName(saveDir, folderName string) (string, error) {
	path := filepath.Join(saveDir, folderName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return folderName, nil // doesn't exist, use as-is
	}

	// Folder exists, find unique name
	for i := 1; i <= MaxUniquePathAttempts; i++ {
		newName := fmt.Sprintf("%s (%d)", folderName, i)
		path = filepath.Join(saveDir, newName)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return newName, nil
		}
	}
	return "", fmt.Errorf("could not find unique folder name after %d attempts for %s", MaxUniquePathAttempts, folderName)
}

type uniqueFileKey struct {
	dir      string
	filename string
}

// UniqueFileAllocator remembers the next likely numeric suffix for filenames it
// has already allocated. O_CREATE|O_EXCL remains the authority, so concurrent
// uploads and external filesystem changes cannot overwrite an existing file.
// The zero value is ready to use.
type UniqueFileAllocator struct {
	mu   sync.Mutex
	next map[uniqueFileKey]int
}

// Reset forgets session-local suffix hints.
func (a *UniqueFileAllocator) Reset() {
	a.mu.Lock()
	a.next = nil
	a.mu.Unlock()
}

// Create atomically creates a unique file while avoiding a restart from suffix
// (1) for every duplicate in the same transfer.
func (a *UniqueFileAllocator) Create(dir, filename string) (*os.File, string, error) {
	key := uniqueFileKey{dir: dir, filename: filename}
	a.mu.Lock()
	start := 0
	if a.next != nil {
		start = a.next[key]
	}
	a.mu.Unlock()

	file, path, usedSuffix, err := createUniqueFileFrom(dir, filename, start)
	if err != nil {
		return nil, "", err
	}

	next := usedSuffix + 1
	if usedSuffix == 0 {
		next = 1
	}
	a.mu.Lock()
	if a.next == nil {
		a.next = make(map[uniqueFileKey]int)
	}
	if next > a.next[key] {
		a.next[key] = next
	}
	a.mu.Unlock()
	return file, path, nil
}

// CreateUniqueFile atomically creates a file with a unique name, appending a counter if needed.
// For example: "file.txt" -> "file (1).txt" -> "file (2).txt"
// Uses O_CREATE|O_EXCL to prevent race conditions between concurrent uploads.
// Returns the opened file and its path, or an error if a unique name cannot be found.
func CreateUniqueFile(dir, filename string) (*os.File, string, error) {
	file, path, _, err := createUniqueFileFrom(dir, filename, 0)
	return file, path, err
}

func createUniqueFileFrom(dir, filename string, startSuffix int) (*os.File, string, int, error) {
	if startSuffix <= 0 {
		path := filepath.Join(dir, filename)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			return file, path, 0, nil
		}
		if !os.IsExist(err) {
			return nil, "", 0, err
		}
		startSuffix = 1
	}

	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)
	for i := startSuffix; i <= MaxUniquePathAttempts; i++ {
		newFilename := fmt.Sprintf("%s (%d)%s", name, i, ext)
		path := filepath.Join(dir, newFilename)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			return file, path, i, nil
		}
		if !os.IsExist(err) {
			return nil, "", 0, err
		}
	}

	return nil, "", 0, fmt.Errorf("could not create unique file after %d attempts for %s", MaxUniquePathAttempts, filename)
}
