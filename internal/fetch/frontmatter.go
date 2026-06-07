package fetch

import (
	"bufio"
	"encoding/json"
	"strings"
)

// frontmatterValue returns the trimmed value of key inside the leading `---`
// frontmatter block, or "" if the block or key is absent.
func frontmatterValue(content []byte, key string) string {
	s := string(content)
	if !strings.HasPrefix(s, "---") {
		return ""
	}

	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	opened := false
	for sc.Scan() {
		line := sc.Text()
		if !opened {
			// The very first line must be the opening delimiter.
			if strings.TrimSpace(line) != "---" {
				return ""
			}
			opened = true
			continue
		}
		if strings.TrimSpace(line) == "---" {
			return "" // closing delimiter reached without finding the key
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// FrontmatterTags returns the note's tags from its `red_tags` frontmatter key.
// RED-Feather writes them as a JSON array (e.g. `red_tags: ["Electronics","Security"]`);
// a bare comma-separated value is accepted as a fallback. Tags are trimmed and
// blanks dropped; nil is returned when there are none. This is the single source
// of truth for tag extraction shared by the store and navigation scanners.
func FrontmatterTags(content []byte) []string {
	raw := frontmatterValue(content, "red_tags")
	if raw == "" {
		return nil
	}

	var tags []string
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			tags = nil
		}
	}
	if tags == nil {
		tags = strings.Split(raw, ",")
	}

	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
