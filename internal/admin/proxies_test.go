package admin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMergeProxyImportPreservesExistingFileAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	original := []byte("# existing\nhttp://one.example:8080\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	content, added, err := mergeProxyImport(path, []string{"http://one.example:8080", "http://two.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	want := string(original) + "http://two.example:8080\n"
	if string(content) != want {
		t.Fatalf("file content = %q, want %q", content, want)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %o, want 600", info.Mode().Perm())
		}
	}
}
