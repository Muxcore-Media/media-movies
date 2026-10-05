package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/module/netguard"
)

// allowLoopbackArtwork lets a test reach its httptest server by using an
// Integration/loopback netguard client for the duration of the test.
func allowLoopbackArtwork(t *testing.T, _ *httptest.Server) {
	t.Helper()
	old := artworkHTTPClient
	artworkHTTPClient = netguard.NewClient(netguard.Integration, netguard.Options{AllowLoopback: true, Timeout: 5 * time.Second})
	t.Cleanup(func() { artworkHTTPClient = old })
}

func TestCacheRemoteArtworkBlocksPrivateTargets(t *testing.T) {
	m := newTestModule(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(srv.Close)
	urls := []string{
		srv.URL + "/p.jpg", // 127.0.0.1
		"http://localhost/p.jpg",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/p.jpg",
		"http://192.168.1.1/p.jpg",
		"http://[::1]/p.jpg",
		"http://metadata.google.internal/",
		"file:///etc/passwd",
	}
	for _, u := range urls {
		_, _, err := m.cacheRemoteArtwork(context.Background(), "m1", "poster", u)
		if err == nil || !strings.Contains(err.Error(), "netguard") {
			t.Errorf("%s: expected netguard block, got %v", u, err)
		}
	}
	if hits != 0 {
		t.Fatalf("private server was contacted %d times", hits)
	}
}
