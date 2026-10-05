package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
	manifest "github.com/Muxcore-Media/media-movies"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	_ "modernc.org/sqlite"
)

type Module struct {
	mgmntv1.UnimplementedMovieManagementServiceServer

	mu    sync.RWMutex
	cfgMu sync.RWMutex
	db    *sql.DB
	// mc is the core mesh client, set asynchronously by dialCore. Always read
	// it via coreClient(); nil means not (yet) connected.
	mc atomic.Pointer[client.Client]
	// lifeCancel stops background goroutines (core dial, event subscriptions).
	lifeCancel context.CancelFunc

	id           string
	dbPath       string
	grpcAddr     string
	announceAddr string
	httpAddr     string
	imageDir     string
	grpcSrv      *grpc.Server
	httpSrv      *http.Server
	grpcLis      net.Listener
	httpLis      net.Listener

	rootsConn   *grpc.ClientConn
	rootsClient rootsv1.RootFolderServiceClient
	// rootsListFn overrides mesh discovery for tests. When set, roots are considered available.
	rootsListFn func(ctx context.Context, mediaKind string) ([]string, error)
	// automationSearchFn overrides mesh automation SearchItem for tests.
	automationSearchFn func(ctx context.Context, req *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error)
	// collectionPartsFn overrides metadata GetCollection for tests.
	collectionPartsFn func(ctx context.Context, collectionID int32) ([]collectionPart, error)
}

type Config struct {
	ID           string
	DBPath       string
	GRPCAddr     string
	AnnounceAddr string
	HTTPAddr     string
	ImageDir     string
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "media-movies"
	}
	// Apply env var overrides first.
	if cfg.DBPath == "" {
		if v := os.Getenv("MOVIES_DB_PATH"); v != "" {
			cfg.DBPath = v
		}
	}
	if cfg.GRPCAddr == "" {
		if v := os.Getenv("MOVIES_GRPC_ADDR"); v != "" {
			cfg.GRPCAddr = v
		}
	}
	cfg.GRPCAddr = resolveGRPCAddr(cfg.GRPCAddr)
	if cfg.AnnounceAddr == "" {
		if v := os.Getenv("MOVIES_ANNOUNCE_ADDR"); v != "" {
			cfg.AnnounceAddr = v
		}
	}
	if cfg.HTTPAddr == "" {
		if v := os.Getenv("MOVIES_HTTP_ADDR"); v != "" {
			cfg.HTTPAddr = v
		}
	}
	if cfg.ImageDir == "" {
		if v := os.Getenv("MOVIES_IMAGE_DIR"); v != "" {
			cfg.ImageDir = v
		}
	}
	// Apply defaults for anything still empty.
	if cfg.DBPath == "" {
		cfg.DBPath = "/var/lib/media-movies/movies.db"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:9420"
	}
	if cfg.AnnounceAddr == "" {
		cfg.AnnounceAddr = cfg.GRPCAddr
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":9430"
	}
	if cfg.ImageDir == "" {
		cfg.ImageDir = "/var/lib/media-movies/images"
	}
	return &Module{
		id:           cfg.ID,
		dbPath:       cfg.DBPath,
		grpcAddr:     cfg.GRPCAddr,
		announceAddr: cfg.AnnounceAddr,
		httpAddr:     cfg.HTTPAddr,
		imageDir:     cfg.ImageDir,
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           m.id,
		Name:         "Media Movies",
		Version:      modulesdk.ManifestVersion(manifest.ManifestJSON),
		Roles:        []string{"media_manager"},
		Description:  "Movie library manager with TMDB metadata import, file tracking, and admin UI integration",
		Author:       "MuxCore",
		Capabilities: []string{"media.library", "media.library.movies", "settings"},
		Contracts: []contracts.ContractDeclaration{
			{Repo: "github.com/Muxcore-Media/contracts-media-admin", Interface: "MediaAdminService", Version: "v0.1.1"},
		},
		MinCoreVersion: "0.4.0",
		HTTPAddr:       m.announceAddr,
	}
}

