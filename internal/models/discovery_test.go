package models

import (
	"encoding/json"
	"testing"
)

// =============================================================================
// DeviceInfo JSON Marshaling Tests
// =============================================================================

// TestDeviceInfo_JSONMarshaling_CorrectFieldNames verifies that DeviceInfo
// uses the correct JSON field names per protocol spec.
func TestDeviceInfo_JSONMarshaling_CorrectFieldNames(t *testing.T) {
	device := DeviceInfo{
		Alias:       "Test Device",
		Version:     "2.3",
		DeviceModel: "iPhone 15",
		DeviceType:  "mobile",
		Fingerprint: "abc123",
		Token:       "token123",
		Download:    true,
	}

	data, err := json.Marshal(device)
	if err != nil {
		t.Fatalf("Failed to marshal DeviceInfo: %v", err)
	}

	// Parse as generic map to verify field names
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// Verify expected field names
	expectedFields := map[string]bool{
		"alias":       true,
		"version":     true,
		"deviceModel": true,
		"deviceType":  true,
		"fingerprint": true,
		"token":       true,
		"download":    true,
	}

	for field := range expectedFields {
		if _, ok := result[field]; !ok {
			t.Errorf("Expected field %q not found in JSON output", field)
		}
	}

	// Verify IP is not included (json:"-")
	if _, ok := result["ip"]; ok {
		t.Error("IP field should not be serialized (has json:\"-\" tag)")
	}
	if _, ok := result["IP"]; ok {
		t.Error("IP field should not be serialized (has json:\"-\" tag)")
	}
}

// TestDeviceInfo_JSONUnmarshal_ParsesCorrectly verifies that DeviceInfo
// can be properly unmarshaled from JSON.
func TestDeviceInfo_JSONUnmarshal_ParsesCorrectly(t *testing.T) {
	jsonData := `{
		"alias": "Sender",
		"version": "2.3",
		"deviceModel": "Android",
		"deviceType": "mobile",
		"fingerprint": "xyz789",
		"download": false
	}`

	var device DeviceInfo
	if err := json.Unmarshal([]byte(jsonData), &device); err != nil {
		t.Fatalf("Failed to unmarshal DeviceInfo: %v", err)
	}

	if device.Alias != "Sender" {
		t.Errorf("Alias = %q; want 'Sender'", device.Alias)
	}
	if device.Version != "2.3" {
		t.Errorf("Version = %q; want '2.3'", device.Version)
	}
	if device.DeviceModel != "Android" {
		t.Errorf("DeviceModel = %q; want 'Android'", device.DeviceModel)
	}
	if device.DeviceType != "mobile" {
		t.Errorf("DeviceType = %q; want 'mobile'", device.DeviceType)
	}
	if device.Fingerprint != "xyz789" {
		t.Errorf("Fingerprint = %q; want 'xyz789'", device.Fingerprint)
	}
	if device.Download != false {
		t.Error("Download should be false")
	}
}

