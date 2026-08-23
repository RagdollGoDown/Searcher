package scraper

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxRobotsBytes caps how much of a robots.txt response is read, so a
// misbehaving server can't stall or balloon memory on small hardware.
const maxRobotsBytes = 200 * 1024

// robotsRules is a deliberately minimal robots.txt interpretation for a
// self-hosted personal tool, not a production-grade crawler: only
// "User-agent: *" rules are honored, Disallow is a plain path-prefix
// match with no wildcards, and there's no Allow-override precedence or
// Crawl-delay support (politeness is instead controlled by the crawler's
// own configured delay setting).
type robotsRules struct {
	disallow []string
}

// allowed reports whether path is not blocked by any Disallow rule.
func (r *robotsRules) allowed(path string) bool {
	if r == nil {
		return true
	}
	for _, prefix := range r.disallow {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return true
}

// fetchRobots fetches and parses root's robots.txt. Any failure (no
// robots.txt, network error, oversized response) is treated as "no rules",
// matching how a real crawler should fail open rather than block an entire
// crawl over an unreachable robots.txt.
func fetchRobots(ctx context.Context, client *http.Client, root *url.URL) *robotsRules {
	robotsURL := &url.URL{Scheme: root.Scheme, Host: root.Host, Path: "/robots.txt"}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL.String(), nil)
	if err != nil {
		return &robotsRules{}
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return &robotsRules{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &robotsRules{}
	}

	return parseRobots(io.LimitReader(resp.Body, maxRobotsBytes))
}

// parseRobots parses a robots.txt body, collecting Disallow rules that
// apply to "User-agent: *" (or to no preceding User-agent line at all).
func parseRobots(r io.Reader) *robotsRules {
	rules := &robotsRules{}
	applies := true // rules before any User-agent line apply to everyone

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		field = strings.ToLower(strings.TrimSpace(field))
		value = strings.TrimSpace(value)

		switch field {
		case "user-agent":
			applies = value == "*"
		case "disallow":
			if applies && value != "" {
				rules.disallow = append(rules.disallow, value)
			}
		}
	}

	return rules
}