func (m *Module) Init(ctx context.Context) error {
	dir := filepath.Dir(m.dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create db directory: %w", err)
	}
	if err := os.MkdirAll(m.imageDir, 0700); err != nil {
		return fmt.Errorf("create image directory: %w", err)
	}

	db, err := sql.Open("sqlite", m.dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS movies (
			id           TEXT PRIMARY KEY,
			tmdb_id      INTEGER UNIQUE,
			title        TEXT NOT NULL,
			original_title TEXT DEFAULT '',
			year         INTEGER DEFAULT 0,
			overview     TEXT DEFAULT '',
			tagline      TEXT DEFAULT '',
			runtime      INTEGER DEFAULT 0,
			vote_average REAL DEFAULT 0,
			status       TEXT DEFAULT '',
			imdb_id      TEXT DEFAULT '',
			genres       TEXT DEFAULT '[]',
			poster_path  TEXT DEFAULT '',
			backdrop_path TEXT DEFAULT '',
			monitored    INTEGER DEFAULT 1,
			has_file     INTEGER DEFAULT 0,
			quality_profile_id TEXT DEFAULT '',
			root_folder_path   TEXT DEFAULT '',
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create movies table: %w", err)
	}
	for _, col := range []string{
		`ALTER TABLE movies ADD COLUMN quality_profile_id TEXT DEFAULT ''`,
		`ALTER TABLE movies ADD COLUMN root_folder_path TEXT DEFAULT ''`,
		`ALTER TABLE movies ADD COLUMN collection_id INTEGER DEFAULT 0`,
		`ALTER TABLE movies ADD COLUMN collection_name TEXT DEFAULT ''`,
		`ALTER TABLE movies ADD COLUMN release_date TEXT DEFAULT ''`,
	} {
		if _, err := db.ExecContext(ctx, col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return fmt.Errorf("migrate movies: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS movie_files (
			id         TEXT PRIMARY KEY,
			movie_id   TEXT NOT NULL,
			file_path  TEXT NOT NULL,
			quality    TEXT DEFAULT '',
			size_bytes INTEGER DEFAULT 0,
			container  TEXT DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create movie_files table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_movies_title ON movies(title)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create index: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tags (
			id TEXT PRIMARY KEY,
			label TEXT UNIQUE NOT NULL,
			created_at TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create tags table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS item_tags (
			item_id TEXT NOT NULL,
			tag_id TEXT NOT NULL,
			PRIMARY KEY (item_id, tag_id)
		)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create item_tags table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_movie_files_movie ON movie_files(movie_id)
	`); err != nil {
		db.Close()
		return fmt.Errorf("create files index: %w", err)
	}

	m.mu.Lock()
	m.db = db
	if err := m.ensureHistoryTable(ctx); err != nil {
		m.mu.Unlock()
		db.Close()
		return err
	}
	if err := m.ensureMovieTitlesTable(ctx); err != nil {
		m.mu.Unlock()
		db.Close()
		return err
	}
	if err := m.ensureCollectionPrefsTable(ctx); err != nil {
		m.mu.Unlock()
		db.Close()
		return err
	}
	m.backfillMovieTitles(ctx)
	m.mu.Unlock()

	grpcLis, err := net.Listen("tcp", m.grpcAddr)
	if err != nil {
		db.Close()
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.grpcLis = grpcLis

	httpLis, err := net.Listen("tcp", m.httpAddr)
	if err != nil {
		db.Close()
		return fmt.Errorf("listen HTTP %s: %w", m.httpAddr, err)
	}
	m.httpLis = httpLis

	slog.Info("media-movies initialized",
		"db", m.dbPath,
		"grpc", m.grpcAddr,
		"http", m.httpAddr,
	)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	srv, err := meshtls.NewServer()
	if err != nil {
		return fmt.Errorf("gRPC TLS: %w", err)
	}
	if meshtls.Insecure() {
		slog.Warn("media-movies gRPC listening without TLS (dev only)",
			"addr", m.grpcAddr,
			"hint", "unset MUXCORE_INSECURE_DISABLE_TLS for production",
		)
	} else {
		slog.Info("media-movies gRPC TLS enabled", "addr", m.grpcAddr)
	}
	m.grpcSrv = srv
	mediaadminv1.RegisterMediaAdminServiceServer(m.grpcSrv, mediaAdminServer{m: m})
	mgmntv1.RegisterMovieManagementServiceServer(m.grpcSrv, m)
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)

	mux := http.NewServeMux()
	mux.HandleFunc("/images/", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/images/", http.FileServer(http.Dir(m.getImageDir()))).ServeHTTP(w, r)
	})
	mux.HandleFunc("/stream/movies/", m.handleStreamMovie)
	mux.HandleFunc("GET /api/calendar", m.handleHTTPCalendar)
	m.httpSrv = &http.Server{Handler: mux}

	go func() {
		slog.Info("media-movies gRPC service started", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(m.grpcLis); err != nil {
			slog.Error("media-movies gRPC serve error", "error", err)
		}
	}()
	go func() {
		slog.Info("media-movies HTTP service started", "addr", m.httpAddr)
		if err := m.httpSrv.Serve(m.httpLis); err != nil && err != http.ErrServerClosed {
			slog.Error("media-movies HTTP serve error", "error", err)
		}
	}()

	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	m.lifeCancel = lifeCancel
	go m.dialCore(lifeCtx)
	go m.subscribeToFileImported(lifeCtx)
	go m.subscribeToDownloadDispatched(lifeCtx)
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m.lifeCancel != nil {
		m.lifeCancel()
	}
	if m.httpSrv != nil {
		if err := m.httpSrv.Shutdown(ctx); err != nil {
			slog.Warn("http server shutdown", "error", err)
		}
	}
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.rootsConn != nil {
		_ = m.rootsConn.Close()
	}
	if c := m.mc.Swap(nil); c != nil {
		c.Close()
	}
	m.mu.Lock()
	if m.db != nil {
		m.db.Close()
		m.db = nil
	}
	m.mu.Unlock()
	slog.Info("media-movies stopped")
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("not initialized")
	}
	return db.PingContext(ctx)
}

func (m *Module) dialCore(ctx context.Context) {
	meshAddr := os.Getenv("MUXCORE_GRPC_ADDR")
	if meshAddr == "" {
		meshAddr = "localhost:9090"
	}
	insecureMode := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true"

	var opts []client.Option
	if insecureMode {
		opts = append(opts, client.WithInsecure())
	}

	backoff := coreRetryMin
	for {
		c, err := client.Dial(meshAddr, opts...)
		if err == nil {
			if ctx.Err() != nil {
				c.Close()
				return
			}
			m.mc.Store(c)
			break
		}
		slog.Error("media-movies: dial core; will retry", "error", err, "retry_in", backoff)
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
	slog.Info("media-movies: connected to core mesh", "addr", meshAddr)
}

// coreClient returns the core mesh client, or nil if not yet connected.
func (m *Module) coreClient() *client.Client { return m.mc.Load() }

// Retry backoff bounds for dialing core and subscribing to events.
const (
	coreRetryMin = 100 * time.Millisecond
	coreRetryMax = 5 * time.Second
)

// sleepCtx waits for d or until ctx is done; it reports whether ctx is still live.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(d time.Duration) time.Duration {
	if d *= 2; d > coreRetryMax {
		return coreRetryMax
	}
	return d
}

// eventSubscriber opens an event subscription (satisfied by the SDK events client).
type eventSubscriber interface {
	Subscribe(ctx context.Context, eventType string) (<-chan *eventsv1.Event, context.CancelFunc, error)
}

// subscribeWhenReady subscribes to eventType as soon as the core mesh client is
// connected, retrying with exponential backoff while the client is missing or
// Subscribe fails (for example core not ready). Event delivery is at-most-once,
// so subscribing late loses events; there is deliberately no fixed delay. It
// returns the event channel and cancel func, or ok=false if ctx ends first.
func subscribeWhenReady(ctx context.Context, get func() eventSubscriber, eventType string) (<-chan *eventsv1.Event, context.CancelFunc, bool) {
	backoff := coreRetryMin
	for {
		if sub := get(); sub != nil {
			ch, cancel, err := sub.Subscribe(ctx, eventType)
			if err == nil {
				return ch, cancel, true
			}
			slog.Warn("subscribe to events; will retry", "event", eventType, "error", err, "retry_in", backoff)
		}
		if !sleepCtx(ctx, backoff) {
			return nil, nil, false
		}
		backoff = nextBackoff(backoff)
	}
}

func (m *Module) eventSubscriber() eventSubscriber {
	if c := m.coreClient(); c != nil {
		return c.Events
	}
	return nil
}

func (m *Module) subscribeToFileImported(ctx context.Context) {
	ch, cancel, ok := subscribeWhenReady(ctx, m.eventSubscriber, contracts.EventFileImported)
	if !ok {
		return
	}
	defer cancel()
	slog.Info("subscribed to file imported events")
	for evt := range ch {
		var p contracts.FileImportedPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil || p.MediaType != "movie" {
			continue
		}
		if err := m.handleFileImported(context.Background(), p); err != nil {
			slog.Debug("handle imported movie file", "title", p.Title, "error", err)
		}
	}
}

func (m *Module) handleFileImported(ctx context.Context, p contracts.FileImportedPayload) error {
	movieID, err := m.resolveMovieForImport(ctx, p)
	if err != nil {
		return err
	}
	if movieID == "" {
		return fmt.Errorf("no matching movie for %q", p.Title)
	}

	qualityStr := p.Quality
	if qualityStr == "" {
		qualityStr = "Unknown"
	}
	filePath := p.DestinationPath
	if !filepath.IsAbs(filePath) {
		// Prefer absolute library paths for local streaming; storage keys are relative.
		if filepath.IsAbs(p.StorageKey) {
			filePath = p.StorageKey
		} else if filePath == "" {
			filePath = p.StorageKey
		}
	}
	ext := filepath.Ext(filePath)
	container := "mkv"
	if ext != "" {
		container = ext[1:]
	}
	_, err = m.AddFile(ctx, &mgmntv1.AddFileRequest{
		MovieId:   movieID,
		FilePath:  filePath,
		Quality:   qualityStr,
		SizeBytes: 0,
		Container: container,
	})
	return err
}

func (m *Module) resolveMovieForImport(ctx context.Context, p contracts.FileImportedPayload) (string, error) {
	if id := m.findMovieID(p.TMDBID, p.Title, p.Year); id != "" {
		return id, nil
	}
	tmdbID := p.TMDBID
	title := p.Title
	year := p.Year
	overview := ""
	poster := ""
	backdrop := ""
	release := ""
	var genres []string

	if tmdbID == 0 {
		result, err := m.searchMovieMetadata(ctx, p.Title, p.Year)
		if err != nil {
			return "", err
		}
		if result == nil {
			return "", nil
		}
		tmdbID = result.GetId()
		title = result.GetTitle()
		if title == "" {
			title = result.GetOriginalTitle()
		}
		release = result.GetReleaseDate()
		year = extractYear(release)
		overview = result.GetOverview()
		poster = result.GetPosterPath()
		backdrop = result.GetBackdropPath()
	}

	if id := m.findMovieID(tmdbID, title, year); id != "" {
		return id, nil
	}

	resp, err := m.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:       tmdbID,
		Title:        title,
		Year:         year,
		Overview:     overview,
		PosterPath:   poster,
		BackdropPath: backdrop,
		Genres:       genres,
	})
	if err != nil {
		return "", err
	}
	m.persistReleaseDate(ctx, resp.GetMovieId(), release)
	go m.syncMovieTitlesFromTMDB(context.Background(), resp.GetMovieId(), tmdbID, title, "")
	return resp.GetMovieId(), nil
}

func (m *Module) findMovieID(tmdbID int32, title string, year int32) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return ""
	}
	var id string
	if tmdbID != 0 {
		_ = m.db.QueryRow(`SELECT id FROM movies WHERE tmdb_id = ? LIMIT 1`, tmdbID).Scan(&id)
		if id != "" {
			return id
		}
	}
	clean := cleanMatchTitle(title)
	if clean == "" {
		return ""
	}
	rows, err := m.db.Query(`
		SELECT m.id, m.year FROM movies m
		INNER JOIN movie_titles t ON t.movie_id = m.id
		WHERE t.clean_title = ?
	`, clean)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var rowID string
		var rowYear int32
		if err := rows.Scan(&rowID, &rowYear); err != nil {
			continue
		}
		if year == 0 || rowYear == 0 || rowYear == year {
			return rowID
		}
	}
	return ""
}

func (m *Module) searchMovieMetadata(ctx context.Context, title string, year int32) (*metadatav1.SearchResult, error) {
	metaAddr, err := m.findMetadataAddr(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := meshtls.Dial(metaAddr)
	if err != nil {
		return nil, fmt.Errorf("dial metadata: %w", err)
	}
	defer conn.Close()

	metaClient := metadatav1.NewMetadataServiceClient(conn)
	resp, err := metaClient.Search(ctx, &metadatav1.SearchRequest{
		Query: title,
		Type:  metadatav1.MediaType_MEDIA_TYPE_MOVIE,
		Year:  year,
	})
	if err != nil {
		return nil, fmt.Errorf("metadata search: %w", err)
	}
	return pickBestSearchResult(resp.GetResults(), year, true), nil
}

func pickBestSearchResult(results []*metadatav1.SearchResult, year int32, movie bool) *metadatav1.SearchResult {
	if len(results) == 0 {
		return nil
	}
	var best *metadatav1.SearchResult
	for _, r := range results {
		if movie && r.GetMediaType() != metadatav1.MediaType_MEDIA_TYPE_UNSPECIFIED &&
			r.GetMediaType() != metadatav1.MediaType_MEDIA_TYPE_MOVIE {
			continue
		}
		if !movie && r.GetMediaType() != metadatav1.MediaType_MEDIA_TYPE_UNSPECIFIED &&
			r.GetMediaType() != metadatav1.MediaType_MEDIA_TYPE_TV {
			continue
		}
		date := r.GetReleaseDate()
		if !movie {
			date = r.GetFirstAirDate()
		}
		ry := extractYear(date)
		if year != 0 && ry != 0 && ry != year {
			continue
		}
		if best == nil || r.GetPopularity() > best.GetPopularity() {
			best = r
		}
	}
	if best != nil {
		return best
	}
	return results[0]
}

func (m *Module) findMetadataAddr(ctx context.Context) (string, error) {
	mc := m.coreClient()
	if mc == nil {
		return "", fmt.Errorf("not connected to core")
	}
	modules, err := mc.Discovery.FindByCapability(ctx, "metadata")
	if err != nil {
		return "", fmt.Errorf("discover metadata: %w", err)
	}
	for _, mod := range modules {
		addr := dialAddrForModule(mod.Id, mod.HttpAddr)
		if addr != "" {
			return addr, nil
		}
	}
	return "", fmt.Errorf("no metadata module found")
}

// resolveGRPCAddr prefers loopback when plaintext is explicitly enabled and the
// bind address would otherwise listen on all interfaces.
func resolveGRPCAddr(addr string) string {
	if !meshtls.Insecure() {
		return addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "127.0.0.1" + addr
		}
		return addr
	}
	if host == "" || host == "0.0.0.0" {
		return "127.0.0.1:" + port
	}
	return addr
}

// dialAddrForModule maps discovery HttpAddr to a dial target.
// Bare/wildcard hosts use module ID (compose DNS) unless MUXCORE_MESH_DIAL_LOCAL=true.
func dialAddrForModule(moduleID, httpAddr string) string {
	if httpAddr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(httpAddr)
	if err != nil || port == "" {
		return httpAddr
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return net.JoinHostPort(host, port)
	}
	if os.Getenv("MUXCORE_MESH_DIAL_LOCAL") == "true" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	if moduleID != "" {
		return net.JoinHostPort(moduleID, port)
	}
	return httpAddr
}

func (m *Module) publish(ctx context.Context, eventType string, payload map[string]interface{}) {
	mc := m.coreClient()
	if mc == nil {
		return
	}
	data, _ := json.Marshal(payload)
	if err := mc.Events.Publish(ctx, eventType, m.id, data); err != nil {
		slog.Warn("publish event failed", "type", eventType, "error", err)
	}
}

// ── MovieManagementService ────────────────────────────────────

func (m *Module) AddMovie(ctx context.Context, req *mgmntv1.AddMovieRequest) (*mgmntv1.AddMovieResponse, error) {
	rootPath, err := m.resolveRootFolderPath(ctx, req.GetRootFolderPath(), "movies")
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if m.db == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("not initialized")
	}

	var existingID string
	if req.GetTmdbId() != 0 {
		_ = m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE tmdb_id = ? LIMIT 1`, req.GetTmdbId()).Scan(&existingID)
		if existingID != "" {
			m.mu.Unlock()
			return &mgmntv1.AddMovieResponse{MovieId: existingID}, nil
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("mv_%d_%s", req.GetTmdbId(), now)

	genresJSON, _ := json.Marshal(req.GetGenres())

	_, err = m.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO movies (id, tmdb_id, title, year, overview, poster_path, backdrop_path, genres, monitored, quality_profile_id, root_folder_path, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		id, req.GetTmdbId(), req.GetTitle(), req.GetYear(),
		req.GetOverview(), req.GetPosterPath(), req.GetBackdropPath(),
		string(genresJSON), req.GetQualityProfileId(), rootPath, now, now,
	)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("insert movie: %w", err)
	}

	_ = m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE tmdb_id = ? LIMIT 1`, req.GetTmdbId()).Scan(&existingID)
	if existingID != "" && existingID != id {
		m.mu.Unlock()
		return &mgmntv1.AddMovieResponse{MovieId: existingID}, nil
	}
	m.syncMoviePrimaryTitlesLocked(ctx, id, req.GetTitle(), "")
	m.mu.Unlock()

	m.persistCachedArtwork(ctx, id, req.GetPosterPath(), req.GetBackdropPath())
	go m.syncMovieTitlesFromTMDB(context.Background(), id, req.GetTmdbId(), req.GetTitle(), "")

	go m.publish(context.Background(), contracts.EventMovieAdded, map[string]interface{}{
		"movie_id": id, "tmdb_id": req.GetTmdbId(), "title": req.GetTitle(),
	})

	return &mgmntv1.AddMovieResponse{MovieId: id}, nil
}

func (m *Module) UpdateMovie(ctx context.Context, req *mgmntv1.UpdateMovieRequest) (*mgmntv1.UpdateMovieResponse, error) {
	var rootPath *string
	if req.RootFolderPath != nil {
		resolved, err := m.resolveRootFolderPath(ctx, req.GetRootFolderPath(), "movies")
		if err != nil {
			return nil, err
		}
		rootPath = &resolved
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}
	if req.GetMovieId() == "" {
		return nil, fmt.Errorf("movie_id required")
	}

	var sets []string
	var args []any
	if req.QualityProfileId != nil {
		sets = append(sets, `quality_profile_id = ?`)
		args = append(args, req.GetQualityProfileId())
	}
	if rootPath != nil {
		sets = append(sets, `root_folder_path = ?`)
		args = append(args, *rootPath)
	}
	if req.Monitored != nil {
		monitored := 0
		if req.GetMonitored() {
			monitored = 1
		}
		sets = append(sets, `monitored = ?`)
		args = append(args, monitored)
	}
	if len(sets) == 0 {
		movie := m.getMovieLocked(ctx, req.GetMovieId())
		if movie == nil {
			return nil, fmt.Errorf("movie not found: %s", req.GetMovieId())
		}
		return &mgmntv1.UpdateMovieResponse{Movie: movie}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sets = append(sets, `updated_at = ?`)
	args = append(args, now, req.GetMovieId())
	res, err := m.db.ExecContext(ctx,
		`UPDATE movies SET `+strings.Join(sets, `, `)+` WHERE id = ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("update movie: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("movie not found: %s", req.GetMovieId())
	}

	movie := m.getMovieLocked(ctx, req.GetMovieId())
	if movie == nil {
		return nil, fmt.Errorf("movie not found: %s", req.GetMovieId())
	}
	return &mgmntv1.UpdateMovieResponse{Movie: movie}, nil
}

func (m *Module) getMovieLocked(ctx context.Context, movieID string) *mgmntv1.MovieItem {
	row := m.db.QueryRowContext(ctx,
		`SELECT id, tmdb_id, title, original_title, year, overview, tagline,
		 runtime, vote_average, status, imdb_id, genres, poster_path, backdrop_path,
		 monitored, has_file, quality_profile_id, root_folder_path, created_at, updated_at FROM movies WHERE id = ?`,
		movieID,
	)
	return m.scanSingle(row)
}

func (m *Module) RemoveMovie(ctx context.Context, req *mgmntv1.RemoveMovieRequest) (*mgmntv1.RemoveMovieResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	var tmdbID int
	var title, rootFolder string
	if err := m.db.QueryRowContext(ctx, `SELECT tmdb_id, title, COALESCE(root_folder_path, '') FROM movies WHERE id = ?`, req.GetMovieId()).Scan(&tmdbID, &title, &rootFolder); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("remove movie: lookup failed", "movie_id", req.GetMovieId(), "error", err)
	}

	if req.GetDeleteFiles() {
		rows, err := m.db.QueryContext(ctx, `SELECT DISTINCT file_path FROM movie_files WHERE movie_id = ?`, req.GetMovieId())
		if err == nil {
			for rows.Next() {
				var p string
				if rows.Scan(&p) == nil {
					_ = safeDeleteMediaFile(p, rootFolder)
				}
			}
			rows.Close()
		}
	}

	m.appendHistory(ctx, historyEntry{
		EventType: historyDeleteItem,
		ItemID:    req.GetMovieId(),
		Title:     title,
		Data:      map[string]any{"tmdb_id": tmdbID, "delete_files": req.GetDeleteFiles()},
	})

	for _, q := range []string{
		`DELETE FROM item_tags WHERE item_id = ?`,
		`DELETE FROM movie_titles WHERE movie_id = ?`,
		`DELETE FROM movie_files WHERE movie_id = ?`,
	} {
		if _, err := m.db.ExecContext(ctx, q, req.GetMovieId()); err != nil {
			slog.Warn("remove movie: dependent row cleanup failed", "movie_id", req.GetMovieId(), "query", q, "error", err)
		}
	}
	_, err := m.db.ExecContext(ctx, `DELETE FROM movies WHERE id = ?`, req.GetMovieId())
	if err != nil {
		return nil, fmt.Errorf("delete movie: %w", err)
	}

	m.removeItemArtwork(req.GetMovieId())

	go m.publish(context.Background(), contracts.EventMovieRemoved, map[string]interface{}{
		"movie_id": req.GetMovieId(), "tmdb_id": tmdbID, "title": title,
	})
	return &mgmntv1.RemoveMovieResponse{}, nil
}

func (m *Module) RefreshMetadata(ctx context.Context, req *mgmntv1.RefreshMetadataRequest) (*mgmntv1.RefreshMetadataResponse, error) {
	m.mu.RLock()
	var tmdbID int32
	var movieTitle string
	lookupErr := m.db.QueryRowContext(ctx, `SELECT tmdb_id, title FROM movies WHERE id = ?`, req.GetMovieId()).Scan(&tmdbID, &movieTitle)
	m.mu.RUnlock()
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return nil, fmt.Errorf("look up movie %s: %w", req.GetMovieId(), lookupErr)
	}

	if tmdbID == 0 {
		return nil, fmt.Errorf("movie not found: %s", req.GetMovieId())
	}

	metaAddr, err := m.findMetadataAddr(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := meshtls.Dial(metaAddr)
	if err != nil {
		return nil, fmt.Errorf("dial metadata: %w", err)
	}
	defer conn.Close()

	metaClient := metadatav1.NewMetadataServiceClient(conn)
	details, err := metaClient.GetMovieDetails(ctx, &metadatav1.GetMovieDetailsRequest{
		TmdbId: tmdbID,
	})
	if err != nil {
		return nil, fmt.Errorf("metadata fetch: %w", err)
	}

	genresJSON, _ := json.Marshal(details.GetGenres())
	now := time.Now().UTC().Format(time.RFC3339)

	posterSrc := details.GetPosterUrl()
	if posterSrc == "" {
		posterSrc = details.GetPosterPath()
	}
	backdropSrc := details.GetBackdropUrl()
	if backdropSrc == "" {
		backdropSrc = details.GetBackdropPath()
	}

	m.mu.Lock()
	var collID int32
	var collName string
	if c := details.GetBelongsToCollection(); c != nil {
		collID = c.GetId()
		collName = c.GetName()
	}
	_, err = m.db.ExecContext(ctx,
		`UPDATE movies SET title=?, original_title=?, year=?, overview=?, tagline=?, runtime=?, vote_average=?, status=?, imdb_id=?, genres=?, poster_path=?, backdrop_path=?, collection_id=?, collection_name=?, release_date=?, updated_at=? WHERE id=?`,
		details.GetTitle(), details.GetOriginalTitle(), extractYear(details.GetReleaseDate()),
		details.GetOverview(), details.GetTagline(), details.GetRuntime(), details.GetVoteAverage(),
		details.GetStatus(), details.GetImdbId(), string(genresJSON),
		details.GetPosterPath(), details.GetBackdropPath(), collID, collName, normalizeReleaseDate(details.GetReleaseDate()), now, req.GetMovieId(),
	)
	if err == nil {
		m.syncMoviePrimaryTitlesLocked(ctx, req.GetMovieId(), details.GetTitle(), details.GetOriginalTitle())
	}
	m.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("update movie: %w", err)
	}

	m.persistCachedArtwork(ctx, req.GetMovieId(), posterSrc, backdropSrc)
	go m.syncMovieTitlesFromTMDB(context.Background(), req.GetMovieId(), tmdbID, details.GetTitle(), details.GetOriginalTitle())

	go m.publish(context.Background(), contracts.EventMovieUpdated, map[string]interface{}{
		"movie_id": req.GetMovieId(), "tmdb_id": tmdbID, "title": details.GetTitle(),
	})

	slog.Info("metadata refreshed", "movie_id", req.GetMovieId(), "title", details.GetTitle())
	return &mgmntv1.RefreshMetadataResponse{}, nil
}

func extractYear(dateStr string) int32 {
	if len(dateStr) >= 4 {
		if y, err := strconv.Atoi(dateStr[:4]); err == nil {
			return int32(y)
		}
	}
	return 0
}

func (m *Module) ListMovies(ctx context.Context, req *mgmntv1.ListMoviesRequest) (*mgmntv1.ListMoviesResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `SELECT id, tmdb_id, title, original_title, year, overview, tagline,
		runtime, vote_average, status, imdb_id, genres, poster_path, backdrop_path,
		monitored, has_file, quality_profile_id, root_folder_path, created_at, updated_at FROM movies`
	countQuery := `SELECT COUNT(*) FROM movies`

	var args []any
	var where []string

	if req.GetSearch() != "" {
		where = append(where, `(title LIKE ? OR id IN (SELECT movie_id FROM movie_titles WHERE title LIKE ? OR clean_title LIKE ?))`)
		q := "%" + req.GetSearch() + "%"
		args = append(args, q, q, "%"+cleanMatchTitle(req.GetSearch())+"%")
	}
	if req.GetGenre() != "" {
		where = append(where, `genres LIKE ?`)
		args = append(args, `%"`+req.GetGenre()+`"%`)
	}
	if req.GetTagId() != "" {
		where = append(where, `id IN (SELECT item_id FROM item_tags WHERE tag_id = ?)`)
		args = append(args, req.GetTagId())
	}
	if len(where) > 0 {
		clause := ` WHERE ` + strings.Join(where, ` AND `)
		query += clause
		countQuery += clause
	}

	var total int
	if err := m.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count movies: %w", err)
	}

	sortBy := req.GetSortBy()
	if sortBy == "" {
		sortBy = "title"
	}
	validSortBy := map[string]bool{
		"title": true, "year": true, "rating": true,
		"added_at": true, "updated_at": true,
		"sort_title": true, "runtime": true,
	}
	if !validSortBy[sortBy] {
		sortBy = "title"
	}
	sortOrder := req.GetSortOrder()
	if sortOrder != "desc" {
		sortOrder = "asc"
	}
	query += fmt.Sprintf(` ORDER BY %s %s LIMIT ? OFFSET ?`, sortBy, sortOrder)
	queryArgs := append(args, pageSize, offset)

	rows, err := m.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query movies: %w", err)
	}
	defer rows.Close()

	var movies []*mgmntv1.MovieItem
	for rows.Next() {
		movie := m.scanMovie(rows)
		if movie != nil {
			movies = append(movies, movie)
		}
	}

	return &mgmntv1.ListMoviesResponse{
		Movies: movies, Total: int32(total),
		Page: int32(page), PageSize: int32(pageSize),
	}, nil
}

