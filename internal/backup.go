package internal

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// ExportState implements contracts.Backupable — snapshot the library SQLite DB.
func (m *Module) ExportState(ctx context.Context) ([]byte, error) {
	m.mu.RLock()
	path := m.dbPath
	m.mu.RUnlock()
	if path == "" {
		return nil, fmt.Errorf("db path not set")
	}
	m.mu.Lock()
	if m.db != nil {
		_, _ = m.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	m.mu.Unlock()
	return os.ReadFile(path)
}

// ImportState replaces the library SQLite DB from a backup snapshot.
func (m *Module) ImportState(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty backup payload")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db != nil {
		_ = m.db.Close()
		m.db = nil
	}
	if err := os.WriteFile(m.dbPath, data, 0600); err != nil {
		return fmt.Errorf("write db: %w", err)
	}
	db, err := sql.Open("sqlite", m.dbPath)
	if err != nil {
		return fmt.Errorf("reopen sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := m.finishDatabaseOpen(ctx, db); err != nil {
		_ = db.Close()
		return err
	}
	m.db = db
	return nil
}

// BackupExtraPaths returns filesystem paths operators should include in
// BACKUP_SOURCE_DIRS alongside the SQLite export from ExportState.
func (m *Module) BackupExtraPaths() []string {
	if dir := m.getImageDir(); dir != "" {
		return []string{dir}
	}
	return nil
}
