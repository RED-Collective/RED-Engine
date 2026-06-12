package fetch

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeFolderSegment(t *testing.T) {
	cases := map[string]string{
		"awesome-markdown": "awesome-markdown", // plain name kept
		"crypto-notes":     "crypto-notes",
		".hidden":          "hidden", // leading dots stripped (never hidden)
		"...dots":          "dots",
		"a/b/c":            "c", // directory part dropped (single segment)
		"/abs/path/name":   "name",
		"  spaced  ":       "spaced", // trimmed
		"":                 "",       // empty → root (caller falls back)
		"..":               "",       // traversal → empty
		".":                "",
	}
	for in, want := range cases {
		if got := SafeFolderSegment(in); got != want {
			t.Errorf("SafeFolderSegment(%q) = %q, want %q", in, got, want)
		}
		// A non-empty result must always be a single, non-hidden, traversal-free segment.
		if got := SafeFolderSegment(in); got != "" {
			if strings.ContainsAny(got, `/\`) || strings.HasPrefix(got, ".") || strings.Contains(got, "..") {
				t.Errorf("SafeFolderSegment(%q) = %q is not a safe single segment", in, got)
			}
		}
	}
}

func TestGitCachePath(t *testing.T) {
	prev := gitCacheRoot
	defer func() { gitCacheRoot = prev }()

	// With no root configured, callers get "" and fall back to the legacy location.
	SetGitCacheRoot("")
	if got := GitCachePath("https://github.com/x/y"); got != "" {
		t.Fatalf("GitCachePath with no root = %q, want empty", got)
	}

	root := t.TempDir()
	SetGitCacheRoot(root)

	const url1 = "https://github.com/mundimark/awesome-markdown"
	const url2 = "https://github.com/StandardCodebase/Test-Repository"

	p1 := GitCachePath(url1)
	if p1 != GitCachePath(url1) { // stable across calls
		t.Fatal("GitCachePath is not stable for the same URL")
	}
	if !strings.HasPrefix(p1, root+string(filepath.Separator)) {
		t.Fatalf("cache path %q is not under the configured root %q", p1, root)
	}
	if p1 == GitCachePath(url2) {
		t.Fatal("different URLs must map to different cache dirs")
	}
	// The cache must live OUTSIDE the data dir — the whole point of the change.
	if strings.Contains(p1, string(filepath.Separator)+"data"+string(filepath.Separator)) {
		t.Fatalf("cache path %q unexpectedly inside a data dir", p1)
	}
}
