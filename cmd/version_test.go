package cmd

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestVersionStringFormat(t *testing.T) {
	v := versionString()
	// Expect "<version> <goos>/<arch>", e.g. "vX.Y.Z linux/arm64".
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

func TestVersionMatchesPluginMetadata(t *testing.T) {
	meta, err := os.ReadFile("../lua/_meta.lua")
	if err != nil {
		t.Fatalf("read plugin metadata: %v", err)
	}

	match := regexp.MustCompile(`version\s*=\s*"([^"]+)"`).FindSubmatch(meta)
	if len(match) != 2 {
		t.Fatal("plugin metadata does not contain a version field")
	}

	if pluginVersion := string(match[1]); pluginVersion != version {
		t.Errorf("CLI version %q does not match plugin metadata version %q", version, pluginVersion)
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
