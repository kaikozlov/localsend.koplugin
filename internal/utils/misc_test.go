package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestRandChoice tests the RandChoice function
func TestRandChoice(t *testing.T) {
	t.Run("returns zero value for empty slice", func(t *testing.T) {
		var empty []int
		result := RandChoice(empty)
		if result != 0 {
			t.Errorf("expected 0 for empty int slice, got %d", result)
		}
	})

	t.Run("returns zero value for nil slice", func(t *testing.T) {
		var nilSlice []string
		result := RandChoice(nilSlice)
		if result != "" {
			t.Errorf("expected empty string for nil string slice, got %q", result)
		}
	})

	t.Run("returns the only element for single-element slice", func(t *testing.T) {
		single := []int{42}
		result := RandChoice(single)
		if result != 42 {
			t.Errorf("expected 42, got %d", result)
		}
	})

	t.Run("returns element from slice", func(t *testing.T) {
		items := []string{"a", "b", "c", "d", "e"}
		result := RandChoice(items)

		found := false
		for _, item := range items {
			if item == result {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("result %q not found in original slice", result)
		}
	})

	t.Run("provides distribution across elements", func(t *testing.T) {
		items := []int{1, 2, 3}
		counts := make(map[int]int)

		// Run many iterations to check distribution
		iterations := 1000
		for i := 0; i < iterations; i++ {
			result := RandChoice(items)
			counts[result]++
		}

		// Each element should be picked at least once
		for _, item := range items {
			if counts[item] == 0 {
				t.Errorf("item %d was never selected in %d iterations", item, iterations)
			}
		}
	})
}

// TestSHA256ofFile tests the SHA256ofFile function
func TestSHA256ofFile(t *testing.T) {
	t.Run("computes correct hash for file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")

		content := []byte("hello world")
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		hash, err := SHA256ofFile(path)
		if err != nil {
			t.Fatalf("SHA256ofFile failed: %v", err)
		}

		// SHA256("hello world") = b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9
		expected := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
		if hash != expected {
			t.Errorf("expected hash %s, got %s", expected, hash)
		}
	})

	t.Run("computes correct hash for empty file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.txt")

		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		hash, err := SHA256ofFile(path)
		if err != nil {
			t.Fatalf("SHA256ofFile failed: %v", err)
		}

		// SHA256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
		expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		if hash != expected {
			t.Errorf("expected hash %s, got %s", expected, hash)
		}
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		_, err := SHA256ofFile("/nonexistent/file.txt")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})
}

// TestGetMyIPv4Addr tests the GetMyIPv4Addr function
func TestGetMyIPv4Addr(t *testing.T) {
	t.Run("returns valid IPs without error", func(t *testing.T) {
		ips, err := GetMyIPv4Addr()
		if err != nil {
			t.Fatalf("GetMyIPv4Addr failed: %v", err)
		}

		// Result might be empty if no non-loopback IPv4 interfaces are running
		// but it shouldn't error
		for _, ip := range ips {
			if ip.To4() == nil {
				t.Errorf("expected IPv4 address, got %v", ip)
			}
			if ip.IsLoopback() {
				t.Errorf("loopback addresses should be filtered out: %v", ip)
			}
		}
	})
}

// TestGetProtocolScheme tests the GetProtocolScheme function
func TestGetProtocolScheme(t *testing.T) {
	t.Run("returns https when useHTTPS is true", func(t *testing.T) {
		result := GetProtocolScheme(true)
		if result != "https" {
			t.Errorf("expected 'https', got %q", result)
		}
	})

	t.Run("returns http when useHTTPS is false", func(t *testing.T) {
		result := GetProtocolScheme(false)
		if result != "http" {
			t.Errorf("expected 'http', got %q", result)
		}
	})
}

