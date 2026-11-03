package send

import (
	"io/fs"
	"path/filepath"

	"localsend-cli/internal/models"
)

type FileSender interface {
	SetPIN(pin string)
	SetAlias(alias string)
	Init(target *models.DeviceInfo, https bool) error
	AddFile(filePath string) error
	AddDir(dirPath string) error
	AddDirWithStructure(dirPath string) error
	AddText(text []byte)
	Start() error
	Cancel() error
}

type baseSender struct {
	tokens  models.FileTokens
	files   models.FileMetas
	session string
	pin     string
	alias   string // Custom alias for device name (if set, overrides generated alias)
}

func (fsp *baseSender) SetPIN(pin string) {
	fsp.pin = pin
}

func (fsp *baseSender) SetAlias(alias string) {
	fsp.alias = alias
}

func (fsp *baseSender) AddFile(filePath string) error {
	if fsp.files == nil {
		fsp.files = make(map[string]models.FileMeta)
	}

	fileMeta, err := models.GenFileMeta(filePath)
	if err != nil {
		return err
	}

	fsp.files[fileMeta.Id] = fileMeta
	return nil
}

// AddText adds text sent as a message, or as a .txt file when it is longer
// than models.MaxMessageBytes.
func (fsp *baseSender) AddText(text []byte) {
	if fsp.files == nil {
		fsp.files = make(map[string]models.FileMeta)
	}
	fileMeta := models.GenTextFileMeta(text)
	fsp.files[fileMeta.Id] = fileMeta
}

func (fsp *baseSender) AddDir(dirPath string) error {
	return filepath.Walk(dirPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		return fsp.AddFile(path)
	})
}

// addFileWithBase adds a file with its filename set to the relative path from baseDir.
func (fsp *baseSender) addFileWithBase(filePath string, baseDir string) error {
	if fsp.files == nil {
		fsp.files = make(map[string]models.FileMeta)
	}

	fileMeta, err := models.GenFileMetaWithBase(filePath, baseDir)
	if err != nil {
		return err
	}

	fsp.files[fileMeta.Id] = fileMeta
	return nil
}

// AddDirWithStructure adds all files in a directory while preserving the
// subdirectory structure. The directory name becomes the root of the structure.
//
// Example: AddDirWithStructure("/tmp/Photos") sends files as:
//   - "Photos/beach.jpg"
//   - "Photos/Summer/vacation.jpg"
func (fsp *baseSender) AddDirWithStructure(dirPath string) error {
	// Use parent of dirPath as base, so dirPath name is included in the relative path
	baseDir := filepath.Dir(dirPath)
	return filepath.Walk(dirPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		return fsp.addFileWithBase(path, baseDir)
	})
}

func (fsp *baseSender) reset() {
	// Use O(1) map reassignment instead of O(n) range-delete
	fsp.tokens = make(map[string]string)
	fsp.files = make(models.FileMetas)
}

// FileCount returns the number of files added to the sender.
func (fsp *baseSender) FileCount() int {
	return len(fsp.files)
}

// Files returns a copy of the files map for inspection.
func (fsp *baseSender) Files() models.FileMetas {
	return fsp.files
}
