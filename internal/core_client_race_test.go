package internal

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

// TestStartDialCoreConcurrentWithRequests guards the data race between the
// dialCore goroutine (which assigns the core mesh client) and request
// handlers that read it via publish/discovery lookups.
func TestStartDialCoreConcurrentWithRequests(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Setenv("MUXCORE_GRPC_ADDR", lis.Addr().String())
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")

	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "race.db"),
		ImageDir: filepath.Join(t.TempDir(), "images"),
		GRPCAddr: "127.0.0.1:0",
		HTTPAddr: "127.0.0.1:0",
	})
	setRoots(m, "/tmp")
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				add, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
					TmdbId: int32(1000 + i*100 + j),
					Title:  fmt.Sprintf("Movie %d-%d", i, j),
				})
				if err != nil {
					t.Errorf("AddMovie: %v", err)
					return
				}
				if _, err := m.AddFile(ctx, &mgmntv1.AddFileRequest{MovieId: add.MovieId, FilePath: "/tmp/x.mkv"}); err != nil {
					t.Errorf("AddFile: %v", err)
					return
				}
				_, _ = m.findMetadataAddr(ctx)
				_, _ = m.findRootsAddr(ctx)
				m.publish(ctx, "test.event", nil)
				time.Sleep(time.Millisecond)
			}
		}(i)
	}
	wg.Wait()

	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