func (m *Module) ListMissing(ctx context.Context, req *mgmntv1.ListMissingRequest) (*mgmntv1.ListMissingResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	const where = `WHERE monitored = 1 AND has_file = 0`
	var total int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies `+where).Scan(&total); err != nil {
		return nil, fmt.Errorf("count missing movies: %w", err)
	}

	rows, err := m.db.QueryContext(ctx,
		`SELECT id, tmdb_id, title, year, quality_profile_id, root_folder_path FROM movies `+where+`
		 ORDER BY title ASC LIMIT ? OFFSET ?`, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("query missing movies: %w", err)
	}
	defer rows.Close()

	var items []*mgmntv1.MissingMovieItem
	for rows.Next() {
		var id, title, profileID, rootFolder string
		var tmdbID, year int64
		if err := rows.Scan(&id, &tmdbID, &title, &year, &profileID, &rootFolder); err != nil {
			slog.Error("scan missing movie", "error", err)
			continue
		}
		items = append(items, &mgmntv1.MissingMovieItem{
			MovieId: id, TmdbId: int32(tmdbID), Title: title, Year: int32(year),
			QualityProfileId: profileID, RootFolderPath: rootFolder,
		})
	}

	return &mgmntv1.ListMissingResponse{
		Items: items, Total: int32(total),
		Page: int32(page), PageSize: int32(pageSize),
	}, nil
}

func (m *Module) GetMovie(ctx context.Context, req *mgmntv1.GetMovieRequest) (*mgmntv1.GetMovieResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	row := m.db.QueryRowContext(ctx,
		`SELECT id, tmdb_id, title, original_title, year, overview, tagline,
		 runtime, vote_average, status, imdb_id, genres, poster_path, backdrop_path,
		 monitored, has_file, quality_profile_id, root_folder_path, created_at, updated_at FROM movies WHERE id = ?`,
		req.GetMovieId(),
	)

	movie := m.scanSingle(row)
	if movie == nil {
		return nil, fmt.Errorf("movie not found: %s", req.GetMovieId())
	}
	return &mgmntv1.GetMovieResponse{Movie: movie}, nil
}

func (m *Module) scanMovie(rows *sql.Rows) *mgmntv1.MovieItem {
	var id, title, originalTitle, overview, tagline, status, imdbID, genresStr, posterPath, backdropPath, qualityProfileID, rootFolderPath, createdAt, updatedAt string
	var tmdbID, year, runtime int64
	var voteAvg float64
	var monitored, hasFile int

	err := rows.Scan(&id, &tmdbID, &title, &originalTitle, &year, &overview, &tagline,
		&runtime, &voteAvg, &status, &imdbID, &genresStr, &posterPath, &backdropPath,
		&monitored, &hasFile, &qualityProfileID, &rootFolderPath, &createdAt, &updatedAt)
	if err != nil {
		slog.Error("scan movie row", "error", err)
		return nil
	}

	var genres []string
	if err := json.Unmarshal([]byte(genresStr), &genres); err != nil {
		genres = nil
	}
	if genres == nil {
		genres = []string{}
	}

	return &mgmntv1.MovieItem{
		Id: id, TmdbId: int32(tmdbID),
		Title: title, OriginalTitle: originalTitle,
		Year: int32(year), Overview: overview, Tagline: tagline,
		Runtime: int32(runtime), VoteAverage: voteAvg,
		Status: status, ImdbId: imdbID,
		Genres: genres, PosterPath: posterPath, BackdropPath: backdropPath,
		Monitored: monitored != 0, HasFile: hasFile != 0,
		QualityProfileId: qualityProfileID, RootFolderPath: rootFolderPath,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func (m *Module) scanMovieWithCollection(rows *sql.Rows) *mgmntv1.MovieItem {
	var id, title, originalTitle, overview, tagline, status, imdbID, genresStr, posterPath, backdropPath, qualityProfileID, rootFolderPath, createdAt, updatedAt, collectionName string
	var tmdbID, year, runtime, collectionID int64
	var voteAvg float64
	var monitored, hasFile int

	err := rows.Scan(&id, &tmdbID, &title, &originalTitle, &year, &overview, &tagline,
		&runtime, &voteAvg, &status, &imdbID, &genresStr, &posterPath, &backdropPath,
		&monitored, &hasFile, &qualityProfileID, &rootFolderPath, &createdAt, &updatedAt,
		&collectionID, &collectionName)
	if err != nil {
		slog.Error("scan movie collection row", "error", err)
		return nil
	}

	var genres []string
	if err := json.Unmarshal([]byte(genresStr), &genres); err != nil {
		genres = nil
	}
	if genres == nil {
		genres = []string{}
	}

	return &mgmntv1.MovieItem{
		Id: id, TmdbId: int32(tmdbID),
		Title: title, OriginalTitle: originalTitle,
		Year: int32(year), Overview: overview, Tagline: tagline,
		Runtime: int32(runtime), VoteAverage: voteAvg,
		Status: status, ImdbId: imdbID,
		Genres: genres, PosterPath: posterPath, BackdropPath: backdropPath,
		Monitored: monitored != 0, HasFile: hasFile != 0,
		QualityProfileId: qualityProfileID, RootFolderPath: rootFolderPath,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
		CollectionId: int32(collectionID), CollectionName: collectionName,
	}
}

func (m *Module) scanSingle(row *sql.Row) *mgmntv1.MovieItem {
	var id, title, originalTitle, overview, tagline, status, imdbID, genresStr, posterPath, backdropPath, qualityProfileID, rootFolderPath, createdAt, updatedAt string
	var tmdbID, year, runtime int64
	var voteAvg float64
	var monitored, hasFile int

	err := row.Scan(&id, &tmdbID, &title, &originalTitle, &year, &overview, &tagline,
		&runtime, &voteAvg, &status, &imdbID, &genresStr, &posterPath, &backdropPath,
		&monitored, &hasFile, &qualityProfileID, &rootFolderPath, &createdAt, &updatedAt)
	if err != nil {
		return nil
	}

	var genres []string
	if err := json.Unmarshal([]byte(genresStr), &genres); err != nil {
		genres = nil
	}
	if genres == nil {
		genres = []string{}
	}

	return &mgmntv1.MovieItem{
		Id: id, TmdbId: int32(tmdbID),
		Title: title, OriginalTitle: originalTitle,
		Year: int32(year), Overview: overview, Tagline: tagline,
		Runtime: int32(runtime), VoteAverage: voteAvg,
		Status: status, ImdbId: imdbID,
		Genres: genres, PosterPath: posterPath, BackdropPath: backdropPath,
		Monitored: monitored != 0, HasFile: hasFile != 0,
		QualityProfileId: qualityProfileID, RootFolderPath: rootFolderPath,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

// ── File Management ────────────────────────────────────────────

func (m *Module) AddFile(ctx context.Context, req *mgmntv1.AddFileRequest) (*mgmntv1.AddFileResponse, error) {
	filePath, err := m.confineMediaFile(ctx, req.GetFilePath(), "movies")
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := fmt.Sprintf("mf_%d", time.Now().UnixNano())

	_, err = m.db.ExecContext(ctx,
		`INSERT INTO movie_files (id, movie_id, file_path, quality, size_bytes, container, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, req.GetMovieId(), filePath, req.GetQuality(), req.GetSizeBytes(), req.GetContainer(), now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert file: %w", err)
	}

	if _, err := m.db.ExecContext(ctx, `UPDATE movies SET has_file = 1, updated_at = ? WHERE id = ?`, now, req.GetMovieId()); err != nil {
		slog.Warn("add file: failed to set has_file", "movie_id", req.GetMovieId(), "error", err)
	}

	var title string
	_ = m.db.QueryRowContext(ctx, `SELECT title FROM movies WHERE id = ?`, req.GetMovieId()).Scan(&title)
	m.appendHistory(ctx, historyEntry{
		EventType: historyImport,
		ItemID:    req.GetMovieId(),
		Title:     title,
		Quality:   req.GetQuality(),
		FilePath:  filePath,
		Data:      map[string]any{"file_id": id, "container": req.GetContainer(), "size_bytes": req.GetSizeBytes()},
	})

	go m.publish(context.Background(), contracts.EventMovieFileAdded, map[string]interface{}{
		"file_id": id, "movie_id": req.GetMovieId(), "file_path": filePath, "quality": req.GetQuality(),
	})

	return &mgmntv1.AddFileResponse{FileId: id}, nil
}

