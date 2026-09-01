package internal

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/client"
)

func (m *Module) tryDialCore(ctx context.Context) {
	if m.mc != nil {
		return
	}
	meshAddr := getenvDefault("MUXCORE_GRPC_ADDR", "localhost:9090")

	var opts []client.Option
	if meshInsecureEnabled() {
		opts = append(opts, client.WithInsecure())
	}

	c, err := client.Dial(meshAddr, opts...)
	if err != nil {
		slog.Debug("media-movies: dial core retry", "error", err)
		return
	}
	m.mc = c
	slog.Info("media-movies: connected to core mesh", "addr", meshAddr)
}

func (m *Module) waitForCoreClient(ctx context.Context) bool {
	const maxWait = 5 * time.Minute
	deadline := time.Now().Add(maxWait)
	for {
		if m.mc != nil {
			return true
		}
		m.tryDialCore(ctx)
		if m.mc != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return m.mc != nil
		case <-time.After(2 * time.Second):
		}
	}
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
