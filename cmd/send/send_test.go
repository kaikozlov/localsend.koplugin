package send

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMakeFileMeta_IncludesChecksumAndAccessedTimestamp(t *testing.T) {
	content := []byte("wire metadata")
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	accessed := time.Unix(1_700_000_000, 123_456_789)
	modified := time.Unix(1_700_000_001, 987_654_321)
	if err := os.Chtimes(path, accessed, modified); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	meta := makeFileMeta(path, info)
	wantHash := sha256.Sum256(content)
	if meta.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("sha256 = %q; want %q", meta.SHA256, hex.EncodeToString(wantHash[:]))
	}
	if !meta.Accessed.Equal(accessed) {
		t.Fatalf("accessed = %s; want %s", meta.Accessed.Format(time.RFC3339Nano), accessed.Format(time.RFC3339Nano))
	}
	if !meta.Modified.Equal(modified) {
		t.Fatalf("modified = %s; want %s", meta.Modified.Format(time.RFC3339Nano), modified.Format(time.RFC3339Nano))
	}
}

func TestMakeFileMetaWithBase_IncludesChecksumAndAccessedTimestamp(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "Books")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("nested wire metadata")
	path := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	accessed := time.Unix(1_700_000_002, 111_222_333)
	modified := time.Unix(1_700_000_003, 444_555_666)
	if err := os.Chtimes(path, accessed, modified); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	meta := makeFileMetaWithBase(path, info, base)
	wantHash := sha256.Sum256(content)
	if meta.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("sha256 = %q; want %q", meta.SHA256, hex.EncodeToString(wantHash[:]))
	}
	if !meta.Accessed.Equal(accessed) {
		t.Fatalf("accessed = %s; want %s", meta.Accessed.Format(time.RFC3339Nano), accessed.Format(time.RFC3339Nano))
	}
}

func TestReadText_DropsOneTrailingLineBreakFromStdin(t *testing.T) {
	got, err := readText("-", strings.NewReader("first\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\r\n" {
		t.Fatalf("readText = %q, want %q", got, "first\r\n")
	}
}

func TestReadText_KeepsTrailingLineBreakInArgument(t *testing.T) {
	got, err := readText("line\n", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "line\n" {
		t.Fatalf("readText = %q, want the argument unchanged", got)
	}
}

func TestReadText_RejectsStdinOverLimit(t *testing.T) {
	if _, err := readText("-", strings.NewReader(strings.Repeat("a", maxStdinTextBytes+1))); err == nil {
		t.Fatal("readText accepted stdin over the limit")
	}
	if _, err := readText("-", strings.NewReader(strings.Repeat("a", maxStdinTextBytes))); err != nil {
		t.Fatalf("readText at the limit = %v, want accepted", err)
	}
}

func TestReadText_RejectsEmptyAndNonUTF8Text(t *testing.T) {
	for name, stdin := range map[string]string{"empty": "\n", "not UTF-8": "\xff\xfe"} {
		if _, err := readText("-", strings.NewReader(stdin)); err == nil {
			t.Fatalf("readText accepted %s text", name)
		}
	}
}
