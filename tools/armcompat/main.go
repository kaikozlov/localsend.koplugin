// Command armcompat creates a Go build overlay for 32-bit ARM release
// binaries. It replaces epoll_pwait with epoll_wait and restores Go's former
// ARM fallback from accept4 to accept for older Kindle kernels.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	currentRuntimeSourcePath = "src/internal/runtime/syscall/linux/syscall_linux.go"
	pollSourcePath           = "src/internal/poll/sock_cloexec.go"
	syscallSourcePath        = "src/syscall/syscall_linux.go"
	patchedRuntimeSourceName = "runtime_syscall_linux.go.overlay"
	patchedPollSourceName    = "sock_cloexec.go.overlay"
	patchedSyscallSourceName = "syscall_linux.go.overlay"
	patchedGuardSourceName   = "arm_only_guard.go.overlay"
	guardVirtualSourceName   = "armcompat_overlay_guard.go"
	overlayFileName          = "overlay.json"

	// __NR_epoll_wait for the 32-bit ARM EABI. This overlay is only passed to
	// GOARCH=arm builds; syscall numbers differ on other architectures.
	armEpollWaitSyscall = 252
)

// The overlay contains ARM EABI syscall numbers and must never be reused by a
// non-ARM build. The virtual file is excluded on GOARCH=arm and deliberately
// fails compilation everywhere else with a descriptive undefined identifier.
const armOnlyGuardSource = `//go:build !arm

package linux

var _ = armcompatOverlayRequiresGOARCHArm
`

type overlayConfig struct {
	Replace map[string]string
}

func findRuntimeSource(goroot string) (string, error) {
	return requiredSource(goroot, currentRuntimeSourcePath)
}

func patchRuntimeSource(source []byte) ([]byte, error) {
	text := string(source)
	call := "Syscall6(SYS_EPOLL_PWAIT, uintptr(epfd),"
	if strings.Count(text, call) != 1 {
		return nil, errors.New("go runtime source does not contain exactly one expected epoll_pwait call")
	}
	function := "func EpollWait("
	if strings.Count(text, function) != 1 {
		return nil, errors.New("go runtime source does not contain exactly one EpollWait function")
	}

	text = strings.Replace(text, function,
		fmt.Sprintf("const armEpollWaitSyscall = %d\n\n%s", armEpollWaitSyscall, function), 1)
	text = strings.Replace(text, call, "Syscall6(armEpollWaitSyscall, uintptr(epfd),", 1)
	return []byte(text), nil
}

const currentPollAccept = `func accept(s int) (int, syscall.Sockaddr, string, error) {
	ns, sa, err := Accept4Func(s, syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC)
	if err != nil {
		return -1, nil, "accept4", err
	}
	return ns, sa, "", nil
}`

const legacyPollAccept = `func accept(s int) (int, syscall.Sockaddr, string, error) {
	ns, sa, err := Accept4Func(s, syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC)
	switch err {
	case nil:
		return ns, sa, "", nil
	default:
		return -1, sa, "accept4", err
	case syscall.ENOSYS:
	case syscall.EINVAL:
	case syscall.EACCES:
	case syscall.EFAULT:
	}

	// The listening descriptor is normally nonblocking, so this accept call
	// will not block while CloseOnExec is applied. This is the ARM fallback
	// used by Go 1.23 before Go raised its minimum Linux version.
	ns, sa, err = AcceptFunc(s)
	if err == nil {
		syscall.CloseOnExec(ns)
	}
	if err != nil {
		return -1, nil, "accept", err
	}
	if err = syscall.SetNonblock(ns, true); err != nil {
		CloseFunc(ns)
		return -1, nil, "setnonblock", err
	}
	return ns, sa, "", nil
}`

func patchPollSource(source []byte) ([]byte, error) {
	text := string(source)
	if strings.Count(text, currentPollAccept) != 1 {
		return nil, errors.New("go poll source does not contain exactly one expected accept4 implementation")
	}
	return []byte(strings.Replace(text, currentPollAccept, legacyPollAccept, 1)), nil
}

const currentSyscallAccept = `func Accept(fd int) (nfd int, sa Sockaddr, err error) {
	return Accept4(fd, 0)
}`

