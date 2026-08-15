package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
)

type atomicBlockingReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *atomicBlockingReader) Read(_ []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
}

func TestSaveFile_ClaimsFileAtomically(t *testing.T) {
	dir := t.TempDir()
	sess, _ := NewRecvSession("atomic-session", "192.0.2.1")
	meta := models.FileMeta{Id: "file1", Filename: "atomic.bin", Size: 0}
	if err := sess.AcceptFile("file1", meta); err != nil {
		t.Fatal(err)
	}
	sess.Start()
	token := sess.FileTokens()["file1"]
	blocked := &atomicBlockingReader{started: make(chan struct{}), release: make(chan struct{})}
	firstDone := make(chan error, 1)
	go func() {
		_, err := sess.SaveFile(dir, "file1", token, "192.0.2.1", blocked)
		firstDone <- err
	}()
	<-blocked.started

	if _, err := sess.SaveFile(dir, "file1", token, "192.0.2.1", bytes.NewReader(nil)); err != constants.ErrRejected {
		t.Fatalf("concurrent upload error = %v; want ErrRejected", err)
	}
	close(blocked.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("claimed upload failed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("saved files = %d; want exactly 1", len(entries))
	}
}

func TestSaveFile_StopsSessionAfterThreeChecksumMismatches(t *testing.T) {
	dir := t.TempDir()
	sess, _ := NewRecvSession("checksum-attempts", "192.0.2.1")
	content := []byte("wrong")
	meta := models.FileMeta{
		Id:       "file1",
		Filename: "checksum.bin",
		Size:     int64(len(content)),
		Checksum: strings.Repeat("0", sha256.Size*2),
	}
	if err := sess.AcceptFile("file1", meta); err != nil {
		t.Fatal(err)
	}
	sess.Start()
	token := sess.FileTokens()["file1"]
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := sess.SaveFile(dir, "file1", token, "192.0.2.1", bytes.NewReader(content)); err != constants.ErrChecksum {
			t.Fatalf("attempt %d error = %v; want ErrChecksum", attempt, err)
		}
	}
	if !sess.Stopped() {
		t.Fatal("session remains active after the third checksum mismatch")
	}
	if _, err := sess.SaveFile(dir, "file1", token, "192.0.2.1", bytes.NewReader(content)); err != constants.ErrRejected {
		t.Fatalf("fourth attempt error = %v; want ErrRejected", err)
	}
}

func TestSaveFile_ChecksumRetryReusesSameDuplicateTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	good := []byte("good")
	bad := []byte("baad")
	sum := sha256.Sum256(good)
	sess, _ := NewRecvSession("checksum-retry-path", "192.0.2.1")
	meta := models.FileMeta{
		Id:       "file1",
		Filename: "doc.pdf",
		Size:     int64(len(good)),
		Checksum: hex.EncodeToString(sum[:]),
	}
	if err := sess.AcceptFile("file1", meta); err != nil {
		t.Fatal(err)
	}
	sess.Start()
	token := sess.FileTokens()["file1"]
	if _, err := sess.SaveFile(dir, "file1", token, "192.0.2.1", bytes.NewReader(bad)); err != constants.ErrChecksum {
		t.Fatalf("first attempt error = %v; want ErrChecksum", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "doc (1).pdf")); !os.IsNotExist(err) {
		t.Fatalf("failed checksum left retry target on disk: %v", err)
	}

	saved, err := sess.SaveFile(dir, "file1", token, "192.0.2.1", bytes.NewReader(good))
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if saved != "doc (1).pdf" {
		t.Fatalf("retry saved as %q; want doc (1).pdf", saved)
	}
}
