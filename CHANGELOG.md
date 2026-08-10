# Changelog


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
