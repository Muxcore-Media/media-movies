# Compatibility

## Core Version

| Module Version | Core Version | Status |
|----------------|-------------|--------|
| v0.1.11          | 0.4.0+     | Current |

MVP host stacks pin **core@v0.5.8**. This module declares `minCoreVersion` **0.4.0**.

## Capabilities

- `media.library`, `media.library.movies`
- `settings` — persisted path settings via SettingsProvider
- `backupable` — SQLite `ExportState` / `ImportState`; also back up `MOVIES_IMAGE_DIR` via `BACKUP_SOURCE_DIRS`

## Contracts

Implements `MediaAdminService` from `github.com/Muxcore-Media/contracts-media-admin` (v0.1.0).

Movie-specific gRPC (`MovieManagementService`): collections monitor, `ListTrailers`, alternate titles, missing list with `minimum_availability`.

## Breaking Changes

Pre-1.0 module: interfaces may change without a major version bump. Prefer published tags over `main`/`master` HEAD in production.
