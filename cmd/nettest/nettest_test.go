package nettest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
)

// runOnce runs a short nettest, asserting structural validity only (the loopback
// result is environment-dependent and not asserted here).
func runOnce(t *testing.T, d time.Duration) Result {
	t.Helper()
	res := Run(d)
	if res.DurationMS <= 0 {
		t.Errorf("DurationMS = %d, want > 0", res.DurationMS)
	}
	if res.Peers < 0 {
		t.Errorf("Peers = %d, want >= 0", res.Peers)
	}
	return res
}

func TestRun_JoinsGroupWithoutBindError(t *testing.T) {
	res := runOnce(t, 1*time.Second)
	if res.BindError != "" {
		t.Fatalf("could not bind/join the multicast group: %s", res.BindError)
	}
}

func TestRun_JSONShape(t *testing.T) {
	res := runOnce(t, 1*time.Second)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"loopback", "peers", "udp_peers", "register_peers",
		"register_bind_error", "seen_aliases", "local_ips", "duration_ms"} {
		if _, ok := m[key]; !ok {
			t.Errorf("result JSON missing key %q", key)
		}
	}
}

// On a Linux host with multicast loopback enabled (the default, and what the
// koplugin-dev test container provides), we expect to receive our own probe.
// Other hosts (e.g. macOS dev machines) legitimately fail this, so only assert
// on Linux.
func TestRun_LoopbackSucceedsInContainer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("multicast loopback is only asserted on Linux (GOOS=%s)", runtime.GOOS)
	}
	res := runOnce(t, 2*time.Second)
	if res.BindError != "" {
		t.Skipf("multicast unavailable in this environment: %s", res.BindError)
	}
	if !res.Loopback {
		t.Errorf("Loopback = false, want true (multicast loopback should work on this host)")
	}
}

// The register listener must parse register POSTs, reject anything else, and
// expose the source address used by Run's peer filter.
func TestRegisterListener_AcceptsOnlyRegisterPosts(t *testing.T) {
	devInfo := models.NewDeviceInfo("nettest-test", "SELF-FP")
	type seen struct{ fingerprint, alias, remoteIP string }
	var mu sync.Mutex
	var got []seen
	ln, err := startRegisterListener(devInfo, func(fingerprint, alias, remoteIP string) {
		mu.Lock()
		got = append(got, seen{fingerprint, alias, remoteIP})
		mu.Unlock()
	})
	if err != nil {
		t.Skipf("could not bind the discovery port in this environment: %v", err)
	}
	defer func() { _ = ln.Close() }()

	base := fmt.Sprintf("http://127.0.0.1:%d", constants.DefaultPort)
	body := `{"alias":"peer","version":"2.1","fingerprint":"REG-FP","port":53317,"protocol":"http"}`

	// Valid register POST → recorded, HTTP 200.
	resp, err := http.Post(base+"/api/localsend/v2/register", "application/json",
		bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("register POST failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("register POST status = %d, want 200", resp.StatusCode)
	}

	// GET to the register path and POST to another path → rejected, not recorded.
	resp, err = http.Get(base + "/api/localsend/v2/register")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET register status = %d, want 404", resp.StatusCode)
	}
	resp, err = http.Post(base+"/some/other/path", "application/json",
		bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("POST to other path failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST other path status = %d, want 404", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("recorded %d peers, want exactly 1 (the valid register POST)", len(got))
	}
	if got[0].fingerprint != "REG-FP" {
		t.Errorf("recorded fingerprint = %q, want %q", got[0].fingerprint, "REG-FP")
	}
	if got[0].alias != "peer" {
		t.Errorf("recorded alias = %q, want %q", got[0].alias, "peer")
	}
	if got[0].remoteIP != "127.0.0.1" {
		t.Errorf("recorded remoteIP = %q, want %q", got[0].remoteIP, "127.0.0.1")
	}
	if shouldCountPeer(got[0].fingerprint, got[0].remoteIP, "SELF-FP", map[string]bool{}) {
		t.Error("loopback register should not count as a LAN peer")
	}
}

func TestShouldCountPeer(t *testing.T) {
	localSet := map[string]bool{"192.168.1.50": true}
	cases := []struct {
		name        string
		fingerprint string
		remoteIP    string
		want        bool
	}{
		{"LAN peer", "PEER-FP", "192.168.1.99", true},
		{"empty fingerprint", "", "192.168.1.99", false},
		{"own fingerprint", "SELF-FP", "192.168.1.99", false},
		{"loopback source", "PEER-FP", "127.0.0.1", false},
		{"ipv6 loopback source", "PEER-FP", "::1", false},
		{"own interface address", "PEER-FP", "192.168.1.50", false},
		{"unknown source counts", "PEER-FP", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldCountPeer(tc.fingerprint, tc.remoteIP, "SELF-FP", localSet); got != tc.want {
				t.Errorf("shouldCountPeer(%q, %q) = %v, want %v", tc.fingerprint, tc.remoteIP, got, tc.want)
			}
		})
	}
}

func TestAliasesFrom(t *testing.T) {
	cases := []struct {
		name      string
		peers     map[string]string
		limit     int
		wantSet   []string // expected aliases as an unordered set
		wantCount int      // -1 to use len(wantSet); otherwise assert exact count only (capped case)
	}{
		{"empty", map[string]string{}, 10, []string{}, -1},
		{"single", map[string]string{"fp1": "Phone"}, 10, []string{"Phone"}, -1},
		{"dedup by alias (reinstall)", map[string]string{"fp1": "Phone", "fp2": "Phone"}, 10, []string{"Phone"}, -1},
		{"distinct aliases preserved", map[string]string{"fp1": "Phone", "fp2": "Laptop"}, 10, []string{"Phone", "Laptop"}, -1},
		{"empty alias becomes unknown", map[string]string{"fp1": ""}, 10, []string{"(unknown)"}, -1},
		{"capped at limit", map[string]string{"a": "A", "b": "B", "c": "C", "d": "D"}, 2, nil, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := aliasesFrom(tc.peers, tc.limit)
			if tc.wantCount >= 0 {
				if len(got) != tc.wantCount {
					t.Fatalf("aliasesFrom len = %d, want %d (got %v)", len(got), tc.wantCount, got)
				}
				return
			}
			if len(got) != len(tc.wantSet) {
				t.Fatalf("aliasesFrom len = %d, want %d (got %v)", len(got), len(tc.wantSet), got)
			}
			want := make(map[string]bool, len(tc.wantSet))
			for _, a := range tc.wantSet {
				want[a] = true
			}
			for _, a := range got {
				if !want[a] {
					t.Errorf("aliasesFrom returned unexpected %q (want set %v)", a, tc.wantSet)
				}
			}
		})
	}
}

func TestUnhealthy(t *testing.T) {
	cases := []struct {
		name string
		res  Result
		want bool
	}{
		{"bind error", Result{BindError: "permission denied"}, true},
		{"loopback failed, no peers", Result{Loopback: false, Peers: 0}, true},
		{"loopback failed but peers seen", Result{Loopback: false, Peers: 1}, false},
		{"loopback ok, no peers", Result{Loopback: true, Peers: 0}, false},
		{"loopback ok, peers", Result{Loopback: true, Peers: 3}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unhealthy(tc.res); got != tc.want {
				t.Errorf("unhealthy(%+v) = %v, want %v", tc.res, got, tc.want)
			}
		})
	}
}
