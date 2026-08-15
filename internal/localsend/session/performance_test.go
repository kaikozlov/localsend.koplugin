package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"localsend-cli/internal/models"
)

func benchmarkSaveFile1MiB(b *testing.B, withChecksum bool) {
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(oldLogger) })

	payload := make([]byte, 1024*1024)
	checksum := ""
	if withChecksum {
		sum := sha256.Sum256(payload)
		checksum = hex.EncodeToString(sum[:])
	}
	dir := b.TempDir()
	savedPath := filepath.Join(dir, "file.bin")
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess, err := NewRecvSession(fmt.Sprintf("bench-%d", i), "192.0.2.1")
		if err != nil {
			b.Fatal(err)
		}
		meta := models.FileMeta{Id: "f", Filename: "file.bin", Size: int64(len(payload)), Checksum: checksum}
		if err := sess.AcceptFile("f", meta); err != nil {
			b.Fatal(err)
		}
		sess.Start()
		if _, err := sess.SaveFile(dir, "f", sess.FileTokens()["f"], "192.0.2.1", bytes.NewReader(payload)); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := os.Remove(savedPath); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func BenchmarkSaveFile_1MiB_NoChecksum(b *testing.B) {
	benchmarkSaveFile1MiB(b, false)
}

func BenchmarkSaveFile_1MiB_WithChecksum(b *testing.B) {
	benchmarkSaveFile1MiB(b, true)
}
