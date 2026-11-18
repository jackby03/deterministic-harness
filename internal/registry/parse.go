// Package registry parses gates.yaml and decodes it into the closed schema the
// verifier understands.
//
// It contains a deliberately small YAML subset parser instead of a third-party
// dependency. Three reasons: the standard library has no YAML, Constitution
// §2.3 asks for the standard library first, and a strict subset that rejects
// what it does not understand is fail-closed, whereas a permissive parser
// silently accepts a key nobody reads.
//
// Supported: block maps, block lists, inline lists `[a, b]`, inline maps
// `{a: b}`, quoted and bare scalars, ints, floats, bools, null, comments.
// Rejected: tabs for indentation, duplicate keys in a map, unclosed flow
// collections, anchors, tags, multi-line scalars.
package registry

import (
	"fmt"
	"strconv"
	"strings"
)

type line struct {
	indent int
	text   string
	num    int
}

type parser struct {
	lines []line
	idx   int
	path  string
}

// Parse turns a document into nested map[string]any / []any / scalars.
func Parse(data []byte, path string) (map[string]any, error) {
	p := &parser{path: path}
	if err := p.scan(string(data)); err != nil {
		return nil, err
	}
	if len(p.lines) == 0 {
		return map[string]any{}, nil
	}
	if p.lines[0].indent != 0 {
		return nil, p.errf(p.lines[0], "document must start at column 0")
	}
	v, err := p.parseBlock(0)
	if err != nil {
		return nil, err
	}
	if p.idx != len(p.lines) {
		return nil, p.errf(p.lines[p.idx], "unexpected content")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: document is not a mapping", path)
	}
	return m, nil
}

func (p *parser) errf(l line, format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.path, l.num, fmt.Sprintf(format, args...))
}

func (p *parser) scan(src string) error {
	for i, raw := range strings.Split(src, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		if strings.TrimSpace(trimmed) == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(trimmed)
		if strings.ContainsRune(raw[:indent], '\t') || strings.HasPrefix(trimmed, "\t") {
			return fmt.Errorf("%s:%d: tabs are not valid indentation", p.path, i+1)
		}
		p.lines = append(p.lines, line{indent: indent, text: strings.TrimRight(trimmed, " \t"), num: i + 1})
	}
	return nil
}

func (p *parser) parseBlock(indent int) (any, error) {
	if p.idx < len(p.lines) && p.lines[p.idx].indent == indent && isItem(p.lines[p.idx].text) {
		return p.parseList(indent)
	}
	return p.parseMap(indent)
}

func isItem(text string) bool { return text == "-" || strings.HasPrefix(text, "- ") }

func (p *parser) parseMap(indent int) (map[string]any, error) {
	out := map[string]any{}
	for p.idx < len(p.lines) {
		l := p.lines[p.idx]
		if l.indent != indent || isItem(l.text) {
			break
		}
		key, rest, ok := splitKey(l.text)
		if !ok {
			return nil, p.errf(l, "expected `key: value`")
		}
		key = unquoteKey(key)
		if _, dup := out[key]; dup {
			return nil, p.errf(l, "duplicate key %q", key)
		}
		p.idx++

		val, err := p.parseValue(rest, indent, l)
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

func (p *parser) parseValue(rest string, indent int, l line) (any, error) {
	// `key:   # comment` followed by a nested block is common in this registry:
	// strip the comment before deciding whether a value was written.
	rest = strings.TrimSpace(stripComment(rest))
	if rest == "" {
		if p.idx < len(p.lines) && p.lines[p.idx].indent > indent {
			return p.parseBlock(p.lines[p.idx].indent)
		}
		return nil, nil
	}
	return parseFlowOrScalar(rest, p.path, l.num)
}

func (p *parser) parseList(indent int) ([]any, error) {
	var out []any
	for p.idx < len(p.lines) {
		l := p.lines[p.idx]
		if l.indent != indent || !isItem(l.text) {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l.text, "-"))
		if rest == "" {
			p.idx++
			if p.idx < len(p.lines) && p.lines[p.idx].indent > indent {
				v, err := p.parseBlock(p.lines[p.idx].indent)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
				continue
			}
			out = append(out, nil)
			continue
		}

		if strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, "[") {
			v, err := parseFlowOrScalar(rest, p.path, l.num)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			p.idx++
			continue
		}

		if _, _, isMap := splitKey(rest); isMap {
			// Rewrite `- key: value` as a map line one level in and let
			// parseMap consume it together with its continuation lines.
			p.lines[p.idx] = line{indent: indent + 2, text: rest, num: l.num}
			v, err := p.parseMap(indent + 2)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}

		v, err := parseFlowOrScalar(rest, p.path, l.num)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.idx++
	}
	return out, nil
}

