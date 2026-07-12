package cmd

import (
	"fmt"
	"runtime"
)

var version = "v1.4.0"

// buildArchTag is injected at build time via -ldflags "-X localsend-cli/cmd.buildArchTag=<arch>"
// by the release builds (justfile `release` recipe, .github/workflows/koplugin.yaml) using the same vocabulary
// as the KOReader plugin's getDeviceArch() (armv7 / arm64 / arm-legacy). A plain `go build`
// (no ldflags, e.g. local dev) leaves it empty and effectiveArch() falls back to runtime.GOARCH.
var buildArchTag = ""

// effectiveArch reports the architecture the binary was built for, in the plugin's
// getDeviceArch() vocabulary (armv7 / arm64 / arm-legacy) when injected at build time,
// otherwise the raw runtime.GOARCH. The in-plugin diagnostics compares this to the device
// arch to flag a mismatched package.
func effectiveArch() string {
	if buildArchTag != "" {
		return buildArchTag
	}
	return runtime.GOARCH
}

// versionString renders "<version> <goos>/<arch>" for `localsend --version` and the
// in-plugin diagnostics (which parse the arch token to compare against the device).
func versionString() string {
	return fmt.Sprintf("%s %s/%s", version, runtime.GOOS, effectiveArch())
}