const legacySyscallAccept = `func legacyAccept(s int, rsa *RawSockaddrAny, addrlen *_Socklen) (fd int, err error) {
	r0, _, errno := Syscall6(SYS_ACCEPT, uintptr(s), uintptr(unsafe.Pointer(rsa)), uintptr(unsafe.Pointer(addrlen)), 0, 0, 0)
	fd = int(r0)
	if errno != 0 {
		err = errnoErr(errno)
	}
	return
}

func Accept(fd int) (nfd int, sa Sockaddr, err error) {
	var rsa RawSockaddrAny
	var len _Socklen = SizeofSockaddrAny
	// internal/poll already tried accept4 before reaching this ARM-only
	// compatibility path. Use the syscall available on pre-2.6.36 ARM.
	nfd, err = legacyAccept(fd, &rsa, &len)
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
}`

func patchSyscallSource(source []byte) ([]byte, error) {
	text := string(source)
	if strings.Count(text, currentSyscallAccept) != 1 {
		return nil, errors.New("go syscall source does not contain exactly one expected Accept implementation")
	}
	return []byte(strings.Replace(text, currentSyscallAccept, legacySyscallAccept, 1)), nil
}

func requiredSource(goroot, relativePath string) (string, error) {
	path := filepath.Join(goroot, filepath.FromSlash(relativePath))
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("find Go source %s: %w", relativePath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("go source %s is a directory", relativePath)
	}
	return filepath.Abs(path)
}

func readAndPatch(path, description string, patch func([]byte) ([]byte, error)) ([]byte, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s source: %w", description, err)
	}
	patched, err := patch(source)
	if err != nil {
		return nil, fmt.Errorf("patch %s source: %w", description, err)
	}
	return patched, nil
}

func generateOverlay(goroot, outputDir string) (string, error) {
	runtimePath, err := findRuntimeSource(goroot)
	if err != nil {
		return "", err
	}
	pollPath, err := requiredSource(goroot, pollSourcePath)
	if err != nil {
		return "", err
	}
	syscallPath, err := requiredSource(goroot, syscallSourcePath)
	if err != nil {
		return "", err
	}

	patchedRuntime, err := readAndPatch(runtimePath, "Go runtime", patchRuntimeSource)
	if err != nil {
		return "", err
	}
	patchedPoll, err := readAndPatch(pollPath, "Go poll", patchPollSource)
	if err != nil {
		return "", err
	}
	patchedSyscall, err := readAndPatch(syscallPath, "Go syscall", patchSyscallSource)
	if err != nil {
		return "", err
	}

	outputDir, err = filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}

	guardPath := filepath.Join(filepath.Dir(runtimePath), guardVirtualSourceName)
	patches := []struct {
		originalPath string
		outputName   string
		contents     []byte
	}{
		{runtimePath, patchedRuntimeSourceName, patchedRuntime},
		{pollPath, patchedPollSourceName, patchedPoll},
		{syscallPath, patchedSyscallSourceName, patchedSyscall},
		{guardPath, patchedGuardSourceName, []byte(armOnlyGuardSource)},
	}
	replacements := make(map[string]string, len(patches))
	for _, sourcePatch := range patches {
		patchedPath := filepath.Join(outputDir, sourcePatch.outputName)
		if err := os.WriteFile(patchedPath, sourcePatch.contents, 0o644); err != nil {
			return "", fmt.Errorf("write patched source %s: %w", sourcePatch.outputName, err)
		}
		replacements[sourcePatch.originalPath] = patchedPath
	}

	overlay := overlayConfig{Replace: replacements}
	contents, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode build overlay: %w", err)
	}
	contents = append(contents, '\n')
	overlayPath := filepath.Join(outputDir, overlayFileName)
	if err := os.WriteFile(overlayPath, contents, 0o644); err != nil {
		return "", fmt.Errorf("write build overlay: %w", err)
	}
	return overlayPath, nil
}

func goRoot() (string, error) {
	output, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("run go env GOROOT: %w", err)
	}
	goroot := strings.TrimSpace(string(output))
	if goroot == "" {
		return "", errors.New("go env GOROOT returned an empty path")
	}
	return goroot, nil
}

func main() {
	outputDir := flag.String("output-dir", "", "directory for the patched Go sources and overlay JSON")
	flag.Parse()
	if *outputDir == "" {
		fmt.Fprintln(os.Stderr, "armcompat: -output-dir is required")
		os.Exit(2)
	}
	goroot, err := goRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "armcompat:", err)
		os.Exit(1)
	}
	overlayPath, err := generateOverlay(goroot, *outputDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "armcompat:", err)
		os.Exit(1)
	}
	fmt.Println(overlayPath)
}