// TestParseExtensionList tests the ParseExtensionList function
func TestParseExtensionList(t *testing.T) {
	t.Run("returns nil for empty string", func(t *testing.T) {
		result := ParseExtensionList("")
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("parses single extension", func(t *testing.T) {
		result := ParseExtensionList("pdf")
		if len(result) != 1 || result[0] != "pdf" {
			t.Errorf("expected [pdf], got %v", result)
		}
	})

	t.Run("parses multiple extensions", func(t *testing.T) {
		result := ParseExtensionList("pdf,epub,mobi")
		expected := []string{"pdf", "epub", "mobi"}
		if len(result) != len(expected) {
			t.Errorf("expected %v, got %v", expected, result)
		}
		for i, ext := range expected {
			if result[i] != ext {
				t.Errorf("expected %s at index %d, got %s", ext, i, result[i])
			}
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		result := ParseExtensionList("  pdf , epub  ,  mobi  ")
		expected := []string{"pdf", "epub", "mobi"}
		if len(result) != len(expected) {
			t.Errorf("expected %v, got %v", expected, result)
		}
		for i, ext := range expected {
			if result[i] != ext {
				t.Errorf("expected %s at index %d, got %s", ext, i, result[i])
			}
		}
	})

	t.Run("converts to lowercase", func(t *testing.T) {
		result := ParseExtensionList("PDF,EPUB,Mobi")
		expected := []string{"pdf", "epub", "mobi"}
		if len(result) != len(expected) {
			t.Errorf("expected %v, got %v", expected, result)
		}
		for i, ext := range expected {
			if result[i] != ext {
				t.Errorf("expected %s at index %d, got %s", ext, i, result[i])
			}
		}
	})

	t.Run("filters empty entries", func(t *testing.T) {
		result := ParseExtensionList("pdf,,epub,  ,mobi")
		expected := []string{"pdf", "epub", "mobi"}
		if len(result) != len(expected) {
			t.Errorf("expected %v, got %v", expected, result)
		}
	})
}

// TestEnsureDirectory tests the EnsureDirectory function
func TestEnsureDirectory(t *testing.T) {
	t.Run("creates directory that doesn't exist", func(t *testing.T) {
		dir := t.TempDir()
		newDir := filepath.Join(dir, "new", "nested", "dir")

		err := EnsureDirectory(newDir)
		if err != nil {
			t.Fatalf("EnsureDirectory failed: %v", err)
		}

		info, err := os.Stat(newDir)
		if err != nil {
			t.Fatalf("directory was not created: %v", err)
		}
		if !info.IsDir() {
			t.Error("path is not a directory")
		}
	})

	t.Run("succeeds when directory already exists", func(t *testing.T) {
		dir := t.TempDir()

		// Call twice - should succeed both times
		err := EnsureDirectory(dir)
		if err != nil {
			t.Fatalf("EnsureDirectory failed on existing directory: %v", err)
		}
	})

	t.Run("returns error for invalid path", func(t *testing.T) {
		// Try to create a directory inside a file
		dir := t.TempDir()
		filePath := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		invalidDir := filepath.Join(filePath, "subdir")
		err := EnsureDirectory(invalidDir)
		if err == nil {
			t.Error("expected error when creating directory inside a file")
		}
	})
}

// TestForEachAsync tests the ForEachAsync function
func TestForEachAsync(t *testing.T) {
	t.Run("executes function for each element", func(t *testing.T) {
		items := []int{1, 2, 3, 4, 5}
		results := make(chan int, len(items))

		var wg sync.WaitGroup
		ForEachAsync(items, &wg, func(val int) {
			results <- val
		})
		wg.Wait()
		close(results)

		collected := make(map[int]bool)
		for val := range results {
			collected[val] = true
		}

		for _, item := range items {
			if !collected[item] {
				t.Errorf("item %d was not processed", item)
			}
		}
	})

	t.Run("handles empty slice", func(t *testing.T) {
		var empty []string
		var wg sync.WaitGroup

		ForEachAsync(empty, &wg, func(val string) {
			t.Error("function should not be called for empty slice")
		})
		wg.Wait()
	})
}

// TestGetFileExtension tests the GetFileExtension function
func TestGetFileExtension(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{"simple extension", "file.pdf", "pdf"},
		{"uppercase extension", "FILE.PDF", "pdf"},
		{"mixed case", "Document.Epub", "epub"},
		{"multiple dots", "file.name.with.dots.txt", "txt"},
		{"no extension", "filename", ""},
		{"hidden file no ext", ".gitignore", "gitignore"},
		{"ends with dot", "file.", ""},
		{"empty string", "", ""},
		{"path with extension", "/path/to/file.mobi", "mobi"},
		{"windows path", "C:\\Users\\file.doc", "doc"},
		{"extension with numbers", "archive.7z", "7z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := GetFileExtension(tc.filename)
			if result != tc.expected {
				t.Errorf("GetFileExtension(%q) = %q, expected %q", tc.filename, result, tc.expected)
			}
		})
	}
}

// TestIsExtensionAllowed tests the IsExtensionAllowed function
func TestIsExtensionAllowed(t *testing.T) {
	t.Run("empty allowed list accepts all", func(t *testing.T) {
		if !IsExtensionAllowed("file.pdf", nil) {
			t.Error("nil allowedExtensions should accept all files")
		}
		if !IsExtensionAllowed("file.pdf", []string{}) {
			t.Error("empty allowedExtensions should accept all files")
		}
		if !IsExtensionAllowed("noextension", []string{}) {
			t.Error("empty allowedExtensions should accept files without extension")
		}
	})

	t.Run("accepts allowed extensions", func(t *testing.T) {
		allowed := []string{"pdf", "epub", "mobi"}

		if !IsExtensionAllowed("book.pdf", allowed) {
			t.Error("should accept .pdf")
		}
		if !IsExtensionAllowed("book.epub", allowed) {
			t.Error("should accept .epub")
		}
		if !IsExtensionAllowed("book.mobi", allowed) {
			t.Error("should accept .mobi")
		}
	})

	t.Run("rejects non-allowed extensions", func(t *testing.T) {
		allowed := []string{"pdf", "epub"}

		if IsExtensionAllowed("virus.exe", allowed) {
			t.Error("should reject .exe")
		}
		if IsExtensionAllowed("script.sh", allowed) {
			t.Error("should reject .sh")
		}
	})

	t.Run("rejects files without extension when filter is set", func(t *testing.T) {
		allowed := []string{"pdf"}

		if IsExtensionAllowed("noextension", allowed) {
			t.Error("should reject files without extension when filter is set")
		}
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		allowed := []string{"pdf"}

		if !IsExtensionAllowed("file.PDF", allowed) {
			t.Error("should accept .PDF (uppercase)")
		}
		if !IsExtensionAllowed("file.Pdf", allowed) {
			t.Error("should accept .Pdf (mixed case)")
		}
	})

	t.Run("handles paths with directories", func(t *testing.T) {
		allowed := []string{"pdf"}

		if !IsExtensionAllowed("/path/to/file.pdf", allowed) {
			t.Error("should accept file with path")
		}
		if !IsExtensionAllowed("C:\\Users\\Documents\\file.pdf", allowed) {
			t.Error("should accept file with Windows path")
		}
	})
}

// TestSanitizeForLog tests the SanitizeForLog function
func TestSanitizeForLog(t *testing.T) {
	t.Run("preserves normal text", func(t *testing.T) {
		input := "Hello, World! This is a normal filename.pdf"
		result := SanitizeForLog(input)
		if result != input {
			t.Errorf("expected %q, got %q", input, result)
		}
	})

	t.Run("preserves tabs", func(t *testing.T) {
		input := "file\twith\ttabs.txt"
		result := SanitizeForLog(input)
		if result != input {
			t.Errorf("expected %q, got %q", input, result)
		}
	})

	t.Run("removes control characters", func(t *testing.T) {
		// Test with newline, carriage return, and null byte
		input := "file\nwith\rnewlines\x00and\x07bells.txt"
		expected := "filewithnewlinesandbells.txt"
		result := SanitizeForLog(input)
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("removes escape sequences", func(t *testing.T) {
		// Test with ANSI escape sequence (could be used for log injection)
		input := "file\x1b[31mred\x1b[0m.txt"
		expected := "file[31mred[0m.txt"
		result := SanitizeForLog(input)
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("handles empty string", func(t *testing.T) {
		result := SanitizeForLog("")
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("handles unicode", func(t *testing.T) {
		input := "文件名.pdf"
		result := SanitizeForLog(input)
		if result != input {
			t.Errorf("expected %q, got %q", input, result)
		}
	})

	t.Run("handles emoji", func(t *testing.T) {
		input := "📚book.epub"
		result := SanitizeForLog(input)
		if result != input {
			t.Errorf("expected %q, got %q", input, result)
		}
	})
}

type bufferSizingReader struct {
	remaining int
	maxRead   int
}

func (r *bufferSizingReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		r.maxRead = len(p)
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = byte(i)
	}
	r.remaining -= n
	return n, nil
}

type writeOnlySink struct{ n int64 }

func (w *writeOnlySink) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func TestCopyWithFileIOBuffer_UsesLocalSend512KiBBuffer(t *testing.T) {
	if FileIOBufferSize != 512*1024 {
		t.Fatalf("FileIOBufferSize = %d; want 524288", FileIOBufferSize)
	}
	reader := &bufferSizingReader{remaining: FileIOBufferSize + 1}
	writer := &writeOnlySink{}
	written, err := CopyWithFileIOBuffer(writer, reader)
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(FileIOBufferSize+1) {
		t.Fatalf("written = %d; want %d", written, FileIOBufferSize+1)
	}
	if reader.maxRead != FileIOBufferSize {
		t.Fatalf("largest read buffer = %d; want %d", reader.maxRead, FileIOBufferSize)
	}
}

func BenchmarkSHA256ofFile_1MiB(b *testing.B) {
	path := filepath.Join(b.TempDir(), "one-mib.bin")
	if err := os.WriteFile(path, make([]byte, 1024*1024), 0644); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(1024 * 1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := SHA256ofFile(path); err != nil {
			b.Fatal(err)
		}
	}
}

type bypassCapableReader struct {
	bufferSizingReader
	writeToCalled bool
}

func (r *bypassCapableReader) WriteTo(io.Writer) (int64, error) {
	r.writeToCalled = true
	return 0, fmt.Errorf("WriterTo must not be used")
}

type bypassCapableWriter struct {
	writeOnlySink
	readFromCalled bool
}

func (w *bypassCapableWriter) ReadFrom(io.Reader) (int64, error) {
	w.readFromCalled = true
	return 0, fmt.Errorf("ReaderFrom must not be used")
}

func TestCopyWithFileIOBuffer_DoesNotBypassBufferViaFastPathInterfaces(t *testing.T) {
	reader := &bypassCapableReader{bufferSizingReader: bufferSizingReader{remaining: FileIOBufferSize + 1}}
	writer := &bypassCapableWriter{}
	written, err := CopyWithFileIOBuffer(writer, reader)
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(FileIOBufferSize+1) {
		t.Fatalf("written = %d; want %d", written, FileIOBufferSize+1)
	}
	if reader.writeToCalled || writer.readFromCalled {
		t.Fatalf("copy bypassed supplied buffer: WriterTo=%v ReaderFrom=%v", reader.writeToCalled, writer.readFromCalled)
	}
	if reader.maxRead != FileIOBufferSize {
		t.Fatalf("largest read buffer = %d; want %d", reader.maxRead, FileIOBufferSize)
	}
}
