package send

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localsend-cli/internal/models"
)

func TestForwardSender_CancelInterruptsActiveUpload(t *testing.T) {
	started := make(chan struct{})
	releaseUpload := make(chan struct{})
	var startedOnce bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/localsend/v2/prepare-upload":
			var req models.PreUploadReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				return
			}
			tokens := make(map[string]string, len(req.Files))
			for id := range req.Files {
				tokens[id] = "token"
			}
			_ = json.NewEncoder(w).Encode(models.PreUploadResp{SessionId: "session", Tokens: tokens})
		case "/api/localsend/v2/upload":
			if !startedOnce {
				startedOnce = true
				close(started)
			}
			<-releaseUpload
		case "/api/localsend/v2/cancel":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	hostPort := strings.TrimPrefix(server.URL, "http://")
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "large.bin")
	if err := os.WriteFile(path, make([]byte, 4<<20), 0644); err != nil {
		t.Fatal(err)
	}
	sender := NewForwardSender()
	if err := sender.Init(&models.DeviceInfo{IP: host}, false); err != nil {
		t.Fatal(err)
	}
	sender.SetRemotePort(port)
	if err := sender.AddFile(path); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- sender.Start() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upload never became active")
	}
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- sender.Cancel() }()
	select {
	case <-done:
		close(releaseUpload)
	case <-time.After(time.Second):
		close(releaseUpload)
		t.Fatal("Cancel did not interrupt active upload")
	}
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatalf("protocol cancel failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("protocol cancel did not complete")
	}
}