// TestDeviceInfo_OmitsEmptyOptionalFields verifies that optional fields
// with omitempty are not included when empty.
func TestDeviceInfo_OmitsEmptyOptionalFields(t *testing.T) {
	device := DeviceInfo{
		Alias:   "Test",
		Version: "2.3",
		// Optional fields left empty
	}

	data, err := json.Marshal(device)
	if err != nil {
		t.Fatalf("Failed to marshal DeviceInfo: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// These fields should be omitted when empty
	omittedFields := []string{"deviceModel", "deviceType", "fingerprint", "token", "download", "hasWebInterface"}
	for _, field := range omittedFields {
		if _, ok := result[field]; ok {
			t.Errorf("Field %q should be omitted when empty (has omitempty)", field)
		}
	}
}

// =============================================================================
// Announcement Tests
// =============================================================================

// TestAnnouncement_JSONMarshaling verifies that Announcement uses correct
// JSON field names for embedded DeviceInfo and additional fields.
func TestAnnouncement_JSONMarshaling(t *testing.T) {
	announcement := Announcement{
		DeviceInfo: DeviceInfo{
			Alias:       "Test",
			Version:     "2.3",
			DeviceModel: "Test Model",
			DeviceType:  "headless",
		},
		Protocol: "https",
		Port:     53317,
		Announce: true,
	}

	data, err := json.Marshal(announcement)
	if err != nil {
		t.Fatalf("Failed to marshal Announcement: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// Verify embedded DeviceInfo fields are at top level
	if result["alias"] != "Test" {
		t.Errorf("alias = %v; want 'Test'", result["alias"])
	}

	// Verify Announcement-specific fields
	if result["protocol"] != "https" {
		t.Errorf("protocol = %v; want 'https'", result["protocol"])
	}
	if result["port"] != float64(53317) {
		t.Errorf("port = %v; want 53317", result["port"])
	}
	if result["announce"] != true {
		t.Errorf("announce = %v; want true", result["announce"])
	}
}

// TestAnnouncement_JSONUnmarshal_ParsesCorrectly verifies that Announcement
// can be properly unmarshaled from JSON.
func TestAnnouncement_JSONUnmarshal_ParsesCorrectly(t *testing.T) {
	jsonData := `{
		"alias": "Remote Device",
		"version": "2.3",
		"deviceType": "mobile",
		"protocol": "http",
		"port": 53317,
		"announce": true
	}`

	var announcement Announcement
	if err := json.Unmarshal([]byte(jsonData), &announcement); err != nil {
		t.Fatalf("Failed to unmarshal Announcement: %v", err)
	}

	if announcement.Alias != "Remote Device" {
		t.Errorf("Alias = %q; want 'Remote Device'", announcement.Alias)
	}
	if announcement.Protocol != "http" {
		t.Errorf("Protocol = %q; want 'http'", announcement.Protocol)
	}
	if announcement.Port != 53317 {
		t.Errorf("Port = %d; want 53317", announcement.Port)
	}
	if announcement.Announce != true {
		t.Error("Announce should be true")
	}
}

// TestAnnouncement_GetDeviceInfo verifies the GetDeviceInfo helper method.
func TestAnnouncement_GetDeviceInfo(t *testing.T) {
	announcement := Announcement{
		DeviceInfo: DeviceInfo{
			Alias:   "Test",
			Version: "2.3",
		},
		Protocol: "https",
		Port:     53317,
	}

	info := announcement.GetDeviceInfo()

	if info.Alias != "Test" {
		t.Errorf("Alias = %q; want 'Test'", info.Alias)
	}
	if info.Version != "2.3" {
		t.Errorf("Version = %q; want '2.3'", info.Version)
	}
}

// =============================================================================
// SenderInfo Tests
// =============================================================================

// TestSenderInfo_JSONMarshaling verifies that SenderInfo includes port and
// protocol fields as required by protocol spec Section 4.1.
func TestSenderInfo_JSONMarshaling(t *testing.T) {
	sender := SenderInfo{
		DeviceInfo: DeviceInfo{
			Alias:   "Sender",
			Version: "2.3",
		},
		Port:     53317,
		Protocol: "https",
	}

	data, err := json.Marshal(sender)
	if err != nil {
		t.Fatalf("Failed to marshal SenderInfo: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// Verify required fields
	if result["port"] != float64(53317) {
		t.Errorf("port = %v; want 53317", result["port"])
	}
	if result["protocol"] != "https" {
		t.Errorf("protocol = %v; want 'https'", result["protocol"])
	}
	if result["alias"] != "Sender" {
		t.Errorf("alias = %v; want 'Sender'", result["alias"])
	}
}

// =============================================================================
// NewDeviceInfo Tests
// =============================================================================

// TestNewDeviceInfo_SetsDefaults verifies that NewDeviceInfo sets correct default values.
func TestNewDeviceInfo_SetsDefaults(t *testing.T) {
	info := NewDeviceInfo("TestAlias", "abc123")

	if info.Alias != "TestAlias" {
		t.Errorf("Alias = %q; want 'TestAlias'", info.Alias)
	}
	if info.Fingerprint != "abc123" {
		t.Errorf("Fingerprint = %q; want 'abc123'", info.Fingerprint)
	}
	if info.Version != "2.2" {
		t.Errorf("Version = %q; want '2.2'", info.Version)
	}
	if info.DeviceModel != "LocalSend-CLI" {
		t.Errorf("DeviceModel = %q; want 'LocalSend-CLI'", info.DeviceModel)
	}
	if info.DeviceType != "headless" {
		t.Errorf("DeviceType = %q; want 'headless'", info.DeviceType)
	}
	if info.Download != false {
		t.Error("Download should be false by default")
	}
}
