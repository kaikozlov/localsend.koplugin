package models

import (
	"encoding/json"
	"math"
	"testing"
)

func TestFlexInt_UnmarshalJSON_Integer(t *testing.T) {
	var fi FlexInt
	err := json.Unmarshal([]byte(`346`), &fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fi != 346 {
		t.Errorf("got %d, want 346", fi)
	}
}

func TestFlexInt_UnmarshalJSON_Float(t *testing.T) {
	var fi FlexInt
	err := json.Unmarshal([]byte(`346.0`), &fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fi != 346 {
		t.Errorf("got %d, want 346", fi)
	}
}

func TestFlexInt_UnmarshalJSON_FloatTruncates(t *testing.T) {
	var fi FlexInt
	err := json.Unmarshal([]byte(`346.9`), &fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fi != 346 {
		t.Errorf("got %d, want 346 (truncated)", fi)
	}
}

func TestFlexInt_UnmarshalJSON_QuotedInteger(t *testing.T) {
	var fi FlexInt
	err := json.Unmarshal([]byte(`"346"`), &fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fi != 346 {
		t.Errorf("got %d, want 346", fi)
	}
}

func TestFlexInt_UnmarshalJSON_QuotedFloat(t *testing.T) {
	var fi FlexInt
	err := json.Unmarshal([]byte(`"346.0"`), &fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fi != 346 {
		t.Errorf("got %d, want 346", fi)
	}
}

func TestFlexInt_UnmarshalJSON_Zero(t *testing.T) {
	var fi FlexInt
	err := json.Unmarshal([]byte(`0`), &fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fi != 0 {
		t.Errorf("got %d, want 0", fi)
	}
}

func TestFlexInt_UnmarshalJSON_LargeValue(t *testing.T) {
	var fi FlexInt
	err := json.Unmarshal([]byte(`9999999999999`), &fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fi != 9999999999999 {
		t.Errorf("got %d, want 9999999999999", fi)
	}
}

func TestFlexInt_MarshalJSON(t *testing.T) {
	fi := FlexInt(346)
	data, err := json.Marshal(fi)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "346" {
		t.Errorf("got %s, want 346", data)
	}
}

func TestFlexInt_Int64(t *testing.T) {
	fi := FlexInt(346)
	if fi.Int64() != 346 {
		t.Errorf("got %d, want 346", fi.Int64())
	}
}

func TestFlexInt_Valid(t *testing.T) {
	tests := []struct {
		name  string
		value FlexInt
		valid bool
	}{
		{"zero", 0, true},
		{"positive", 346, true},
		{"max int64", FlexInt(math.MaxInt64), true},
		{"negative", -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value.Valid() != tt.valid {
				t.Errorf("Valid() = %v, want %v for %d", tt.value.Valid(), tt.valid, tt.value)
			}
		})
	}
}

func TestFlexInt_InPreUploadReq(t *testing.T) {
	// Test the actual use case: a prepare-upload request with float size
	jsonBody := `{
		"info": {"alias":"T","version":"2.0","deviceType":"mobile","fingerprint":"x","port":53317,"protocol":"https"},
		"files": {
			"z1": {"id":"z1","fileName":"archive.zip","size":346.0,"fileType":"application/zip"}
		}
	}`

	var req PreUploadReq
	err := json.Unmarshal([]byte(jsonBody), &req)
	if err != nil {
		t.Fatalf("failed to parse prepare-upload with float size: %v", err)
	}

	meta, ok := req.Files["z1"]
	if !ok {
		t.Fatal("file z1 not found")
	}
	if meta.Size != 346 {
		t.Errorf("Size = %d, want 346", meta.Size)
	}
	if meta.Filename != "archive.zip" {
		t.Errorf("Filename = %q, want %q", meta.Filename, "archive.zip")
	}
}

func TestFlexInt_StringSize(t *testing.T) {
	// Some clients send size as a string
	jsonBody := `{"id":"z1","fileName":"test.epub","size":"12345","fileType":"application/epub"}`

	var meta FileMeta
	err := json.Unmarshal([]byte(jsonBody), &meta)
	if err != nil {
		t.Fatalf("failed to parse FileMeta with string size: %v", err)
	}
	if meta.Size != 12345 {
		t.Errorf("Size = %d, want 12345", meta.Size)
	}
}
