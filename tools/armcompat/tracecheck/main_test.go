package main

import (
	"strings"
	"testing"
)

const completeTrace = `100 epoll_create1(524288) = -1 errno=38 (Function not implemented)
100 epoll_create(1) = 6
100 eventfd2(0,526336) = -1 errno=38 (Function not implemented)
100 eventfd(0) = 7
101 accept4(9,123,456,526336) = -1 errno=38 (Function not implemented)
101 accept(9,123,456) = 10
101 epoll_wait(6,123,128,0) = 1
101 getrandom(123,8,0) = -1 errno=38 (Function not implemented)
101 prlimit64(0,RLIMIT_NOFILE,NULL,123) = -1 errno=38 (Function not implemented)
101 pipe2(123,524288) = -1 errno=38 (Function not implemented)
101 pipe(123) = 0
102 dup3(14,0,0) = -1 errno=38 (Function not implemented)
102 dup2(14,0) = 0
`

func TestCheckTrace_AcceptsInterleavedQEMUResults(t *testing.T) {
	trace := strings.Replace(
		completeTrace,
		"101 pipe2(123,524288) = -1 errno=38 (Function not implemented)",
		"101 pipe2(123,524288)102 sched_yield() = 0\n = -1 errno=38 (Function not implemented)",
		1,
	)

	if err := checkTrace([]byte(trace)); err != nil {
		t.Fatalf("checkTrace() rejected an interleaved QEMU trace: %v", err)
	}
}

func TestCheckTrace_RejectsMissingLegacyFallback(t *testing.T) {
	trace := strings.Replace(completeTrace, "101 pipe(123) = 0\n", "", 1)

	err := checkTrace([]byte(trace))
	if err == nil || !strings.Contains(err.Error(), "pipe") {
		t.Fatalf("checkTrace() error = %v, want missing pipe fallback", err)
	}
}

func TestCheckTrace_RejectsEpollPwait(t *testing.T) {
	trace := completeTrace + "101 epoll_pwait(6,123,128,0,NULL,0) = 0\n"

	err := checkTrace([]byte(trace))
	if err == nil || !strings.Contains(err.Error(), "epoll_pwait") {
		t.Fatalf("checkTrace() error = %v, want epoll_pwait rejection", err)
	}
}

func TestCheckTraceWithOptions_AllowsMissingDup2FallbackOnlyWhenExplicitlySkipped(t *testing.T) {
	trace := strings.Replace(
		completeTrace,
		"102 dup3(14,0,0) = -1 errno=38 (Function not implemented)\n102 dup2(14,0) = 0\n",
		"102 dup3(14,0,0) = 0\n",
		1,
	)

	if err := checkTraceWithOptions([]byte(trace), checkOptions{skipDup2Fallback: true}); err != nil {
		t.Fatalf("checkTraceWithOptions() rejected explicit dup2 skip: %v", err)
	}
}

func TestCheckTraceWithOptions_Dup2SkipStillRequiresEveryOtherFallback(t *testing.T) {
	trace := strings.Replace(completeTrace, "101 pipe(123) = 0\n", "", 1)

	err := checkTraceWithOptions([]byte(trace), checkOptions{skipDup2Fallback: true})
	if err == nil || !strings.Contains(err.Error(), "pipe") {
		t.Fatalf("checkTraceWithOptions() error = %v, want missing pipe fallback", err)
	}
}
