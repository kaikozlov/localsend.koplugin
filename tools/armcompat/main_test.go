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

func EpollCreate1(flags int32) (fd int32, errno uintptr) {
	r1, _, e := Syscall6(SYS_EPOLL_CREATE1, uintptr(flags), 0, 0, 0, 0, 0)
	return int32(r1), e
}

func EpollWait(epfd int32) {
	Syscall6(SYS_EPOLL_PWAIT, uintptr(epfd), 0, 0, 0, 0, 0)
}

func Eventfd(initval, flags int32) (fd int32, errno uintptr) {
	r1, _, e := Syscall6(SYS_EVENTFD2, uintptr(initval), uintptr(flags), 0, 0, 0, 0)
	return int32(r1), e
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

func Pipe2(p []int, flags int) error {
	if len(p) != 2 {
		return EINVAL
	}
	var pp [2]_C_int
	err := pipe2(&pp, flags)
	if err == nil {
		p[0] = int(pp[0])
		p[1] = int(pp[1])
	}
	return err
}

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
	forkPipeSourceFixture = `package syscall

func forkExecPipe(p []int) error {
	return Pipe2(p, O_CLOEXEC)
}
`
	execLinuxSourceFixture = `package syscall

func remap(pipe, nextfd int, fd []int) Errno {
	var err1 Errno
	if pipe < nextfd {
		_, _, err1 = RawSyscall(SYS_DUP3, uintptr(pipe), uintptr(nextfd), O_CLOEXEC)
		if err1 != 0 {
			goto childerror
		}
	}
	for i := 0; i < len(fd); i++ {
		if fd[i] >= 0 && fd[i] < i {
			_, _, err1 = RawSyscall(SYS_DUP3, uintptr(fd[i]), uintptr(nextfd), O_CLOEXEC)
			if err1 != 0 {
				goto childerror
			}
		}
	}
	for i := 0; i < len(fd); i++ {
		if fd[i] == i {
			continue
		}
		_, _, err1 = RawSyscall(SYS_DUP3, uintptr(fd[i]), uintptr(i), 0)
		if err1 != 0 {
			goto childerror
		}
	}
childerror:
	return err1
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
	writeRuntimeSource(t, goroot, "src/syscall/forkpipe2.go", forkPipeSourceFixture)
	writeRuntimeSource(t, goroot, "src/syscall/exec_linux.go", execLinuxSourceFixture)
	return pollPath, syscallPath
}

func TestGenerateOverlay_RestoresLegacyARMNetpollSyscalls(t *testing.T) {
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
	for _, expected := range []string{
		"armEpollCreateSyscall = 250",
		"armEpollWaitSyscall   = 252",
		"armEventfdSyscall     = 351",
		"Syscall6(armEpollCreateSyscall, 1,",
		"Syscall6(armEpollWaitSyscall,",
		"Syscall6(armEventfdSyscall, uintptr(initval),",
		"Syscall6(SYS_FCNTL, uintptr(fd), armFSetFD, armFDCloexec,",
		"Syscall6(SYS_FCNTL, uintptr(fd), armFSetFL,",
		"Close(int(fd))",
	} {
		if !strings.Contains(string(patched), expected) {
			t.Fatalf("patched runtime source is missing %q:\n%s", expected, patched)
		}
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

func TestGenerateOverlay_RestoresLegacyARMProcessSyscalls(t *testing.T) {
	goroot := t.TempDir()
	writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	writeStandardLibrarySources(t, goroot)
	outputDir := filepath.Join(t.TempDir(), "overlay")

	overlayPath, err := generateOverlay(goroot, outputDir)
	if err != nil {
		t.Fatal(err)
	}

	patchedSyscall, err := os.ReadFile(filepath.Join(outputDir, "syscall_linux.go.overlay"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"func legacyPipe(", "SYS_PIPE", "F_SETFD", "FD_CLOEXEC", "Close(p[0])"} {
		if !strings.Contains(string(patchedSyscall), expected) {
			t.Fatalf("patched syscall source is missing %q:\n%s", expected, patchedSyscall)
		}
	}

	patchedForkPipe, err := os.ReadFile(filepath.Join(outputDir, "forkpipe2.go.overlay"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patchedForkPipe), "if err != ENOSYS") ||
		!strings.Contains(string(patchedForkPipe), "return legacyPipe(p)") {
		t.Fatalf("patched fork pipe source does not fall back after pipe2 ENOSYS:\n%s", patchedForkPipe)
	}

	patchedExec, err := os.ReadFile(filepath.Join(outputDir, "exec_linux.go.overlay"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(patchedExec), "SYS_DUP2") != 3 ||
		strings.Count(string(patchedExec), "err1 == ENOSYS") != 3 ||
		strings.Count(string(patchedExec), "F_SETFD, FD_CLOEXEC") != 2 {
		t.Fatalf("patched exec source does not restore all dup2 fallbacks:\n%s", patchedExec)
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
	forkPipePath := filepath.Join(goroot, "src/syscall/forkpipe2.go")
	if got := overlay.Replace[forkPipePath]; got != filepath.Join(outputDir, "forkpipe2.go.overlay") {
		t.Fatalf("fork pipe overlay replacement = %q, want patched source path", got)
	}
	execPath := filepath.Join(goroot, "src/syscall/exec_linux.go")
	if got := overlay.Replace[execPath]; got != filepath.Join(outputDir, "exec_linux.go.overlay") {
		t.Fatalf("exec overlay replacement = %q, want patched source path", got)
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
	writeStandardLibrarySources(t, goroot)
	writeRuntimeSource(t, goroot, "src/syscall/syscall_linux.go", "package syscall\n")

	_, err := generateOverlay(goroot, filepath.Join(t.TempDir(), "overlay"))
	if err == nil {
		t.Fatal("generateOverlay() succeeded without the expected syscall implementations")
	}
}

func TestGenerateOverlay_RejectsUnexpectedForkPipeSource(t *testing.T) {
	goroot := t.TempDir()
	writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	writeStandardLibrarySources(t, goroot)
	writeRuntimeSource(t, goroot, "src/syscall/forkpipe2.go", "package syscall\n")

	_, err := generateOverlay(goroot, filepath.Join(t.TempDir(), "overlay"))
	if err == nil {
		t.Fatal("generateOverlay() succeeded without the expected forkExecPipe implementation")
	}
}

func TestGenerateOverlay_RejectsUnexpectedExecSource(t *testing.T) {
	goroot := t.TempDir()
	writeRuntimeSource(t, goroot, currentRuntimeSourcePath, runtimeSourceFixture)
	writeStandardLibrarySources(t, goroot)
	writeRuntimeSource(t, goroot, "src/syscall/exec_linux.go", "package syscall\n")

	_, err := generateOverlay(goroot, filepath.Join(t.TempDir(), "overlay"))
	if err == nil {
		t.Fatal("generateOverlay() succeeded without the expected dup3 implementations")
	}
}
