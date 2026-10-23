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
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	want := string(original) + "http://one.example:8080#pool\nhttp://two.example:8080#pool\n"
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

func TestMergeProxyImportNormalizesAndDeduplicatesPoolMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.txt")
	if err := os.WriteFile(path, []byte("http://proxy.example:8080#pool\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, added, err := mergeProxyImport(path, []string{"proxy.example:8080", "http://proxy.example:8080#pool", "pool+http://proxy.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("added = %d, want 0", added)
	}
	want := "http://proxy.example:8080#pool\n"
	if string(content) != want {
		t.Fatalf("file content = %q, want %q", content, want)
	}
}

func TestUniqueRuntimeProxyEntriesMatchesConfigDeduplication(t *testing.T) {
	got := uniqueRuntimeProxyEntries([]string{
		"http://same.example:8080#pool",
		"http://same.example:8080#pool",
		"http://same.example:8080",
		"http://static.example:8080",
		"http://static.example:8080",
	})
	want := []string{"http://same.example:8080#pool", "http://same.example:8080", "http://static.example:8080"}
	if len(got) != len(want) {
		t.Fatalf("unique entries = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unique entries = %#v, want %#v", got, want)
		}
	}
}

func TestStageProxyFileDeletesSelectedRowsAndKeepsComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	original := "# keep this comment\nhttp://one.example:8080#pool\nhttp://two.example:8080#pool\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	temp, err := stageProxyFile(path, []string{"http://one.example:8080#pool", "http://two.example:8080#pool"}, map[int]struct{}{0: {}})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(temp)
	content, err := os.ReadFile(temp)
	if err != nil {
		t.Fatal(err)
	}
	want := "# keep this comment\nhttp://two.example:8080#pool\n"
	if string(content) != want {
		t.Fatalf("staged content = %q, want %q", content, want)
	}
}