func (m *Module) RemoveFile(ctx context.Context, req *mgmntv1.RemoveFileRequest) (*mgmntv1.RemoveFileResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	var movieID, filePath, quality string
	if err := m.db.QueryRowContext(ctx, `SELECT movie_id, file_path, COALESCE(quality, '') FROM movie_files WHERE id = ?`, req.GetFileId()).Scan(&movieID, &filePath, &quality); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("remove file: lookup failed", "file_id", req.GetFileId(), "error", err)
	}

	var title string
	if movieID != "" {
		_ = m.db.QueryRowContext(ctx, `SELECT title FROM movies WHERE id = ?`, movieID).Scan(&title)
	}

	if req.GetDeleteFiles() && filePath != "" {
		var rootFolder string
		_ = m.db.QueryRowContext(ctx, `SELECT COALESCE(root_folder_path, '') FROM movies WHERE id = ?`, movieID).Scan(&rootFolder)
		_ = safeDeleteMediaFile(filePath, rootFolder)
	}

	if movieID != "" {
		m.appendHistory(ctx, historyEntry{
			EventType: historyDeleteFile,
			ItemID:    movieID,
			Title:     title,
			Quality:   quality,
			FilePath:  filePath,
			Data:      map[string]any{"file_id": req.GetFileId(), "delete_files": req.GetDeleteFiles()},
		})
	}

	_, err := m.db.ExecContext(ctx, `DELETE FROM movie_files WHERE id = ?`, req.GetFileId())
	if err != nil {
		return nil, fmt.Errorf("delete file: %w", err)
	}

	var remaining int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movie_files WHERE movie_id = ?`, movieID).Scan(&remaining); err != nil {
		slog.Warn("remove file: count remaining files failed", "movie_id", movieID, "error", err)
		remaining = -1 // unknown: leave has_file untouched
	}
	if remaining == 0 {
		if _, err := m.db.ExecContext(ctx, `UPDATE movies SET has_file = 0, updated_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), movieID); err != nil {
			slog.Warn("remove file: failed to clear has_file", "movie_id", movieID, "error", err)
		}
	}

	go m.publish(context.Background(), contracts.EventMovieFileRemoved, map[string]interface{}{
		"file_id": req.GetFileId(), "movie_id": movieID, "file_path": filePath,
	})

	return &mgmntv1.RemoveFileResponse{}, nil
}

