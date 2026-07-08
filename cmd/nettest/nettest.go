package nettest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"localsend-cli/internal/localsend/constants"
	lsutils "localsend-cli/internal/localsend/utils"
	"localsend-cli/internal/models"
	"localsend-cli/internal/utils"
)

// Multicast discovery group used by LocalSend (protocol spec Section 3.1).
var multicastAddr = &net.UDPAddr{
	IP:   net.ParseIP("224.0.0.167"),
	Port: constants.DefaultPort,
}

var (
	duration   int // total probe/listen window in seconds
	jsonOutput bool
)

// Result is the machine-readable output consumed by the in-plugin troubleshooting wizard.
type Result struct {
	Loopback          bool     `json:"loopback"`            // did we receive our own multicast probe?
	BindError         string   `json:"bind_error"`          // non-empty if we could not bind/join the multicast group
	Peers             int      `json:"peers"`               // distinct non-local LocalSend devices seen (union of both paths)
	UDPPeers          int      `json:"udp_peers"`           // peers seen via UDP announcements
	RegisterPeers     int      `json:"register_peers"`      // peers seen via HTTP register calls
	RegisterBindError string   `json:"register_bind_error"` // non-empty if the TCP register listener could not bind
	LocalIPs          []string `json:"local_ips"`           // this device's IPv4 addresses
	SeenAliases       []string `json:"seen_aliases"`        // device names of responding peers (capped)
	DurationMS        int64    `json:"duration_ms"`         // how long the test ran
}

var Cmd = &cobra.Command{
	Use:   "nettest",
	Short: "Diagnose LocalSend multicast discovery",
	Long: `nettest checks whether this device can send and receive LocalSend multicast
discovery packets (self-loopback) and whether other LocalSend devices are on
the LAN. It announces itself and counts peers that respond either with a UDP
announcement or with an HTTP register call. The in-plugin "Test discovery"
troubleshooting action uses the result to attribute "device not discovered"
problems.`,
	Run: func(cmd *cobra.Command, args []string) {
		res := Run(time.Duration(duration) * time.Second)
		if jsonOutput {
			out, _ := json.Marshal(res)
			fmt.Println(string(out))
		} else {
			printHuman(res)
		}
		if unhealthy(res) {
			os.Exit(1)
		}
	},
}

// Run performs the multicast self-loopback test and counts advertising peers over the
// given duration. A separate sender socket is used so the listening socket (a group
// member) actually receives the probe back via host multicast loopback — same-socket
// loopback is unreliable across kernels. Exported so it can be exercised by tests.
// unhealthy reports whether the result indicates discovery is broken on this device/network:
// the multicast group could not be bound, or loopback failed with no peers seen (peers>0
// proves multicast works regardless of loopback). Used to set the process exit code.
func unhealthy(res Result) bool {
	return res.BindError != "" || (!res.Loopback && res.Peers == 0)
}

func Run(d time.Duration) Result {
	start := time.Now()
	res := Result{LocalIPs: localIPv4s()}

	localSet := make(map[string]bool, len(res.LocalIPs))
	for _, ip := range res.LocalIPs {
		localSet[ip] = true
	}

	// Listener: joins the multicast group.
	listener, err := net.ListenMulticastUDP("udp", nil, multicastAddr)
	if err != nil {
		res.BindError = err.Error()
		res.DurationMS = time.Since(start).Milliseconds()
		return res
	}
	defer func() { _ = listener.Close() }()
	_ = listener.SetReadBuffer(2048)

	// Our own announcement so other devices see us; we filter ourselves out by fingerprint.
	devInfo := models.NewDeviceInfo("nettest", lsutils.GenFingerprint())
	anno := models.Announcement{
		DeviceInfo: devInfo,
		Protocol:   "http",
		Port:       constants.DefaultPort,
		Announce:   true,
	}
	annoBytes, _ := json.Marshal(anno)
	probeTag := []byte("LSNETTEST-" + uuid.NewString())

	// Peers are deduplicated by fingerprint, tracked per response path so the
	// diagnosis can tell "multicast receive works" apart from "another device
	// could reach us over TCP".
	var peersMu sync.Mutex
	udpPeers := make(map[string]string) // fingerprint → alias
	registerPeers := make(map[string]string)
	makeRecorder := func(seen map[string]string) func(fingerprint, alias, remoteIP string) {
		return func(fingerprint, alias, remoteIP string) {
			if !shouldCountPeer(fingerprint, remoteIP, devInfo.Fingerprint, localSet) {
				return
			}
			peersMu.Lock()
			if _, ok := seen[fingerprint]; !ok {
				if alias == "" {
					alias = "(unknown)"
				}
				seen[fingerprint] = alias
			}
			peersMu.Unlock()
		}
	}
	recordUDPPeer := makeRecorder(udpPeers)
	recordRegisterPeer := makeRecorder(registerPeers)

	// The official LocalSend app answers an announcement primarily with an HTTP
	// register POST to the announcer's TCP port; UDP responses are a fallback.
	// Listen for those too, otherwise healthy peers can show up as 0. The port is
	// free because the plugin stops the receiver before running nettest; if it is
	// taken anyway, we still have the UDP path, so this is best-effort.
	registerListener, regErr := startRegisterListener(devInfo, recordRegisterPeer)
	if regErr != nil {
		res.RegisterBindError = regErr.Error()
	} else {
		defer func() { _ = registerListener.Close() }()
	}

	// Sender: a separate UDP socket on an ephemeral source port. It does NOT join the group;
	// it just emits to it, and host multicast loopback delivers a copy to the listener.
	sender, err := net.DialUDP("udp", nil, multicastAddr)
	if err != nil {
		res.BindError = err.Error()
		res.DurationMS = time.Since(start).Milliseconds()
		return res
	}
	defer func() { _ = sender.Close() }()

	var senderWG sync.WaitGroup
	senderWG.Add(1)
	go func() {
		defer senderWG.Done()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.Now().Add(d)
		for {
			if time.Now().After(deadline) {
				return
			}
			// Probe via the ephemeral sender so the listener reliably receives it back via
			// host loopback (same-socket loopback is unreliable across kernels).
			_, _ = sender.Write(probeTag)
			// Advertise from the listener (port 53317) so other devices' register responses
			// come back to it — responses to an ephemeral source port would be lost.
			// NOTE: sending from the multicast-group-bound socket lets the kernel pick the
			// outgoing interface; on a multi-homed host (e.g. Wi-Fi + USB networking) the
			// chosen source IP may differ from the interface a peer reaches us on. The
			// UDP-announcement path still works as a fallback; this only affects which IP a
			// peer POSTs its register response to.
			_, _ = listener.WriteToUDP(annoBytes, multicastAddr)
			select {
			case <-ticker.C:
			case <-time.After(time.Until(deadline)):
				return
			}
		}
	}()

	// Reader: classify packets until the read deadline.
	_ = listener.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 2048)
	for {
		n, remote, err := listener.ReadFromUDP(buf)
		if err != nil {
			break // deadline reached
		}
		pkt := buf[:n]
		if bytes.Contains(pkt, probeTag) {
			res.Loopback = true
			continue
		}
		var a models.Announcement
		if json.Unmarshal(pkt, &a) != nil {
			continue
		}
		remoteIP := ""
		if remote != nil && remote.IP != nil {
			remoteIP = remote.IP.String()
		}
		recordUDPPeer(a.Fingerprint, a.Alias, remoteIP)
	}

	senderWG.Wait()
	if registerListener != nil {
		_ = registerListener.Close()
	}
	peersMu.Lock()
	res.UDPPeers = len(udpPeers)
	res.RegisterPeers = len(registerPeers)
	union := make(map[string]string, len(udpPeers)+len(registerPeers))
	for fp, alias := range udpPeers {
		union[fp] = alias
	}
	for fp, alias := range registerPeers {
		if _, ok := union[fp]; !ok {
			union[fp] = alias
		}
	}
	res.Peers = len(union)
	res.SeenAliases = aliasesFrom(union, 10)
	peersMu.Unlock()
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

