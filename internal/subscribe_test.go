package internal

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/integsupport"
	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

// TestFileImportedHandledShortlyAfterStart guards the removed 15 s subscribe
// delay: event delivery is at-most-once, so an import published right after
// Start must still be handled.
func TestFileImportedHandledShortlyAfterStart(t *testing.T) {
	h := integsupport.NewCoreHarness(t)
	t.Setenv("MUXCORE_GRPC_ADDR", h.Addr)
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	t.Setenv("MUXCORE_MODULE_ID", "media-movies")

	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "sub.db"),
		ImageDir: filepath.Join(t.TempDir(), "images"),
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
	})
	setRoots(m, "/library/movies")
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{TmdbId: 603, Title: "The Matrix", Year: 1999})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })

	time.Sleep(100 * time.Millisecond)
	payload, _ := json.Marshal(contracts.FileImportedPayload{
		MediaType: "movie", Title: "The Matrix", Year: 1999, TMDBID: 603,
		DestinationPath: "/library/movies/The Matrix (1999)/The Matrix.mkv", Quality: "1080p",
	})
	// Retry the publish only to ride out the (inherently racy) server-side
	// registration of the stream; the handler must still run well under 15 s.
	deadline := time.Now().Add(5 * time.Second)
	for {
		pubCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := h.Bus.Publish(pubCtx, contracts.Event{Type: contracts.EventFileImported, Source: "test", Payload: payload})
		cancel()
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		if n := countFiles(t, m, add.GetMovieId()); n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("file.imported was not handled within 5s of Start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func countFiles(t *testing.T, m *Module, movieID string) int {
	t.Helper()
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM movie_files WHERE movie_id = ?`, movieID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type fakeSub struct {
	calls atomic.Int32
	failN int32
}

func (f *fakeSub) Subscribe(_ context.Context, _ string) (<-chan *eventsv1.Event, context.CancelFunc, error) {
	if f.calls.Add(1) <= f.failN {
		return nil, nil, errors.New("core not ready")
	}
	return make(chan *eventsv1.Event), func() {}, nil
}

func TestSubscribeWhenReadyRetriesAndStops(t *testing.T) {
	f := &fakeSub{failN: 2}
	var polls atomic.Int32
	get := func() eventSubscriber {
		if polls.Add(1) < 3 { // client not connected yet
			return nil
		}
		return f
	}
	if _, _, ok := subscribeWhenReady(context.Background(), get, "x"); !ok {
		t.Fatal("expected success after retries")
	}
	if f.calls.Load() != 3 {
		t.Fatalf("subscribe calls = %d, want 3", f.calls.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, ok := subscribeWhenReady(ctx, func() eventSubscriber { return nil }, "x"); ok {
		t.Fatal("expected stop on cancelled ctx")
	}
}
