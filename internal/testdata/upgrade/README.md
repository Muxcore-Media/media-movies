# Upgrade snapshots (ADR-0015, NFR-DATA-002, FR-INS-005)

`<tag>.db` is a SQLite database produced by that tag's code and seeded with
representative rows; `<tag>.schema.sql` is `sqlite3 <db> .schema`. They are
read by `internal/upgrade_test.go`, which opens each (twice) with the current
code and checks schema superset, seeded rows, new-column defaults, integrity.

| Snapshot | Tag code differences |
|----------|----------------------|
| `v0.1.9` (previous release-train version) | no `movies.release_date`, no `collection_prefs` |
| `v0.1.15` (previous tag before latest `v0.1.16`) | same DDL as current |

## How produced

1. `git worktree add /tmp/media-movies-<tag> <tag>`
2. Copy `seed_upgrade_test.go.txt` to `internal/seed_upgrade_test.go` in the
   worktree (build tag `upgradeseed`; kept as `.txt` here so it does not
   compile in the current tree).
3. `UPGRADE_SEED_DB=/tmp/<tag>.db GOWORK=off go test -tags upgradeseed -run TestUpgradeSeed ./internal/`
   The old tags' `go.sum` no longer matches republished dependency tags, so a
   throwaway `-modfile` copy (empty sum) with `GOSUMDB=off` was used; the old
   `go.mod`/`go.sum` were not edited.
4. `sqlite3 <db> VACUUM`; `sqlite3 <db> .schema > <tag>.schema.sql`.

## Seed summary

The old `Module.Init` creates the schema; the seed then inserts via SQL:
3 movies (`mv_550_seed`, `mv_603_seed` in collection 2344, `mv_680_seed` with
only NOT NULL columns), 2 `movie_files` (user-home-style paths), 2 `tags`,
3 `item_tags`, 2 `history` rows (download id, source title, indexer, file
path), 3 `movie_titles` (none for `mv_680_seed`, so startup backfill is
exercised), 1 `collection_prefs` row (only if the table exists), and
`release_date` on `mv_550_seed` (only if the column exists).
