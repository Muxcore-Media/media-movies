package internal

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (m *Module) httpAccessToken() string {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	if m.httpToken != "" {
		return m.httpToken
	}
	return os.Getenv("MOVIES_HTTP_TOKEN")
}

func (m *Module) authorizeHTTPRequest(r *http.Request) bool {
	token := m.httpAccessToken()
	if token != "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			auth = strings.TrimSpace(auth[7:])
		}
		if auth == "" {
			auth = r.URL.Query().Get("token")
		}
		return auth == token
	}
	return isLoopbackRemoteAddr(r.RemoteAddr)
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (m *Module) handleImages(w http.ResponseWriter, r *http.Request) {
	if !m.authorizeHTTPRequest(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/images/")
	if rel == "" || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	abs := filepath.Join(m.getImageDir(), filepath.FromSlash(rel))
	if _, err := pathUnderRoot(abs, m.getImageDir()); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, abs)
}
