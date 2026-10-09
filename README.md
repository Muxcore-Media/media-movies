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

## Parental classification (ADR-0031 Decision 2)

media-movies is the authority for each movie's `content_rating` and `content_rating_source` on `MovieItem`. There are two sources, highest precedence first:

1. `operator` — written only by `SetContentRating` (a ladder token, or an explicit `NR`). Table `movie_content_rating`.
2. `tmdb` — derived from the metadata module's `certification` (metadata-tmdb v0.1.10+) on every successful `RefreshMetadata`. Table `movie_tmdb_rating`. It is never written by `SetContentRating` and never overwrites an operator value.

The effective value is the operator value if one is recorded, else the tmdb value, else *unavailable* (empty rating and source). An explicit operator `NR` beats a tmdb rating; clearing the operator value returns the item to its tmdb value (or unavailable). The `classification_filter` on `ListMovies` evaluates the effective value; unavailable is never visible to an enabled filter.

A TMDB certification is accepted only if, after trimming and upper-casing, it is a ladder token (`G TV-Y TV-Y7 TV-Y7-FV ALL E PG TV-G TV-PG E10+ PG-13 TV-14 T R TV-MA M MA NC-17 AO X`) or an unrated marker (`NR`, `UR`, `NOT RATED`, `UNRATED`, stored as `NR`). Country-specific tokens (`15`, `12A`), free text and the empty string yield no tmdb value. Nothing is inferred from other data (vote average, TMDB's `adult` flag, genres).

Refresh semantics: if the metadata fetch fails, the previously stored tmdb value is kept; if it succeeds and the certification is empty or unmappable, the stored tmdb value is cleared (so a removed or changed certification is reflected). A newly added movie has no tmdb value until its first `RefreshMetadata`. The configured certification country lives in metadata-tmdb; no new environment variable or setting is added here.

## Capability

`media.library`, `media.library.movies` — Movie library management

## Contract

`MediaAdminService` (`github.com/Muxcore-Media/contracts-media-admin`, v0.1.1+)

## Quick Start

```bash
go build -o media-movies ./cmd/module

export MUXCORE_INSECURE_DISABLE_TLS=true
./media-movies --muxcore-mesh-addr localhost:9090
```

## License

GPL-3.0
