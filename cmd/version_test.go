package cmd

import (
	"runtime"
	"strings"
	"testing"
)

func TestVersionStringFormat(t *testing.T) {
	v := versionString()
	// Expect "<version> <goos>/<arch>", e.g. "v1.3.0 linux/arm64".
	if !strings.HasPrefix(v, version+" ") {
		t.Errorf("versionString %q does not start with %q", v, version+" ")
	}
	if !strings.Contains(v, runtime.GOOS+"/") {
		t.Errorf("versionString %q does not contain %q", v, runtime.GOOS+"/")
	}
	if !strings.HasSuffix(v, "/"+effectiveArch()) {
		t.Errorf("versionString %q does not end with %q", v, "/"+effectiveArch())
	}
}

func TestEffectiveArchFallback(t *testing.T) {
	// With no ldflags injection, effectiveArch falls back to the raw runtime GOARCH.
	buildArchTag = ""
	defer func() { buildArchTag = "" }()
	if got := effectiveArch(); got != runtime.GOARCH {
		t.Errorf("effectiveArch() = %q, want runtime.GOARCH %q", got, runtime.GOARCH)
	}
}

func TestEffectiveArchInjected(t *testing.T) {
	// When the release build injects the tag, it is returned verbatim so the plugin can
	// compare it against getDeviceArch() (armv7 / arm64 / arm-legacy).
	buildArchTag = "arm-legacy"
	defer func() { buildArchTag = "" }()
	if got := effectiveArch(); got != "arm-legacy" {
		t.Errorf("effectiveArch() = %q, want %q", got, "arm-legacy")
	}
}