func (m *Module) ListFiles(ctx context.Context, req *mgmntv1.ListFilesRequest) (*mgmntv1.ListFilesResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	rows, err := m.db.QueryContext(ctx,
		`SELECT id, movie_id, file_path, quality, size_bytes, container, created_at FROM movie_files WHERE movie_id = ? ORDER BY created_at`, req.GetMovieId())
	if err != nil {
		return nil, fmt.Errorf("query files: %w", err)
	}
	defer rows.Close()

	var files []*mgmntv1.MovieFile
	for rows.Next() {
		var id, movieID, filePath, quality, container, createdAt string
		var size int64
		if err := rows.Scan(&id, &movieID, &filePath, &quality, &size, &container, &createdAt); err != nil {
			continue
		}
		files = append(files, &mgmntv1.MovieFile{
			Id: id, MovieId: movieID, FilePath: filePath, Quality: quality,
			SizeBytes: size, Container: container, CreatedAt: createdAt,
		})
	}

	return &mgmntv1.ListFilesResponse{Files: files}, nil
}

// ── MediaAdminService (admin-ui contract) ─────────────────────

func (m *Module) GetMediaTypeInfo(ctx context.Context, req *mediaadminv1.GetMediaTypeInfoRequest) (*mediaadminv1.GetMediaTypeInfoResponse, error) {
	return &mediaadminv1.GetMediaTypeInfoResponse{
		DisplayName: "Movies",
		Icon:        "🎬",
		FilterFields: []*mediaadminv1.FilterField{
			{Key: "genre", Label: "Genre", Type: mediaadminv1.FilterFieldType_FILTER_FIELD_TYPE_TEXT},
			{Key: "year", Label: "Year", Type: mediaadminv1.FilterFieldType_FILTER_FIELD_TYPE_NUMBER},
			{Key: "runtime", Label: "Runtime", Type: mediaadminv1.FilterFieldType_FILTER_FIELD_TYPE_NUMBER},
			{Key: "has_file", Label: "Has File", Type: mediaadminv1.FilterFieldType_FILTER_FIELD_TYPE_SELECT, Options: []string{"true", "false"}},
		},
		Features: []mediaadminv1.Feature{
			mediaadminv1.Feature_FEATURE_MISSING,
			mediaadminv1.Feature_FEATURE_TAGS,
			mediaadminv1.Feature_FEATURE_COLLECTIONS,
		},
	}, nil
}

