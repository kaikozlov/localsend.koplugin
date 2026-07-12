package transfer

import (
	"encoding/json"
	"testing"
)

func TestDCFile_UnmarshalJSON_RejectsFloatSize(t *testing.T) {
	var file DCFile
	err := json.Unmarshal([]byte(`{"id":"file-123","fileName":"test.txt","size":1024.0,"fileType":"text/plain"}`), &file)
	if err == nil {
		t.Fatal("expected float-encoded size to be rejected")
	}
}

func TestRTCFileDto_UnmarshalJSON_RejectsFloatSize(t *testing.T) {
	var file RTCFileDto
	err := json.Unmarshal([]byte(`{"id":"file-123","fileName":"test.txt","size":1024.0,"fileType":"text/plain"}`), &file)
	if err == nil {
		t.Fatal("expected float-encoded size to be rejected")
	}
}
