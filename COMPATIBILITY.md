# Compatibility

## Core Version

| Module Version | Core Version | Status |
|----------------|-------------|--------|
| v0.1.1           | 0.4.0+     | Current |

MVP host stacks pin **core@v0.5.0**. This module declares `minCoreVersion` **0.4.0**.

## Capabilities

- `media.library`
- `media.library.movies`

## Contracts

Implements `MediaAdminService` from `github.com/Muxcore-Media/contracts-media-admin` (v0.1.0).

## Breaking Changes

Pre-1.0 module: interfaces may change without a major version bump. Prefer published tags over `main`/`master` HEAD in production.
