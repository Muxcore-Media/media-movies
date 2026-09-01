package internal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (m *Module) applyDatabasePragmas(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return fmt.Errorf("enable foreign_keys: %w", err)
	}
	return nil
}

func (m *Module) migrateMovieColumns(ctx context.Context, db *sql.DB) error {
	for _, col := range []string{
		`ALTER TABLE movies ADD COLUMN quality_profile_id TEXT DEFAULT ''`,
		`ALTER TABLE movies ADD COLUMN root_folder_path TEXT DEFAULT ''`,
		`ALTER TABLE movies ADD COLUMN collection_id INTEGER DEFAULT 0`,
		`ALTER TABLE movies ADD COLUMN collection_name TEXT DEFAULT ''`,
		`ALTER TABLE movies ADD COLUMN minimum_availability TEXT DEFAULT 'released'`,
		`ALTER TABLE movies ADD COLUMN release_date TEXT DEFAULT ''`,
	} {
		if _, err := db.ExecContext(ctx, col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migrate movies: %w", err)
		}
	}
	return nil
}

func (m *Module) ensureDatabaseHelpers(ctx context.Context) error {
	if err := m.ensureHistoryTable(ctx); err != nil {
		return err
	}
	if err := m.ensureMovieTitlesTable(ctx); err != nil {
		return err
	}
	return m.ensureCollectionPrefs(ctx)
}

func (m *Module) finishDatabaseOpen(ctx context.Context, db *sql.DB) error {
	if err := m.applyDatabasePragmas(ctx, db); err != nil {
		return err
	}
	if err := m.migrateMovieColumns(ctx, db); err != nil {
		return err
	}
	prev := m.db
	m.db = db
	err := m.ensureDatabaseHelpers(ctx)
	m.db = prev
	return err
}
