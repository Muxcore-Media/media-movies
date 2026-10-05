# Changelog

## [0.1.14] - 2026-10-05


### Added
- `integsupport` package: `Config`, `Module`, `NewTestModule(t, Config)` and `Start(ctx, *Module)` for the umbrella integration tests (T-M1-06, NFR-MNT-004). Temp-dir DB, loopback listeners, plaintext gRPC for tests; `Module.GRPCListenAddr()` test hook.

## [0.1.13] - 2026-10-05

### Changed
- Requires media-automation v0.1.46.

## [0.1.11] - 2026-10-05

### Changed
- CI runs on GitHub-hosted runners from the umbrella template; retired-origin workflows removed.
- Dependencies resolve from published GitHub tags (no filesystem `replace`); requires core v0.6.0.

## [0.1.11] — 2026-09-08

### Added
- Household Radarr collection loop: persist `collection_prefs`, `GetCollectionPrefs` / `SetCollectionMonitored` / `SyncCollection`. Sync adds missing TMDB collection parts (fixture hook in tests) and searches when `search_on_add` is on.

## [0.1.10] — 2026-09-06

### Fixed
- Bump `contracts-media-admin` to Feature enum generation so tip admin-ui Unified Wanted recognizes movies `FEATURE_MISSING` (umbrella #122).

## [0.1.9] — 2026-08-10

### Added

- Advertise `settings` capability so admin-ui discovers SettingsProvider without ListAll probing.

## [0.1.8] — 2026-08-10

### Added
- SettingsProvider mesh (`RegisterSettings`) for `image_dir` (live artwork path).


## [0.1.7] — 2026-08-10

### Fixed
- Tests expect absolute destination_path when both storage_key and destination are present.


## [0.1.6] — 2026-08-10

### Fixed
- Resolve relative/storage-key movie file paths against root_folder_path for streaming.
- Prefer absolute destination_path over storage_key when attaching imported files.


## [0.1.5] — 2026-08-10

### Fixed
- PathUnescape movie stream ids so RFC3339-suffixed ids work through proxies.


## [0.1.4] — 2026-08-10

### Fixed
- Sync Info()/muxcore.json version to **0.1.4**.

## v0.1.1 (2026-08-09)

- Movie library manager with TMDB import, file tracking, HTTP poster/stream helpers
- `MediaAdminService` (SearchIndexers / grab path) for admin UI
- Mesh registration for MVP host (`:9420` / `:9430`)