// sortColumnForField maps the admin SortField enum to a movies table column.
// Unspecified (and unknown) values fall back to title.
func sortColumnForField(f mediaadminv1.SortField) string {
	switch f {
	case mediaadminv1.SortField_SORT_FIELD_YEAR:
		return "year"
	case mediaadminv1.SortField_SORT_FIELD_CREATED_AT:
		return "created_at"
	case mediaadminv1.SortField_SORT_FIELD_UPDATED_AT:
		return "updated_at"
	case mediaadminv1.SortField_SORT_FIELD_RUNTIME:
		return "runtime"
	case mediaadminv1.SortField_SORT_FIELD_RATING:
		return "vote_average"
	default:
		return "title"
	}
}

func (m *Module) ListItems(ctx context.Context, req *mediaadminv1.ListItemsRequest) (*mediaadminv1.ListItemsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := `SELECT id, tmdb_id, title, original_title, year, overview, tagline,
		runtime, vote_average, status, imdb_id, genres, poster_path, backdrop_path,
		monitored, has_file, quality_profile_id, root_folder_path, created_at, updated_at FROM movies`
	countQuery := `SELECT COUNT(*) FROM movies`

	var args []any
	var where []string

	if req.GetSearch() != "" {
		where = append(where, `(title LIKE ? OR id IN (SELECT movie_id FROM movie_titles WHERE title LIKE ? OR clean_title LIKE ?))`)
		q := "%" + req.GetSearch() + "%"
		args = append(args, q, q, "%"+cleanMatchTitle(req.GetSearch())+"%")
	}
	if req.GetTagId() != "" {
		where = append(where, `id IN (SELECT item_id FROM item_tags WHERE tag_id = ?)`)
		args = append(args, req.GetTagId())
	}

	sortBy := sortColumnForField(req.GetSortBy())
	sortOrder := req.GetSortOrder()
	if sortOrder != "desc" {
		sortOrder = "asc"
	}

	if len(where) > 0 {
		clause := ` WHERE ` + strings.Join(where, ` AND `)
		query += clause
		countQuery += clause
	}

	var total int
	if err := m.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	query += fmt.Sprintf(` ORDER BY %s %s LIMIT ? OFFSET ?`, sortBy, sortOrder)
	qargs := append(args, pageSize, offset)

	rows, err := m.db.QueryContext(ctx, query, qargs...)
	if err != nil {
		return nil, fmt.Errorf("query items: %w", err)
	}
	defer rows.Close()

	var items []*mediaadminv1.MediaItem
	for rows.Next() {
		movie := m.scanMovie(rows)
		if movie != nil {
			items = append(items, m.movieToMediaItem(movie))
		}
	}

	return &mediaadminv1.ListItemsResponse{
		Items: items, Total: int32(total),
		Page: int32(page), PageSize: int32(pageSize),
	}, nil
}

