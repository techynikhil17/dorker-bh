package crawl

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

type link struct{ url, source string }

var jsURL = regexp.MustCompile(`(?i)(?:https?://[^\s"'<>]+|(?:\.\.?/|/)[A-Za-z0-9_./?&=%+@~-]+)`)

func extract(baseRaw, ctype, body string) []link {
	base, err := url.Parse(baseRaw)
	if err != nil {
		return nil
	}
	var out []link
	add := func(raw, source string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "javascript:") {
			return
		}
		u, e := url.Parse(raw)
		if e != nil {
			return
		}
		out = append(out, link{base.ResolveReference(u).String(), source})
	}
	lower := strings.ToLower(ctype)
	if strings.Contains(lower, "html") || strings.Contains(strings.ToLower(body), "<html") || strings.Contains(body, "<a ") {
		z := html.NewTokenizer(strings.NewReader(body))
		for {
			tt := z.Next()
			if tt == html.ErrorToken {
				break
			}
			if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
				continue
			}
			t := z.Token()
			for _, a := range t.Attr {
				switch strings.ToLower(a.Key) {
				case "href", "src", "action", "poster", "data-src":
					add(a.Val, "html")
				case "srcset":
					for _, v := range strings.Split(a.Val, ",") {
						add(strings.Fields(v)[0], "html")
					}
				case "content":
					if t.Data == "meta" && strings.Contains(strings.ToLower(a.Val), "url=") {
						p := strings.SplitN(a.Val, "=", 2)
						if len(p) == 2 {
							add(p[1], "html")
						}
					}
				}
			}
		}
	}
	if strings.Contains(lower, "xml") || strings.Contains(body, "<urlset") || strings.Contains(body, "<sitemapindex") {
		re := regexp.MustCompile(`(?is)<loc>\s*([^<]+)\s*</loc>`)
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			add(html.UnescapeString(m[1]), "sitemap")
		}
	}
	if strings.HasSuffix(base.Path, "robots.txt") {
		for _, line := range strings.Split(body, "\n") {
			p := strings.SplitN(line, ":", 2)
			if len(p) == 2 {
				k := strings.ToLower(strings.TrimSpace(p[0]))
				if k == "allow" || k == "disallow" || k == "sitemap" {
					add(strings.TrimSpace(p[1]), "robots")
				}
			}
		}
	}
	if strings.Contains(lower, "javascript") || strings.HasSuffix(base.Path, ".js") {
		for _, raw := range jsURL.FindAllString(body, -1) {
			add(raw, "javascript")
		}
		if i := strings.Index(body, "sourceMappingURL="); i >= 0 {
			add(strings.Fields(body[i+17:])[0], "source_map")
		}
	}
	return out
}
