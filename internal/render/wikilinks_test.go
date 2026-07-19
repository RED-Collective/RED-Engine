package render

import (
	"strings"
	"testing"
)

// stubResolver resolves a fixed set of names. It mirrors the real store closure:
// embeds (embed=true) look up assets and return a /content/ URL; links (embed=false)
// look up notes and return an article path.
func stubResolver(target string, embed bool) (string, bool) {
	if embed {
		switch strings.ToLower(target) {
		case "a.png":
			return "/content/Testing/a.png", true
		}
		return "", false
	}
	switch strings.ToLower(target) {
	case "note", "other note":
		return "/Testing/Other%20Note", true
	}
	return "", false
}

func TestResolveObsidianLinks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring that must appear
		deny string // substring that must NOT appear ("" = no check)
	}{
		{"embed image", "![[a.png]]", "![a.png](</content/Testing/a.png>)", "[["},
		{"plain link", "[[Note]]", "[Note](</Testing/Other%20Note>)", "[["},
		{"aliased link", "[[Note|Click here]]", "[Click here](</Testing/Other%20Note>)", "[["},
		{"heading anchor", "[[Note#Some Section]]", "/Testing/Other%20Note#some-section>", "[["},
		{"unresolved link degrades to text", "see [[Missing Note]] here", "see Missing Note here", "[["},
		{"unresolved embed degrades to text", "![[missing.png]]", "missing.png", "[["},
		{"embed of a note transcludes as link", "![[Note]]", "[Note](</Testing/Other%20Note>)", "[["},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveObsidianLinks(c.in, stubResolver)
			if !strings.Contains(got, c.want) {
				t.Errorf("got %q, want it to contain %q", got, c.want)
			}
			if c.deny != "" && strings.Contains(got, c.deny) {
				t.Errorf("got %q, must not contain %q", got, c.deny)
			}
		})
	}
}

// TestResolveObsidianLinksNilResolver: a nil resolver is a no-op (source returned
// unchanged), never a panic.
func TestResolveObsidianLinksNilResolver(t *testing.T) {
	in := "![[a.png]] and [[Note]]"
	if got := ResolveObsidianLinks(in, nil); got != in {
		t.Errorf("nil resolver should pass src through unchanged, got %q", got)
	}
}

func TestExtractWikiTargets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []WikiTarget
	}{
		{"plain link", "[[Note]]", []WikiTarget{{"Note", false}}},
		{"embed counted once", "![[a.png]]", []WikiTarget{{"a.png", true}}},
		{"alias stripped", "[[Note|Click here]]", []WikiTarget{{"Note", false}}},
		{"heading stripped", "[[Note#Some Section]]", []WikiTarget{{"Note", false}}},
		{"block ref stripped", "[[Note#^blockid]]", []WikiTarget{{"Note", false}}},
		{"pure-heading self-ref omitted", "[[#Local Heading]]", nil},
		{"link and embed of same target", "see [[A]] and ![[A]] together", []WikiTarget{{"A", true}, {"A", false}}},
		{"two links on one line", "[[A]] then [[B]]", []WikiTarget{{"A", false}, {"B", false}}},
		{"empty input", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractWikiTargets(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("target %d: got %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestHeadingSlug(t *testing.T) {
	cases := map[string]string{
		"Some Section":     "some-section",
		"  Trimmed  ":      "trimmed",
		"Mixed_CASE-123":   "mixed-case-123",
		"!!!punctuation!!!": "punctuation",
	}
	for in, want := range cases {
		if got := headingSlug(in); got != want {
			t.Errorf("headingSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
