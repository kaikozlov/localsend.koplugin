package transfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localsend-cli/internal/utils"
)

func TestRTCSender_LargePhysicalReadStillEmits16KiBFrames(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", utils.FileIOBufferSize+1)
	path := filepath.Join(dir, "large.bin")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewRTCSender(nil, nil, "")
	s.files = []FileMeta{{ID: "f", FileName: "large.bin", FilePath: path, Size: int64(len(content))}}
	s.fileTokens = map[string]string{"f": "token"}
	s.acceptedIDs = []string{"f"}
	rec := &recordingSendOps{results: s.fileResults}
	s.sendOpsOverride = rec

	if err := s.SendFiles(); err != nil {
		t.Fatal(err)
	}

	chunks := 0
	for _, event := range rec.events {
		parts := strings.Split(event, ":")
		if len(parts) != 3 || parts[0] != "data" {
			continue
		}
		var size int
		if _, err := fmt.Sscanf(parts[2], "%d", &size); err != nil {
			t.Fatalf("parse data event %q: %v", event, err)
		}
		chunks++
		if size > ChunkSize {
			t.Fatalf("wire frame = %d bytes; max is %d", size, ChunkSize)
		}
	}
	wantChunks := (len(content) + ChunkSize - 1) / ChunkSize
	if chunks != wantChunks {
		t.Fatalf("data frames = %d; want %d", chunks, wantChunks)
	}
}

func TestRTCReceiver_BuffersWritesAndSkipsHasherWithoutChecksum(t *testing.T) {
	dir := t.TempDir()
	r := NewRTCReceiver(nil, nil, "", dir)
	callbackCount := 0
	r.OnFileReceived(func(filename string, size int64, sender string) {
		callbackCount++
		if filename != "buffered.bin" || size != 3 || sender != "WebRTC" {
			t.Errorf("callback = (%q, %d, %q); want (buffered.bin, 3, WebRTC)", filename, size, sender)
		}
	})
	r.files = []RTCFileDto{{ID: "f", FileName: "buffered.bin", Size: 3}}
	tokens := r.prepareFilesForReceive([]string{"f"})
	if !r.startReceivingFile(&RTCSendFileHeader{ID: "f", Token: tokens["f"]}) {
		t.Fatal("failed to start receiving file")
	}
	if r.fileHashers["f"] != nil {
		t.Fatal("created SHA-256 hasher even though sender advertised no checksum")
	}
	path := r.filePaths["f"]
	r.handleBinaryData([]byte("abc"))

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("underlying file size before boundary = %d; want 0 while data is buffered", info.Size())
	}

	r.finishCurrentFile()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abc" {
		t.Fatalf("saved data = %q; want abc", got)
	}
	if callbackCount != 1 {
		t.Fatalf("completion callback count = %d; want 1", callbackCount)
	}
}

func TestRTCReceiver_RebuildFileIndexPreservesFirstDuplicate(t *testing.T) {
	r := NewRTCReceiver(nil, nil, "", t.TempDir())
	r.files = []RTCFileDto{
		{ID: "same", FileName: "first", Size: 1},
		{ID: "same", FileName: "second", Size: 2},
	}
	r.rebuildFileIndex()
	meta, ok := r.fileMetaByID("same")
	if !ok {
		t.Fatal("indexed file not found")
	}
	if meta.FileName != "first" || meta.Size != 1 {
		t.Fatalf("duplicate resolution = %#v; want first entry", meta)
	}
}

func BenchmarkRTCSenderPrepareSendQueue_1000Files(b *testing.B) {
	s := NewRTCSender(nil, nil, "")
	s.files = make([]FileMeta, 1000)
	s.fileTokens = make(map[string]string, 1000)
	s.acceptedIDs = make([]string, 1000)
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("file-%04d", i)
		s.files[i] = FileMeta{ID: id}
		s.fileTokens[id] = "token"
		s.acceptedIDs[999-i] = id
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := len(s.prepareSendQueue()); got != 1000 {
			b.Fatalf("queue length = %d", got)
		}
	}
}

func BenchmarkRTCReceiverFileLookup_1000Files(b *testing.B) {
	r := NewRTCReceiver(nil, nil, "", b.TempDir())
	r.files = make([]RTCFileDto, 1000)
	for i := range r.files {
		r.files[i] = RTCFileDto{ID: fmt.Sprintf("file-%04d", i), Size: int64(i)}
	}
	r.rebuildFileIndex()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := r.fileMetaByID("file-0999"); !ok {
			b.Fatal("file not found")
		}
	}
}

func BenchmarkRTCReceiverIsFolderTransfer_1000Files(b *testing.B) {
	r := NewRTCReceiver(nil, nil, "", b.TempDir())
	r.files = make([]RTCFileDto, 1000)
	for i := range r.files {
		r.files[i] = RTCFileDto{ID: fmt.Sprintf("file-%04d", i), FileName: fmt.Sprintf("Books/file-%04d.epub", i)}
	}
	if !r.isFolderTransfer() {
		b.Fatal("expected folder transfer")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !r.isFolderTransfer() {
			b.Fatal("expected folder transfer")
		}
	}
}
