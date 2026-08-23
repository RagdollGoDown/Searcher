package scraper

import (
	"context"
	"fmt"
	"strconv"
)

// Settings keys for the crawler's persisted, per-feature configuration,
// stored in store.Store's generic settings table alongside e.g. RAG's
// chunking settings.
const (
	settingDefaultMaxDepth   = "scraper.default_max_depth"
	settingDefaultMaxPages   = "scraper.default_max_pages"
	settingPoliteDelayMillis = "scraper.polite_delay_ms"
)

const (
	scraperDefaultMaxDepth          = 2
	scraperDefaultMaxPages          = 50
	scraperDefaultPoliteDelayMillis = 500
)

// Settings are the defaults applied to a crawl when its Params don't
// specify them, and the politeness delay used by every crawl.
type Settings struct {
	MaxDepth          int
	MaxPages          int
	PoliteDelayMillis int
}

// GetSettings returns the crawler's currently configured settings, falling
// back to the package defaults for any value that hasn't been explicitly
// set.
func (c *Crawler) GetSettings(ctx context.Context) (Settings, error) {
	maxDepth, err := c.getIntSetting(ctx, settingDefaultMaxDepth, scraperDefaultMaxDepth)
	if err != nil {
		return Settings{}, err
	}
	maxPages, err := c.getIntSetting(ctx, settingDefaultMaxPages, scraperDefaultMaxPages)
	if err != nil {
		return Settings{}, err
	}
	delay, err := c.getIntSetting(ctx, settingPoliteDelayMillis, scraperDefaultPoliteDelayMillis)
	if err != nil {
		return Settings{}, err
	}
	return Settings{MaxDepth: maxDepth, MaxPages: maxPages, PoliteDelayMillis: delay}, nil
}

// SetSettings persists s for crawls started from now on.
func (c *Crawler) SetSettings(ctx context.Context, s Settings) error {
	if s.MaxDepth <= 0 {
		return fmt.Errorf("scraper: max depth must be positive")
	}
	if s.MaxPages <= 0 || s.MaxPages > scraperMaxAllowedPages {
		return fmt.Errorf("scraper: max pages must be between 1 and %d", scraperMaxAllowedPages)
	}
	if s.PoliteDelayMillis < 0 {
		return fmt.Errorf("scraper: polite delay must not be negative")
	}
	if err := c.store.SetSetting(ctx, settingDefaultMaxDepth, strconv.Itoa(s.MaxDepth)); err != nil {
		return err
	}
	if err := c.store.SetSetting(ctx, settingDefaultMaxPages, strconv.Itoa(s.MaxPages)); err != nil {
		return err
	}
	return c.store.SetSetting(ctx, settingPoliteDelayMillis, strconv.Itoa(s.PoliteDelayMillis))
}

// getIntSetting returns the int stored under key, or def if it's unset or
// unparseable (so a corrupted setting degrades to the default instead of
// breaking a crawl).
func (c *Crawler) getIntSetting(ctx context.Context, key string, def int) (int, error) {
	v, ok, err := c.store.GetSetting(ctx, key)
	if err != nil {
		return 0, err
	}
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, nil
	}
	return n, nil
}
