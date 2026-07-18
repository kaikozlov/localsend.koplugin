package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	runtimeSourceFixture = `package linux

func EpollWait(epfd int32) {
	Syscall6(SYS_EPOLL_PWAIT, uintptr(epfd), 0, 0, 0, 0, 0)
}
`
	pollSourceFixture = `package poll

import "syscall"

func accept(s int) (int, syscall.Sockaddr, string, error) {
	ns, sa, err := Accept4Func(s, syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC)
	if err != nil {
		return -1, nil, "accept4", err
	}
	return ns, sa, "", nil
}
`
	syscallSourceFixture = `package syscall

func Accept(fd int) (nfd int, sa Sockaddr, err error) {
	return Accept4(fd, 0)
}

func Accept4(fd int, flags int) (nfd int, sa Sockaddr, err error) {
	var rsa RawSockaddrAny
	var len _Socklen = SizeofSockaddrAny
	nfd, err = accept4(fd, &rsa, &len, flags)
	if err != nil {
		return
	}
	if len > SizeofSockaddrAny {
		panic("RawSockaddrAny too small")
	}
	sa, err = anyToSockaddr(&rsa)
	if err != nil {
		Close(nfd)
		nfd = 0
	}
	return
}
`
)

func writeRuntimeSource(t *testing.T, goroot, relativePath, source string) string {
	t.Helper()
	path := filepath.Join(goroot, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeStandardLibrarySources(t *testing.T, goroot string) (pollPath, syscallPath string) {
	t.Helper()
	pollPath = writeRuntimeSource(t, goroot, "src/internal/poll/sock_cloexec.go", pollSourceFixture)
	syscallPath = writeRuntimeSource(t, goroot, "src/syscall/syscall_linux.go", syscallSourceFixture)
	return pollPath, syscallPath
}

func TestGenerateOverlay_ReplacesEpollPwaitWithArmEpollWait(t *testing.T) {
	goroot := t.TempDir()
	originalPath := writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	writeStandardLibrarySources(t, goroot)
	outputDir := filepath.Join(t.TempDir(), "overlay")

	overlayPath, err := generateOverlay(goroot, outputDir)
	if err != nil {
		t.Fatal(err)
	}

	patched, err := os.ReadFile(filepath.Join(outputDir, patchedRuntimeSourceName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(patched), "SYS_EPOLL_PWAIT") {
		t.Fatalf("patched runtime still calls epoll_pwait:\n%s", patched)
	}
	if !strings.Contains(string(patched), "const armEpollWaitSyscall = 252") {
		t.Fatalf("patched runtime does not define the ARM epoll_wait syscall number:\n%s", patched)
	}
	if !strings.Contains(string(patched), "Syscall6(armEpollWaitSyscall,") {
		t.Fatalf("patched runtime does not call the ARM epoll_wait syscall:\n%s", patched)
	}

	var overlay struct {
		Replace map[string]string
	}
	contents, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &overlay); err != nil {
		t.Fatal(err)
	}
	if got := overlay.Replace[originalPath]; got != filepath.Join(outputDir, patchedRuntimeSourceName) {
		t.Fatalf("overlay replacement = %q, want patched source path", got)
	}
}

func TestGenerateOverlay_RestoresLegacyARMAcceptFallback(t *testing.T) {
	goroot := t.TempDir()
	writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	pollPath, syscallPath := writeStandardLibrarySources(t, goroot)
	outputDir := filepath.Join(t.TempDir(), "overlay")

	overlayPath, err := generateOverlay(goroot, outputDir)
	if err != nil {
		t.Fatal(err)
	}

	patchedPoll, err := os.ReadFile(filepath.Join(outputDir, "sock_cloexec.go.overlay"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patchedPoll), "case syscall.ENOSYS:") ||
		!strings.Contains(string(patchedPoll), "AcceptFunc(s)") {
		t.Fatalf("patched poll source does not restore the legacy accept fallback:\n%s", patchedPoll)
	}

	patchedSyscall, err := os.ReadFile(filepath.Join(outputDir, "syscall_linux.go.overlay"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patchedSyscall), "SYS_ACCEPT") ||
		!strings.Contains(string(patchedSyscall), "nfd, err = legacyAccept(fd, &rsa, &len)") {
		t.Fatalf("patched syscall source does not invoke legacy accept:\n%s", patchedSyscall)
	}

	var overlay struct {
		Replace map[string]string
	}
	contents, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &overlay); err != nil {
		t.Fatal(err)
	}
	if got := overlay.Replace[pollPath]; got != filepath.Join(outputDir, "sock_cloexec.go.overlay") {
		t.Fatalf("poll overlay replacement = %q, want patched source path", got)
	}
	if got := overlay.Replace[syscallPath]; got != filepath.Join(outputDir, "syscall_linux.go.overlay") {
		t.Fatalf("syscall overlay replacement = %q, want patched source path", got)
	}
}

func TestGenerateOverlay_AddsNonARMCompileGuard(t *testing.T) {
	goroot := t.TempDir()
	runtimePath := writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	writeStandardLibrarySources(t, goroot)
	outputDir := filepath.Join(t.TempDir(), "overlay")

	overlayPath, err := generateOverlay(goroot, outputDir)
	if err != nil {
		t.Fatal(err)
	}

	var overlay struct {
		Replace map[string]string
	}
	contents, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &overlay); err != nil {
		t.Fatal(err)
	}
	guardPath := filepath.Join(filepath.Dir(runtimePath), guardVirtualSourceName)
	if got := overlay.Replace[guardPath]; got != filepath.Join(outputDir, patchedGuardSourceName) {
		t.Fatalf("guard overlay replacement = %q, want patched guard path", got)
	}
	guard, err := os.ReadFile(filepath.Join(outputDir, patchedGuardSourceName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(guard), "//go:build !arm") ||
		!strings.Contains(string(guard), "armcompatOverlayRequiresGOARCHArm") {
		t.Fatalf("guard does not reject non-ARM builds:\n%s", guard)
	}
}

func TestGenerateOverlay_RejectsUnexpectedRuntimeSource(t *testing.T) {
	goroot := t.TempDir()
	writeRuntimeSource(t, goroot, currentRuntimeSourcePath, "package linux\n")
	writeStandardLibrarySources(t, goroot)

	_, err := generateOverlay(goroot, filepath.Join(t.TempDir(), "overlay"))
	if err == nil {
		t.Fatal("generateOverlay() succeeded without the expected epoll_pwait call")
	}
}

func TestGenerateOverlay_RejectsUnexpectedPollSource(t *testing.T) {
	goroot := t.TempDir()
	writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	writeRuntimeSource(t, goroot, "src/internal/poll/sock_cloexec.go", "package poll\n")
	writeRuntimeSource(t, goroot, "src/syscall/syscall_linux.go", syscallSourceFixture)

	_, err := generateOverlay(goroot, filepath.Join(t.TempDir(), "overlay"))
	if err == nil {
		t.Fatal("generateOverlay() succeeded without the expected poll accept4 implementation")
	}
}

func TestGenerateOverlay_RejectsUnexpectedSyscallSource(t *testing.T) {
	goroot := t.TempDir()
	writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	writeRuntimeSource(t, goroot, "src/internal/poll/sock_cloexec.go", pollSourceFixture)
	writeRuntimeSource(t, goroot, "src/syscall/syscall_linux.go", "package syscall\n")

	_, err := generateOverlay(goroot, filepath.Join(t.TempDir(), "overlay"))
	if err == nil {
		t.Fatal("generateOverlay() succeeded without the expected syscall Accept implementation")
	}
}