// unquoteKey strips the quotes a block-map key may carry. Without this, the key
// `"a:b"` keeps its quotes and never matches the value it is supposed to name:
// a quoted benchmark key silently becomes a ceiling nothing is compared against.
func unquoteKey(k string) string {
	if len(k) < 2 {
		return k
	}
	if k[0] == 34 && k[len(k)-1] == 34 { // comillas dobles
		if v, err := strconv.Unquote(k); err == nil {
			return v
		}
		return k[1 : len(k)-1]
	}
	if k[0] == 39 && k[len(k)-1] == 39 { // comillas simples
		return k[1 : len(k)-1]
	}
	return k
}

// splitKey splits `key: value` at the first colon outside quotes and flow.
func splitKey(text string) (key, rest string, ok bool) {
	depth := 0
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ':' && depth == 0:
			if i+1 < len(text) && text[i+1] != ' ' && text[i+1] != '\t' {
				return "", "", false
			}
			return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:]), true
		}
	}
	return "", "", false
}

func parseFlowOrScalar(s, path string, line int) (any, error) {
	s = strings.TrimSpace(stripComment(s))
	if s == "" {
		return nil, nil
	}
	fp := &flowParser{s: s, path: path, line: line}
	v, err := fp.value()
	if err != nil {
		return nil, err
	}
	fp.ws()
	if fp.i != len(fp.s) {
		return nil, fmt.Errorf("%s:%d: trailing content %q", path, line, fp.s[fp.i:])
	}
	return v, nil
}

func (p *flowParser) value() (any, error) {
	p.ws()
	if p.i >= len(p.s) {
		return nil, fmt.Errorf("%s:%d: unexpected end of value", p.path, p.line)
	}
	switch p.s[p.i] {
	case '[':
		return p.list()
	case '{':
		return p.mapping()
	case '"', '\'':
		return p.quoted()
	default:
		return p.bare(), nil
	}
}

func (p *flowParser) list() (any, error) {
	p.i++ // [
	out := []any{}
	for {
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return out, nil
		}
		return nil, fmt.Errorf("%s:%d: expected , or ] in list", p.path, p.line)
	}
}

func (p *flowParser) mapping() (any, error) {
	p.i++ // {
	out := map[string]any{}
	for {
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return out, nil
		}
		k, err := p.quotedOrBareKey()
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, fmt.Errorf("%s:%d: expected : after key %q", p.path, p.line, k)
		}
		p.i++
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate key %q", p.path, p.line, k)
		}
		out[k] = v
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return out, nil
		}
		return nil, fmt.Errorf("%s:%d: expected , or } in map", p.path, p.line)
	}
}

func (p *flowParser) quotedOrBareKey() (string, error) {
	p.ws()
	if p.i < len(p.s) && (p.s[p.i] == '"' || p.s[p.i] == '\'') {
		v, err := p.quoted()
		if err != nil {
			return "", err
		}
		s, _ := v.(string)
		return s, nil
	}
	start := p.i
	for p.i < len(p.s) && p.s[p.i] != ':' {
		p.i++
	}
	return strings.TrimSpace(p.s[start:p.i]), nil
}

func (p *flowParser) quoted() (any, error) {
	q := p.s[p.i]
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == q {
			p.i++
			return b.String(), nil
		}
		if c == '\\' && p.i+1 < len(p.s) {
			p.i++
			switch p.s[p.i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(p.s[p.i])
			}
			p.i++
			continue
		}
		b.WriteByte(c)
		p.i++
	}
	return nil, fmt.Errorf("%s:%d: unterminated quoted string", p.path, p.line)
}

func (p *flowParser) bare() any {
	start := p.i
	for p.i < len(p.s) && p.s[p.i] != ',' && p.s[p.i] != ']' && p.s[p.i] != '}' {
		p.i++
	}
	raw := strings.TrimSpace(p.s[start:p.i])
	return scalar(raw)
}

func (p *flowParser) ws() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func scalar(raw string) any {
	switch raw {
	case "", "null", "~":
		return nil
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && strings.ContainsAny(raw, ".eE") {
		return f
	}
	return raw
}

// stripComment removes a trailing `#` comment that is outside quotes and flow.
func stripComment(s string) string {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == '#' && depth == 0:
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return s[:i]
			}
		}
	}
	return s
}

type flowParser struct {
	s    string
	i    int
	path string
	line int
}
