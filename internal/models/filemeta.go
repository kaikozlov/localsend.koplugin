package models

import (
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"localsend-cli/internal/utils"
)

// FileMetadata contains optional file timestamp information
type FileMetadata struct {
	Modified string `json:"modified,omitempty"`
	Accessed string `json:"accessed,omitempty"`
}

type FileMeta struct {
	Id       string        `json:"id"`
	Filename string        `json:"fileName"`
	Size     int64         `json:"size"`
	FileMIME string        `json:"fileType"`
	Checksum string        `json:"sha256,omitempty"`
	Preview  string        `json:"preview,omitempty"`
	Metadata *FileMetadata `json:"metadata,omitempty"`
	FullPath string        `json:"-"`
}

func GenFileMeta(fpath string) (FileMeta, error) {
	fd, err := os.Stat(fpath)
	if err != nil {
		return FileMeta{}, err
	}

	checksum, err := utils.SHA256ofFile(fpath)
	if err != nil {
		return FileMeta{}, err
	}

	fileType := mime.TypeByExtension(filepath.Ext(fpath))
	if fileType == "" {
		fileType = "text/plain"
	}

	return FileMeta{
		Id:       uuid.NewString(),
		Filename: fd.Name(),
		Size:     fd.Size(),
		FileMIME: fileType,
		Checksum: checksum,
		Metadata: &FileMetadata{
			Modified: fd.ModTime().Format(time.RFC3339),
			Accessed: getAccessTime(fd).Format(time.RFC3339),
		},
		FullPath: fpath,
	}, nil
}

// GenFileMetaWithBase generates file metadata with the filename set to the
// relative path from baseDir. This preserves directory structure when sending
// folders. The relative path uses forward slashes for protocol compatibility.
//
// Example: GenFileMetaWithBase("/tmp/Photos/Summer/beach.jpg", "/tmp")
// produces Filename: "Photos/Summer/beach.jpg"
func GenFileMetaWithBase(fpath string, baseDir string) (FileMeta, error) {
	fd, err := os.Stat(fpath)
	if err != nil {
		return FileMeta{}, err
	}

	checksum, err := utils.SHA256ofFile(fpath)
	if err != nil {
		return FileMeta{}, err
	}

	fileType := mime.TypeByExtension(filepath.Ext(fpath))
	if fileType == "" {
		fileType = "text/plain"
	}

	// Calculate relative path from baseDir
	relPath, err := filepath.Rel(baseDir, fpath)
	if err != nil {
		return FileMeta{}, err
	}

	// Normalize to forward slashes for protocol compatibility
	relPath = filepath.ToSlash(relPath)

	return FileMeta{
		Id:       uuid.NewString(),
		Filename: relPath, // Relative path instead of fd.Name()
		Size:     fd.Size(),
		FileMIME: fileType,
		Checksum: checksum,
		Metadata: &FileMetadata{
			Modified: fd.ModTime().Format(time.RFC3339),
			Accessed: getAccessTime(fd).Format(time.RFC3339),
		},
		FullPath: fpath,
	}, nil
}
