// Package integsupport exposes media-movies internals to umbrella integration tests.
package integsupport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	mod "github.com/Muxcore-Media/media-movies/internal"
)

// Module is the media-movies module; its gRPC handler methods (AddMovie,
// GetMovie, ListMovies, ...) are available directly.
type Module = mod.Module

// Config is the media-movies module configuration. Empty fields are filled
// with temp-dir paths and loopback ephemeral addresses by NewTestModule.
type Config = mod.Config

// NewTestModule builds and initialises a Module backed by t.TempDir() storage
// and loopback listeners (127.0.0.1:0). Plaintext gRPC is enabled for the test
// unless MUXCORE_INSECURE_DISABLE_TLS is already set. Stop is registered via
// t.Cleanup.
func NewTestModule(t *testing.T, cfg Config) *Module {
	t.Helper()
	if os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "" {
		t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	}
	dir := t.TempDir()
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(dir, "movies.db")
	}
	if cfg.ImageDir == "" {
		cfg.ImageDir = filepath.Join(dir, "images")
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:0"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:0"
	}
	m := mod.NewModule(cfg)
	if err := m.Init(context.Background()); err != nil {
		t.Fatalf("media-movies init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m
}

// Start starts the gRPC/HTTP servers and, when MUXCORE_GRPC_ADDR is set,
// connects to the core mesh at that address.
func Start(ctx context.Context, m *Module) error {
	return m.Start(ctx)
}
