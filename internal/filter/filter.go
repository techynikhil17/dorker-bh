package filter

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/techynikhil17/dorker-bh/internal/discovery"
)

type predicate struct {
	kind, value       string
	negative, content bool
}
type Matcher struct{ predicates []predicate }

func (m Matcher) ContentTerms() []string {
	var out []string
	for _, p := range m.predicates {
		if p.content {
			out = append(out, p.value)
		}
	}
	return out
}

func Compile(template, target string) (Matcher, error) {
	template = strings.ReplaceAll(strings.ReplaceAll(template, "{target}", target), "%s", target)
	tokens, err := lex(template)
	if err != nil {
		return Matcher{}, err
	}
	var m Matcher
	for _, tok := range tokens {
		neg := strings.HasPrefix(tok.text, "-")
		if neg {
			tok.text = strings.TrimPrefix(tok.text, "-")
		}
		lower := strings.ToLower(tok.text)
		if strings.HasPrefix(lower, "site:") {
			if !strings.EqualFold(strings.TrimPrefix(tok.text, "site:"), target) {
				return Matcher{}, fmt.Errorf("site operator must match target %s", target)
			}
			continue
		}
		kind, value := "bare", tok.text
		if i := strings.Index(tok.text, ":"); i >= 0 {
			kind = strings.ToLower(tok.text[:i])
			value = tok.text[i+1:]
			if kind != "inurl" && kind != "ext" && kind != "filetype" {
				return Matcher{}, fmt.Errorf("unsupported dork operator %q", kind)
			}
		}
		if value == "" {
			return Matcher{}, fmt.Errorf("empty %s predicate", kind)
		}
		m.predicates = append(m.predicates, predicate{kind: kind, value: strings.ToLower(value), negative: neg, content: tok.quoted})
	}
	return m, nil
}

type token struct {
	text   string
	quoted bool
}

func lex(s string) ([]token, error) {
	var out []token
	for i := 0; i < len(s); {
		for i < len(s) && unicode.IsSpace(rune(s[i])) {
			i++
		}
		if i >= len(s) {
			break
		}
		quoted := s[i] == '"'
		if quoted {
			i++
			start := i
			for i < len(s) && s[i] != '"' {
				i++
			}
			if i == len(s) {
				return nil, fmt.Errorf("unterminated quote")
			}
			out = append(out, token{s[start:i], true})
			i++
			continue
		}
		start := i
		for i < len(s) && !unicode.IsSpace(rune(s[i])) {
			i++
		}
		out = append(out, token{s[start:i], false})
	}
	return out, nil
}

func (m Matcher) Match(obs discovery.Observation, includeUnverified bool) (bool, string) {
	u, err := url.Parse(obs.URL)
	if err != nil {
		return false, "invalid"
	}
	urlText := strings.ToLower(u.EscapedPath() + "?" + u.RawQuery)
	decoded, _ := url.PathUnescape(urlText)
	content := strings.ToLower(obs.Content)
	unverified := false
	for _, p := range m.predicates {
		var hit bool
		switch p.kind {
		case "inurl":
			hit = strings.Contains(decoded, p.value)
		case "ext", "filetype":
			hit = strings.EqualFold(strings.TrimPrefix(path.Ext(u.Path), "."), p.value)
		default:
			hit = strings.Contains(decoded, p.value)
			if p.content {
				hit = hit || strings.Contains(content, p.value)
				if !hit && !obs.Verified {
					if includeUnverified {
						unverified = true
						continue
					}
					return false, "unverified_filter"
				}
			}
		}
		if p.negative {
			hit = !hit
		}
		if !hit {
			return false, "not_matched"
		}
	}
	if unverified {
		return true, "unverified_filter"
	}
	return true, "matched"
}
