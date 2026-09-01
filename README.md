# Media Movies

Movie library manager with TMDB metadata import, file tracking, and admin UI integration.

## Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `MOVIES_DB_PATH` | `/var/lib/media-movies/movies.db` | SQLite database path |
| `MOVIES_GRPC_ADDR` | `:9420` | gRPC listen address |
| `MOVIES_ANNOUNCE_ADDR` | same as `MOVIES_GRPC_ADDR` | Address announced to the mesh |
| `MOVIES_HTTP_ADDR` | `127.0.0.1:9430` | HTTP listen address (loopback by default) |
| `MOVIES_HTTP_TOKEN` | _(empty)_ | Bearer/query token for `/images/` and `/stream/movies/` when not loopback |
| `MOVIES_IMAGE_DIR` | `/var/lib/media-movies/images` | Poster/image cache directory |
| `MUXCORE_MODULE_ID` | `media-movies` | Module identity |
| `MUXCORE_INSECURE_DISABLE_TLS` | `false` | Disable TLS for module↔core gRPC (`true` for local dev) |

## Capabilities

- `media.library`, `media.library.movies` — movie library management
- `settings` — admin SettingsProvider (`db_path`, `image_dir`)
- `backupable` — `ExportState` / `ImportState` on the SQLite library DB

Include `MOVIES_IMAGE_DIR` in `BACKUP_SOURCE_DIRS` (or per-request backup paths) so artwork survives restore alongside the DB export.

## Contract

Implements `MediaAdminService` from `github.com/Muxcore-Media/contracts-media-admin` (v0.1.0).

## MovieManagementService highlights

| RPC | Purpose |
|-----|---------|
| `ListTrailers` | YouTube/Vimeo trailer URLs from metadata `videos` |
| `SetCollectionMonitored` / `GetCollectionPrefs` | Collection monitor + search-on-add prefs |
| `SyncCollection` | Add missing TMDB collection parts; optional automation search on add |

HTTP (auth required off loopback):

- `GET /images/{relative}` — cached artwork under `MOVIES_IMAGE_DIR`
- `GET /stream/movies/{movie_id}` — first attached file under the movie `root_folder_path`

## Quick Start

```bash
go build -o media-movies ./cmd/module

export MUXCORE_INSECURE_DISABLE_TLS=true
./media-movies --muxcore-mesh-addr localhost:9090
```

## License

GPL-3.0
