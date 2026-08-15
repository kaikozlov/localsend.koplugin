package constants

import "testing"

func TestDeviceTypeToV3_UsesSignalingAndV3HTTPCasing(t *testing.T) {
	tests := map[string]string{
		"mobile": "MOBILE", "desktop": "DESKTOP", "web": "WEB",
		"headless": "HEADLESS", "server": "SERVER",
	}
	for input, want := range tests {
		if got := DeviceTypeToV3(input); got != want {
			t.Errorf("DeviceTypeToV3(%q) = %q; want %q", input, got, want)
		}
	}
}

func TestV3Paths_MatchPinnedOfficialServerSurface(t *testing.T) {
	if NoncePathV3 != "/api/localsend/v3/nonce" {
		t.Fatalf("NoncePathV3 = %q", NoncePathV3)
	}
	if RegisterPathV3 != "/api/localsend/v3/register" {
		t.Fatalf("RegisterPathV3 = %q", RegisterPathV3)
	}
}
