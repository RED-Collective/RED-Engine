package render

import (
	"regexp"
	"strings"
)

// LinkResolver maps an Obsidian wikilink/embed target to a URL. target is the bare
// reference (no surrounding brackets, no |alias, #heading or ^block suffix). embed
// reports whether the reference came from an embed (![[…]], asked about an asset)
// versus a plain link ([[…]], asked about a note). It returns the resolved URL and
// whether the target was found.
type LinkResolver func(target string, embed bool) (url string, ok bool)

var (
	// Inner capture excludes ']' and newlines so a match never spans two links.
	embedRe = regexp.MustCompile(`!\[\[([^\]\n]+)\]\]`)
	linkRe  = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
)

// ResolveObsidianLinks rewrites Obsidian wikilink and embed syntax into standard
// Markdown before the document reaches goldmark, which has no concept of [[…]] or
// ![[…]] (it would render them as literal text). Embeds become images, links become
// anchors, using resolve to turn a bare target name into a URL. An embed whose
// target is not an asset falls back to a note link (Obsidian note transclusion). A
// target that resolve cannot find at all degrades to its plain display text, so raw
// [[…]] never leaks onto the page.
//
// Embeds are processed before links because the embed regex consumes the leading
// '!' that would otherwise be left behind and re-matched as a plain link.
//
// Resolved URLs are wrapped in <…> so paths containing parentheses (legal in
// filenames, illegal in a bare Markdown destination) round-trip safely; the
// resolver is responsible for percent-escaping spaces.
func ResolveObsidianLinks(src string, resolve LinkResolver) string {
	if resolve == nil {
		return src
	}

	src = embedRe.ReplaceAllStringFunc(src, func(m string) string {
		name, heading, display := parseWikiTarget(embedRe.FindStringSubmatch(m)[1])
		if name == "" {
			if display != "" {
				return display
			}
			return m
		}
		if url, ok := resolve(name, true); ok {
			return "![" + display + "](<" + url + ">)"
		}
		// Not an asset — it may be a note being transcluded; degrade to a link.
		if url, ok := resolve(name, false); ok {
			return "[" + display + "](<" + appendAnchor(url, heading) + ">)"
		}
		return display
	})

	src = linkRe.ReplaceAllStringFunc(src, func(m string) string {
		name, heading, display := parseWikiTarget(linkRe.FindStringSubmatch(m)[1])
		if name == "" {
			if display != "" {
				return display
			}
			return m
		}
		if url, ok := resolve(name, false); ok {
			return "[" + display + "](<" + appendAnchor(url, heading) + ">)"
		}
		return display
	})

	return src
}

// WikiTarget is one wikilink or embed reference found in a document: the bare
// target name (no |alias, #heading, or #^block suffix) and whether it came from
// an embed (![[…]]) or a plain link ([[…]]).
type WikiTarget struct {
	Name  string
	Embed bool
}

// ExtractWikiTargets returns every wikilink and embed target in src. Embeds are
// extracted first and then masked out before plain links are scanned, because
// linkRe also matches the [[…]] inside a ![[…]] — the same embeds-first ordering
// ResolveObsidianLinks relies on. Pure-heading self-references ([[#Section]])
// have an empty name and are omitted. No dedup is applied; callers dedup with
// whatever key suits them. Like the render path, this has no code-fence
// awareness, so a wikilink inside a fenced code block is counted — keeping the
// extracted graph in sync with the links the renderer actually produces.
func ExtractWikiTargets(src string) []WikiTarget {
	var out []WikiTarget
	for _, m := range embedRe.FindAllStringSubmatch(src, -1) {
		if name, _, _ := parseWikiTarget(m[1]); name != "" {
			out = append(out, WikiTarget{Name: name, Embed: true})
		}
	}
	src = embedRe.ReplaceAllString(src, "")
	for _, m := range linkRe.FindAllStringSubmatch(src, -1) {
		if name, _, _ := parseWikiTarget(m[1]); name != "" {
			out = append(out, WikiTarget{Name: name, Embed: false})
		}
	}
	return out
}

// parseWikiTarget splits the inside of a [[…]] into its components. For
// "Note Title#Section|Shown text" it returns name="Note Title", heading="Section",
// display="Shown text". display falls back to the target name (then the heading) when
// no |alias is given, matching what Obsidian shows.
func parseWikiTarget(inner string) (name, heading, display string) {
	inner = strings.TrimSpace(inner)

	alias := ""
	if i := strings.Index(inner, "|"); i >= 0 {
		alias = strings.TrimSpace(inner[i+1:])
		inner = strings.TrimSpace(inner[:i])
	}
	// Drop a block reference (#^id) entirely; it has no rendered anchor here.
	if i := strings.Index(inner, "#^"); i >= 0 {
		inner = strings.TrimSpace(inner[:i])
	}
	if i := strings.Index(inner, "#"); i >= 0 {
		heading = strings.TrimSpace(inner[i+1:])
		inner = strings.TrimSpace(inner[:i])
	}
	name = inner

	switch {
	case alias != "":
		display = alias
	case name != "":
		display = name
	default:
		display = heading
	}
	return name, heading, display
}

// appendAnchor adds a best-effort heading anchor to a note URL. The slug is
// GitHub-style and approximate — it may not match goldmark's generated id exactly,
// in which case the link still navigates to the correct page.
func appendAnchor(url, heading string) string {
	if heading == "" {
		return url
	}
	return url + "#" + headingSlug(heading)
}

func headingSlug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
