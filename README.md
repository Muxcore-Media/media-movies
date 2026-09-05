# Media Movies

Movie library manager with TMDB metadata import, file tracking, and admin UI integration.

## Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `MOVIES_DB_PATH` | `/var/lib/media-movies/movies.db` | SQLite database path |
| `MOVIES_GRPC_ADDR` | `127.0.0.1:9420` | gRPC listen address |
| `MOVIES_ANNOUNCE_ADDR` | same as `MOVIES_GRPC_ADDR` | Address announced to the mesh |
| `MOVIES_HTTP_ADDR` | `:9430` | HTTP listen address |
| `MOVIES_IMAGE_DIR` | `/var/lib/media-movies/images` | Poster/image cache directory |
| `MUXCORE_MODULE_ID` | `media-movies` | Module identity |
| `MUXCORE_INSECURE_DISABLE_TLS` | `false` | Disable TLS for module gRPC (`true` for local dev; also accepts `MUXCORE_GRPC_INSECURE`) |
| `MOVIES_TLS_CERT` / `MOVIES_TLS_KEY` / `MOVIES_TLS_CA` | (auto-generated) | Optional TLS material for the gRPC listener |

## Capability

`media.library`, `media.library.movies` — Movie library management

## Contract

`MediaAdminService` (`github.com/Muxcore-Media/contracts-media-admin`, v0.1.0)

## Quick Start

```bash
go build -o media-movies ./cmd/module

export MUXCORE_INSECURE_DISABLE_TLS=true
./media-movies --muxcore-mesh-addr localhost:9090
```

## License

GPL-3.0