// shouldCountPeer reports whether a response should count as another LocalSend
// device: not us (by fingerprint), and not from this host — neither a local
// interface address nor loopback (e.g. something on this device poking the
// register listener).
func shouldCountPeer(fingerprint, remoteIP, selfFingerprint string, localSet map[string]bool) bool {
	if fingerprint == "" || fingerprint == selfFingerprint {
		return false
	}
	if remoteIP != "" {
		if ip := net.ParseIP(remoteIP); ip != nil && ip.IsLoopback() {
			return false
		}
		if localSet[remoteIP] {
			return false
		}
	}
	return true
}

// startRegisterListener serves a minimal LocalSend register endpoint on the discovery
// port and reports each registering device via recordPeer. Only POSTs to a register
// path count, so random HTTP traffic is not mistaken for a LocalSend peer. Returns an
// error if the port could not be bound (e.g. a receiver is still running); peers can
// then only be seen via UDP.
func startRegisterListener(devInfo models.DeviceInfo, recordPeer func(fingerprint, alias, remoteIP string)) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", constants.DefaultPort))
	if err != nil {
		return nil, err
	}
	selfInfo, _ := json.Marshal(devInfo)
	srv := &http.Server{
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/register") {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			var a models.Announcement
			body, _ := io.ReadAll(io.LimitReader(req.Body, 8192))
			if json.Unmarshal(body, &a) == nil {
				host, _, _ := net.SplitHostPort(req.RemoteAddr)
				recordPeer(a.Fingerprint, a.Alias, host)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(selfInfo)
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	return ln, nil
}

func localIPv4s() []string {
	ips, err := utils.GetMyIPv4Addr()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
}

func printHuman(res Result) {
	if res.BindError != "" {
		fmt.Printf("multicast bind FAILED: %s\n", res.BindError)
	} else {
		fmt.Printf("multicast loopback: %s\n", yn(res.Loopback))
	}
	fmt.Printf("peers seen: %d (udp: %d, http register: %d)\n", res.Peers, res.UDPPeers, res.RegisterPeers)
	if len(res.SeenAliases) > 0 {
		fmt.Printf("devices seen: %s\n", joinOr(res.SeenAliases, ""))
	}
	if res.RegisterBindError != "" {
		fmt.Printf("register listener FAILED: %s\n", res.RegisterBindError)
	}
	fmt.Printf("local ip: %s\n", joinOr(res.LocalIPs, "none"))
	fmt.Printf("duration: %dms\n", res.DurationMS)
}

func yn(b bool) string {
	if b {
		return "ok"
	}
	return "FAILED"
}

func joinOr(items []string, def string) string {
	if len(items) == 0 {
		return def
	}
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// aliasesFrom collects peer aliases (deduped and capped) from a fingerprint→alias map
// for display in the diagnostics wizard, so a user can confirm a specific device responded.
func aliasesFrom(peers map[string]string, limit int) []string {
	seen := make(map[string]bool, len(peers))
	out := make([]string, 0, len(peers))
	for _, alias := range peers {
		if alias == "" {
			alias = "(unknown)"
		}
		if !seen[alias] {
			seen[alias] = true
			out = append(out, alias)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func init() {
	Cmd.PersistentFlags().IntVarP(&duration, "duration", "d", 3, "test duration in seconds (loopback + peer listen window)")
	Cmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "output result as JSON (for the diagnostics wizard)")
}
