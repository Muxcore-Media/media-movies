package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsImageDir(t *testing.T) {
	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "m.db"),
		ImageDir: filepath.Join(t.TempDir(), "images"),
	})
	defs := m.Settings()
	foundImage := false
	for _, d := range defs {
		if d.Key == "image_dir" {
			foundImage = true
			break
		}
	}
	if !foundImage {
		t.Fatalf("missing image_dir in defs=%+v", defs)
	}
	next := filepath.Join(t.TempDir(), "images2")
	if err := m.UpdateSetting("image_dir", next); err != nil {
		t.Fatal(err)
	}
	if got := m.getImageDir(); got != next {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(next); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateSetting("unknown", "x"); err == nil {
		t.Fatal("expected error")
	}
}
