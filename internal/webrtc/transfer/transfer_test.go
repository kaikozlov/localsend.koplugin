package transfer

import (
	"encoding/json"
	"testing"
)

func TestNewHandshakeMessage(t *testing.T) {
	msg := NewHandshakeMessage("test-token-123")

	if msg.Type != DCTypeHandshake {
		t.Errorf("Type = %q; want %q", msg.Type, DCTypeHandshake)
	}
	if msg.Token != "test-token-123" {
		t.Errorf("Token = %q; want 'test-token-123'", msg.Token)
	}
}

func TestNewFilesMessage(t *testing.T) {
	files := []DCFile{
		{ID: "file1", FileName: "test.txt", Size: 100, FileType: "text/plain"},
		{ID: "file2", FileName: "image.png", Size: 2000, FileType: "image/png"},
	}

	msg := NewFilesMessage(files)

	if msg.Type != DCTypeFiles {
		t.Errorf("Type = %q; want %q", msg.Type, DCTypeFiles)
	}
	if len(msg.Files) != 2 {
		t.Errorf("len(Files) = %d; want 2", len(msg.Files))
	}
}

func TestNewAcceptMessage(t *testing.T) {
	fileIDs := []string{"file1", "file3"}

	msg := NewAcceptMessage(fileIDs)

	if msg.Type != DCTypeAccept {
		t.Errorf("Type = %q; want %q", msg.Type, DCTypeAccept)
	}
	if len(msg.FileIDs) != 2 {
		t.Errorf("len(FileIDs) = %d; want 2", len(msg.FileIDs))
	}
}

func TestNewDeclineMessage(t *testing.T) {
	msg := NewDeclineMessage()

	if msg.Type != DCTypeDecline {
		t.Errorf("Type = %q; want %q", msg.Type, DCTypeDecline)
	}
}

func TestNewFinishMessage(t *testing.T) {
	msg := NewFinishMessage("file-abc")

	if msg.Type != DCTypeFinish {
		t.Errorf("Type = %q; want %q", msg.Type, DCTypeFinish)
	}
	if msg.FileID != "file-abc" {
		t.Errorf("FileID = %q; want 'file-abc'", msg.FileID)
	}
}

func TestParseMessageType(t *testing.T) {
	tests := []struct {
		json     string
		expected string
		wantErr  bool
	}{
		{`{"type":"HANDSHAKE"}`, DCTypeHandshake, false},
		{`{"type":"FILES","files":[]}`, DCTypeFiles, false},
		{`{"type":"ACCEPT"}`, DCTypeAccept, false},
		{`{"type":"DECLINE"}`, DCTypeDecline, false},
		{`{"type":"BINARY"}`, DCTypeBinary, false},
		{`{"type":"FINISH"}`, DCTypeFinish, false},
		{`{"type":"ERROR"}`, DCTypeError, false},
		{`invalid json`, "", true},
	}

	for _, tt := range tests {
		msgType, err := ParseMessageType([]byte(tt.json))
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseMessageType(%q) error = %v; wantErr = %v", tt.json, err, tt.wantErr)
			continue
		}
		if msgType != tt.expected {
			t.Errorf("ParseMessageType(%q) = %q; want %q", tt.json, msgType, tt.expected)
		}
	}
}

func TestDCFileSerialization(t *testing.T) {
	file := DCFile{
		ID:       "test-id",
		FileName: "document.pdf",
		Size:     1024000,
		FileType: "application/pdf",
		SHA256:   "abc123def456",
	}

	bytes, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var parsed DCFile
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if parsed.ID != file.ID {
		t.Errorf("ID = %q; want %q", parsed.ID, file.ID)
	}
	if parsed.FileName != file.FileName {
		t.Errorf("FileName = %q; want %q", parsed.FileName, file.FileName)
	}
	if parsed.Size != file.Size {
		t.Errorf("Size = %d; want %d", parsed.Size, file.Size)
	}
}

// TestRTCSender_Close_DoubleClose verifies that closing an RTCSender multiple times
// does not panic, thanks to sync.Once protecting channel closures.
func TestRTCSender_Close_DoubleClose(t *testing.T) {
	sender := NewRTCSender(nil, nil, "")

	// First close should succeed
	if err := sender.Close(); err != nil {
		t.Errorf("First Close() failed: %v", err)
	}

	// Second close should also succeed (no panic)
	if err := sender.Close(); err != nil {
		t.Errorf("Second Close() failed: %v", err)
	}

	// Third close should also succeed
	if err := sender.Close(); err != nil {
		t.Errorf("Third Close() failed: %v", err)
	}
}
