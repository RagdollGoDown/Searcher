package scraper

import (
	"bytes"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// skipTextTags are elements whose contents aren't part of a page's readable
// text (script/style aren't visible; nav/header/footer/aside are usually
// site chrome repeated on every page, which would otherwise dominate a
// crawl's chunks with boilerplate instead of page-specific content).
var skipTextTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"nav": true, "header": true, "footer": true, "aside": true,
}

// extractText returns a page's visible text, in document order, with
// boilerplate/non-visible elements (see skipTextTags) dropped.
func extractText(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}

	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && skipTextTags[n.Data] {
			return
		}
		if n.Type == html.TextNode {
			if text := strings.TrimSpace(n.Data); text != "" {
				b.WriteString(text)
				b.WriteString("\n\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return strings.TrimSpace(b.String())
}

// extractTitle returns a page's <title> text, or "" if it has none.
func extractTitle(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}

	var title string
	var walk func(n *html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			title = strings.TrimSpace(n.FirstChild.Data)
			return true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(doc)

	return title
}

// extractLinks returns the absolute http(s) URLs (fragments stripped)
// linked from a page's <a href> attributes, resolved against pageURL.
func extractLinks(pageURL string, body []byte) []string {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}

	var links []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key != "href" {
					continue
				}
				if resolved := resolveLink(base, attr.Val); resolved != "" {
					links = append(links, resolved)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return links
}

// resolveLink resolves href against base, returning "" unless the result
// is an absolute http(s) URL. The fragment is stripped so links differing
// only by #anchor are treated as the same page.
func resolveLink(base *url.URL, href string) string {
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	resolved.Fragment = ""
	return resolved.String()
}

// sameDomain reports whether link's host matches root's host exactly.
func sameDomain(root *url.URL, link string) bool {
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	return u.Host == root.Host
}
