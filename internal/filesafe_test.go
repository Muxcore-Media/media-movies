package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeDeleteMediaFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Movies", "Film (2020)")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "Film.2020.mkv")
	if err := os.WriteFile(f, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := safeDeleteMediaFile(f, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("expected file removed")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("expected empty parent removed")
	}

	escape := filepath.Join(t.TempDir(), "outside.mkv")
	if err := os.WriteFile(escape, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = safeDeleteMediaFile(escape, root)
	if _, err := os.Stat(escape); err != nil {
		t.Fatal("path outside root must not be deleted")
	}
}

func TestSafeDeleteMediaFileSymlinkAndSibling(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "lib")
	evil := filepath.Join(base, "lib-evil")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, evil, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	victim := filepath.Join(outside, "victim.mkv")
	sib := filepath.Join(evil, "sib.mkv")
	for _, f := range []string{victim, sib} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// directory symlink inside root pointing outside
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	_ = safeDeleteMediaFile(filepath.Join(root, "dirlink", "victim.mkv"), root)
	_ = safeDeleteMediaFile(sib, root)
	for _, f := range []string{victim, sib} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s must survive: %v", f, err)
		}
	}
	// file symlink inside root: the link is unlinked, the target survives
	if err := os.Symlink(victim, filepath.Join(root, "filelink.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := safeDeleteMediaFile(filepath.Join(root, "filelink.mkv"), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Error("symlink target must survive")
	}
	if _, err := os.Lstat(filepath.Join(root, "filelink.mkv")); !os.IsNotExist(err) {
		t.Error("link itself should be removed")
	}
}
