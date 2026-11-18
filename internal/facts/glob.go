package facts

import (
	"path"
	"regexp"
	"strings"
)

// globToRegexp translates a repository glob (supporting `**`) into an anchored
// regular expression over slash-separated relative paths.
//
// `**` spans directories; `*` and `?` do not. This is the same dialect the
// registry uses in `covers` and `touch_sets`, so a path that a glob covers in
// the registry is a path the collector reads.
func globToRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")

	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
					continue
				}
				b.WriteString(".*")
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.':
			b.WriteString(`\.`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}

	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// Matches reports whether rel (slash-separated) is covered by any glob.
func Matches(globs []string, rel string) bool {
	rel = path.Clean(strings.ReplaceAll(rel, "\\", "/"))
	for _, g := range globs {
		if globToRegexp(g).MatchString(rel) {
			return true
		}
	}
	return false
}
