package internal

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGoModSiblingFreePins ensures this module pins published MuxCore modules
// and does not replace them onto a sibling ../core (or ../contracts-*) checkout.
func TestGoModSiblingFreePins(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	goMod := filepath.Join(filepath.Dir(thisFile), "..", "go.mod")
	f, err := os.Open(goMod)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	inReplace := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "replace ") {
			inReplace = true
		}
		if inReplace || strings.HasPrefix(line, "replace ") {
			if strings.Contains(line, "../core") || strings.Contains(line, "../contracts") {
				t.Fatalf("sibling replace forbidden in go.mod: %s", line)
			}
		}
		if inReplace && line == ")" {
			inReplace = false
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}