func (m *Module) GetItem(ctx context.Context, req *mediaadminv1.GetItemRequest) (*mediaadminv1.GetItemResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	row := m.db.QueryRowContext(ctx,
		`SELECT id, tmdb_id, title, original_title, year, overview, tagline,
		 runtime, vote_average, status, imdb_id, genres, poster_path, backdrop_path,
		 monitored, has_file, quality_profile_id, root_folder_path, created_at, updated_at FROM movies WHERE id = ?`,
		req.GetId(),
	)

	movie := m.scanSingle(row)
	if movie == nil {
		return nil, fmt.Errorf("movie not found: %s", req.GetId())
	}
	return &mediaadminv1.GetItemResponse{Item: m.movieToMediaItem(movie)}, nil
}

func (m *Module) UpdateMetadata(ctx context.Context, req *mediaadminv1.UpdateMetadataRequest) (*mediaadminv1.UpdateMetadataResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil, fmt.Errorf("not initialized")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	genresJSON, _ := json.Marshal(req.GetGenres())

	_, err := m.db.ExecContext(ctx,
		`UPDATE movies SET title=?, overview=?, year=?, genres=?, updated_at=? WHERE id=?`,
		req.GetTitle(), req.GetDescription(), req.GetYear(),
		string(genresJSON), now, req.GetId(),
	)
	if err != nil {
		return nil, fmt.Errorf("update movie: %w", err)
	}

	meta := req.GetMetadata()
	if meta != nil {
		if v, ok := meta["quality_profile_id"]; ok {
			if _, err := m.db.ExecContext(ctx, `UPDATE movies SET quality_profile_id=?, updated_at=? WHERE id=?`, v, now, req.GetId()); err != nil {
				return nil, fmt.Errorf("update quality_profile_id: %w", err)
			}
		}
		if v, ok := meta["root_folder_path"]; ok {
			if _, err := m.db.ExecContext(ctx, `UPDATE movies SET root_folder_path=?, updated_at=? WHERE id=?`, v, now, req.GetId()); err != nil {
				return nil, fmt.Errorf("update root_folder_path: %w", err)
			}
		}
	}

	row := m.db.QueryRowContext(ctx,
		`SELECT id, tmdb_id, title, original_title, year, overview, tagline,
		 runtime, vote_average, status, imdb_id, genres, poster_path, backdrop_path,
		 monitored, has_file, quality_profile_id, root_folder_path, created_at, updated_at FROM movies WHERE id = ?`,
		req.GetId(),
	)
	movie := m.scanSingle(row)
	if movie == nil {
		return nil, fmt.Errorf("movie not found after update: %s", req.GetId())
	}
	return &mediaadminv1.UpdateMetadataResponse{Item: m.movieToMediaItem(movie)}, nil
}

