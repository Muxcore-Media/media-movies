# Changelog

## [0.1.23] - 2026-10-08

Version skips v0.1.22 (and, for media-tvshows, v0.1.21): tags with those numbers exist on unmerged branches that add an unauthenticated `content_rating` and are not part of this history.

### Added
- Parental classification authority (ADR-0031 Decision 2, T-M4-01 slice S2): `MovieItem` gains `content_rating`, `content_rating_source` (`operator`) and `tag_labels`; new `SetContentRating` RPC stores an operator rating, an explicit `NR` (`explicit_unrated`), or clears it; unknown tokens are rejected with `InvalidArgument`. `ListMovies` accepts an optional narrowing `classification_filter` (max rating, allow_unrated, blocked/allowed tags) whose `total` and pagination count only visible items. Items with no recorded rating are *unavailable* and never visible to an enabled filter. Existing rows migrate to unavailable via the new `movie_content_rating` table (created idempotently at startup); nothing is inferred from existing data and there is no `tmdb` source yet. Proto field 25 on `MovieItem` is reserved (used by the unmerged v0.1.22 tag); new fields are 26-28 and `classification_filter` is 8. Authorization stays with the BFF, as for `SetItemTags`.

## [0.1.21] - 2026-10-05


### Security
- Artwork (poster/backdrop) downloads use the netguard UserURL client: private, loopback, link-local and cloud-metadata targets are blocked at dial time and on every redirect (NFR-SEC-009 / RULE-VAL-2; sdk/go/module v0.6.6).
- Root-folder validation fails closed: if the media.roots registry is unreachable, `root_folder_path` (AddMovie, UpdateMovie, SetCollectionMonitored) is refused instead of accepted; collection-prefs root is now validated against registered roots (NFR-SEC-008 / RULE-VAL-1).
- AddFile confines `file_path` to registered movie roots (pathguard: traversal, symlink and sibling-prefix escapes rejected).
- File deletion on RemoveMovie/RemoveFile resolves symlinks (pathguard) and unlinks links rather than following them.

## [0.1.20] - 2026-10-05


### Security
- gRPC server and peer dials use mesh TLS (meshtls, sdk/go/module v0.6.5) unless the dev insecure flag is set (ADR-0016/0017).

## [0.1.19] - 2026-10-05

### Changed
- Built on core v0.6.14 / sdk/go/module v0.6.4: unregisters on shutdown and re-registers after core restarts (ADR-0022).

## [0.1.18] - 2026-10-05


### Changed
- Reported version comes from muxcore.json (ADR-0021); built on core v0.6.12 / sdk/go/module v0.6.3 (mesh enrollment, ADR-0017).

## [0.1.17] - 2026-10-05


### Added
- Upgrade test (`internal/upgrade_test.go`) with committed snapshots `internal/testdata/upgrade/v0.1.9.db` and `v0.1.15.db` (ADR-0015, T-M2-06, NFR-DATA-002, FR-INS-005): the current code opens databases created by older tags twice, with schema-superset, seeded-row, new-column-default and integrity checks.
### Fixed
- Startup deadlock when upgrading a database that has movies without `movie_titles` rows (e.g. created by v0.1.9): `backfillMovieTitles` kept its query cursor open while upserting, but the DB is limited to one connection, so `Init` hung forever. Rows are now drained and the cursor closed before upserting. Found by the upgrade test.

## [0.1.16] - 2026-10-05


### Fixed
- Event subscriptions (`file.imported`, `download.dispatched`) no longer wait a hard-coded 15 s after start; they subscribe as soon as the core mesh client is connected, with exponential-backoff retry (client not yet connected or Subscribe failing) until module Stop. `dialCore` also retries with backoff and uses the module lifecycle context. Previously, at-most-once delivery lost imports during the window, making fixture acquisition smoke flaky (T-M2-03, NFR-REL). Handler behaviour is unchanged.
- Test requirement: core v0.6.7 (for `core/integsupport`); added an in-process harness test that a `file.imported` published ~100 ms after Start is handled, plus a fake-subscriber retry/stop test.

## [0.1.15] - 2026-10-05


### Fixed
- Data race on the core mesh client: `Module.mc` is now an `atomic.Pointer[client.Client]` read via `coreClient()` (nil until `dialCore` connects); `mcMu` removed. All readers (`publish`, discovery lookups, event subscriptions) use the accessor. Added a `-race` regression test that runs `Start` concurrently with `AddMovie`/`AddFile` (T-M1-06, NFR-MNT-004).

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
