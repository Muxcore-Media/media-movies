package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func (m *Module) Settings() []contracts.SettingDef {
	return m.settingsDefs()
}

func (m *Module) UpdateSetting(key, value string) error {
	return m.updateSetting(key, value)
}

func (m *Module) settingsDefs() []contracts.SettingDef {
	return []contracts.SettingDef{
		{
			Key:         "db_path",
			Label:       "Library Database Path",
			Type:        contracts.SettingTypeString,
			Value:       m.dbPath,
			Description: "SQLite library path (MOVIES_DB_PATH); included in backups via Backupable / source dirs",
			Group:       "Paths",
		},
		{
			Key:         "image_dir",
			Label:       "Artwork Image Directory",
			Type:        contracts.SettingTypeString,
			Value:       m.getImageDir(),
			Description: "Local dir for posters/backdrops (MOVIES_IMAGE_DIR)",
			Group:       "Paths",
		},
	}
}

func (m *Module) updateSetting(key, value string) error {
	value = strings.TrimSpace(value)
	switch key {
	case "image_dir", "MOVIES_IMAGE_DIR":
		if value == "" {
			return fmt.Errorf("image_dir must not be empty")
		}
		if err := os.MkdirAll(value, 0700); err != nil {
			return fmt.Errorf("create image dir: %w", err)
		}
		m.cfgMu.Lock()
		m.imageDir = value
		m.cfgMu.Unlock()
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}

func (m *Module) getImageDir() string {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.imageDir
}