func (m *Module) ListArtwork(ctx context.Context, req *mediaadminv1.ListArtworkRequest) (*mediaadminv1.ListArtworkResponse, error) {
	m.mu.RLock()
	if m.db == nil {
		m.mu.RUnlock()
		return nil, fmt.Errorf("not initialized")
	}

	row := m.db.QueryRowContext(ctx,
		`SELECT poster_path, backdrop_path FROM movies WHERE id = ?`, req.GetId(),
	)
	var poster, backdrop string
	if err := row.Scan(&poster, &backdrop); err != nil {
		m.mu.RUnlock()
		return nil, fmt.Errorf("movie not found: %s", req.GetId())
	}
	m.mu.RUnlock()

	poster = m.resolveServablePath(ctx, req.GetId(), "poster", poster)
	backdrop = m.resolveServablePath(ctx, req.GetId(), "backdrop", backdrop)
	return &mediaadminv1.ListArtworkResponse{
		Artwork: m.buildArtworkInfos(req.GetId(), poster, backdrop),
	}, nil
}

func (m *Module) DeleteItem(ctx context.Context, req *mediaadminv1.DeleteItemRequest) (*mediaadminv1.DeleteItemResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	m.mu.RLock()
	var exists string
	err := m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE id = ?`, req.GetId()).Scan(&exists)
	m.mu.RUnlock()
	if err != nil || exists == "" {
		return nil, status.Errorf(codes.NotFound, "movie not found: %s", req.GetId())
	}

	if _, err := m.RemoveMovie(ctx, &mgmntv1.RemoveMovieRequest{
		MovieId:     req.GetId(),
		DeleteFiles: req.GetDeleteFiles(),
	}); err != nil {
		return nil, err
	}
	return &mediaadminv1.DeleteItemResponse{}, nil
}

func (m *Module) RefreshItem(ctx context.Context, req *mediaadminv1.RefreshItemRequest) (*mediaadminv1.RefreshItemResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	if _, err := m.RefreshMetadata(ctx, &mgmntv1.RefreshMetadataRequest{MovieId: req.GetId()}); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return nil, err
	}
	item, err := m.GetItem(ctx, &mediaadminv1.GetItemRequest{Id: req.GetId()})
	if err != nil {
		return nil, err
	}
	return &mediaadminv1.RefreshItemResponse{Item: item.Item}, nil
}

func (m *Module) ReplaceArtwork(stream mediaadminv1.MediaAdminService_ReplaceArtworkServer) error {
	ctx := stream.Context()
	var itemID, filename string
	var artworkType mediaadminv1.ArtworkType
	var buf []byte

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch {
		case msg.GetItemId() != "":
			itemID = msg.GetItemId()
		case msg.GetArtworkType() != mediaadminv1.ArtworkType_ARTWORK_TYPE_UNSPECIFIED:
			artworkType = msg.GetArtworkType()
		case msg.GetFilename() != "":
			filename = msg.GetFilename()
		default:
			chunk := msg.GetChunk()
			if len(chunk) == 0 {
				continue
			}
			if len(buf)+len(chunk) > maxArtworkBytes {
				return status.Errorf(codes.InvalidArgument, "artwork exceeds %d bytes", maxArtworkBytes)
			}
			buf = append(buf, chunk...)
		}
	}

	if itemID == "" {
		return status.Error(codes.InvalidArgument, "item_id required")
	}
	kind, err := artworkKindFromType(artworkType)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	m.mu.RLock()
	var exists string
	scanErr := m.db.QueryRowContext(ctx, `SELECT id FROM movies WHERE id = ?`, itemID).Scan(&exists)
	m.mu.RUnlock()
	if scanErr != nil || exists == "" {
		return status.Errorf(codes.NotFound, "movie not found: %s", itemID)
	}

	relPath, mime, err := m.writeArtworkBytes(itemID, kind, filename, "", buf)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "%v", err)
	}

	col := "poster_path"
	if kind == "backdrop" {
		col = "backdrop_path"
	}
	m.mu.Lock()
	_, err = m.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE movies SET %s=?, updated_at=? WHERE id=?`, col),
		relPath, time.Now().UTC().Format(time.RFC3339), itemID,
	)
	m.mu.Unlock()
	if err != nil {
		return fmt.Errorf("update artwork path: %w", err)
	}

	return stream.SendAndClose(&mediaadminv1.ReplaceArtworkResponse{
		Artwork: &mediaadminv1.ArtworkInfo{
			Id: itemID + "_" + kind, ItemId: itemID,
			Type: artworkTypeForKind(kind), Url: artworkURL(m.httpAddr, relPath),
			MimeType: mime,
		},
	})
}

func (m *Module) movieToMediaItem(movie *mgmntv1.MovieItem) *mediaadminv1.MediaItem {
	meta := map[string]string{
		"tmdb_id":            strconv.Itoa(int(movie.GetTmdbId())),
		"runtime":            strconv.Itoa(int(movie.GetRuntime())),
		"vote_avg":           fmt.Sprintf("%.1f", movie.GetVoteAverage()),
		"imdb_id":            movie.GetImdbId(),
		"status":             movie.GetStatus(),
		"has_file":           strconv.FormatBool(movie.GetHasFile()),
		"monitored":          strconv.FormatBool(movie.GetMonitored()),
		"quality_profile_id": movie.GetQualityProfileId(),
		"root_folder_path":   movie.GetRootFolderPath(),
	}
	if movie.GetTagline() != "" {
		meta["tagline"] = movie.GetTagline()
	}

	return &mediaadminv1.MediaItem{
		Id: movie.GetId(), Title: movie.GetTitle(),
		Description: movie.GetOverview(), Year: int64(movie.GetYear()),
		Genres: movie.GetGenres(), Metadata: meta,
		Artwork:   m.buildArtworkInfos(movie.GetId(), movie.GetPosterPath(), movie.GetBackdropPath()),
		CreatedAt: movie.GetCreatedAt(), UpdatedAt: movie.GetUpdatedAt(),
	}
}

// handleStreamMovie serves the first attached movie file for browser playback.
// Path: GET /stream/movies/{movie_id}
func (m *Module) handleStreamMovie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/stream/movies/"), "/")
	if decoded, err := url.PathUnescape(id); err == nil {
		id = decoded
	}
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		http.Error(w, "not initialized", http.StatusServiceUnavailable)
		return
	}

	var filePath string
	err := db.QueryRowContext(r.Context(),
		`SELECT file_path FROM movie_files WHERE movie_id = ? ORDER BY created_at LIMIT 1`, id,
	).Scan(&filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if filePath != "" && !filepath.IsAbs(filePath) {
		var root string
		_ = db.QueryRowContext(r.Context(),
			`SELECT COALESCE(root_folder_path, '') FROM movies WHERE id = ?`, id,
		).Scan(&root)
		if root != "" {
			rel := strings.TrimPrefix(filepath.ToSlash(filePath), "media/")
			candidate := filepath.Join(root, filepath.FromSlash(rel))
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				filePath = candidate
			}
		}
	}
	if filePath == "" || !filepath.IsAbs(filePath) {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(filePath)
	if err != nil {
		slog.Warn("stream open failed", "movie_id", id, "path", filePath, "error", err)
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, filepath.Base(filePath), st.ModTime(), f)
}

var _ contracts.Module = (*Module)(nil)

// GRPCListenAddr returns the bound gRPC listener address once Init has run,
// or the configured address before that. Test hook for integration harnesses.
func (m *Module) GRPCListenAddr() string {
	if m.grpcLis != nil {
		return m.grpcLis.Addr().String()
	}
	return m.grpcAddr
}
