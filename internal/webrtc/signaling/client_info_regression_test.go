package signaling

import "testing"

func TestNewClientInfo_UsesOfficialUppercaseDeviceType(t *testing.T) {
	if got := NewClientInfo("review", "token").DeviceType; got != "HEADLESS" {
		t.Fatalf("DeviceType = %q; want HEADLESS", got)
	}
}
