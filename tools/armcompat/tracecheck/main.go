// Command tracecheck validates the guest syscall paths exercised by the
// legacy-ARM compatibility audit.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type syscallPair struct {
	modern string
	legacy string
}

var fallbackPairs = []syscallPair{
	{modern: "epoll_create1", legacy: "epoll_create"},
	{modern: "eventfd2", legacy: "eventfd"},
	{modern: "accept4", legacy: "accept"},
	{modern: "pipe2", legacy: "pipe"},
	{modern: "dup3", legacy: "dup2"},
}

func syscallCall(name string) string {
	return name + "("
}

type checkOptions struct {
	skipDup2Fallback bool
}

func checkTrace(trace []byte) error {
	return checkTraceWithOptions(trace, checkOptions{})
}

func checkTraceWithOptions(trace []byte, options checkOptions) error {
	contents := string(trace)
	if strings.Contains(contents, syscallCall("epoll_pwait")) {
		return errors.New("guest trace still calls epoll_pwait")
	}
	if !strings.Contains(contents, syscallCall("epoll_wait")) {
		return errors.New("guest trace did not call epoll_wait")
	}

	for _, pair := range fallbackPairs {
		modernCall := syscallCall(pair.modern)
		modernAt := strings.Index(contents, modernCall)
		if modernAt < 0 {
			return fmt.Errorf("guest trace did not call modern %s probe", pair.modern)
		}
		if options.skipDup2Fallback && pair.legacy == "dup2" {
			continue
		}
		if !strings.Contains(contents[modernAt+len(modernCall):], syscallCall(pair.legacy)) {
			return fmt.Errorf("guest trace did not call legacy %s fallback after %s", pair.legacy, pair.modern)
		}
	}

	// These calls have fallbacks outside the ARM overlay. Their presence proves
	// the audited workflow exercised the paths; unlike the paired checks above,
	// QEMU may satisfy them without issuing the filtered host syscall.
	for _, probe := range []string{"getrandom", "prlimit64"} {
		if !strings.Contains(contents, syscallCall(probe)) {
			return fmt.Errorf("guest trace did not call %s", probe)
		}
	}
	return nil
}

func main() {
	args := os.Args[1:]
	options := checkOptions{}
	if len(args) == 2 && args[0] == "--skip-dup2-fallback" {
		options.skipDup2Fallback = true
		args = args[1:]
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tracecheck [--skip-dup2-fallback] TRACE")
		os.Exit(2)
	}

	trace, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "tracecheck:", err)
		os.Exit(1)
	}
	if err := checkTraceWithOptions(trace, options); err != nil {
		fmt.Fprintln(os.Stderr, "tracecheck:", err)
		os.Exit(1)
	}
}
