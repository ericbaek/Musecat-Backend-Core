package stats

import (
	"context"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/sync/singleflight"
)

const (
	statsCacheTTL   = 30 * time.Minute
	statsStaleTTL   = 30 * time.Minute
	statsRetryDelay = 30 * time.Second
)

type statsCache struct {
	mu         sync.RWMutex
	value      map[string]any
	expires    time.Time
	dayEnds    time.Time
	retryAfter time.Time
	build      singleflight.Group
}

func getStatsCache(app core.App) *statsCache {
	return app.Store().GetOrSet("publicStatsCache", func() any { return &statsCache{} }).(*statsCache)
}

func (c *statsCache) cached() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Now().Before(c.expires) && time.Now().Before(c.dayEnds) {
		return c.value
	}
	return nil
}

func (c *statsCache) load(ctx context.Context, app core.App) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if value := c.cached(); value != nil {
		return value, nil
	}
	c.mu.RLock()
	var stale map[string]any
	if time.Now().Before(c.expires.Add(statsStaleTTL)) && time.Now().Before(c.dayEnds) {
		stale = c.value
	}
	retry := time.Now().Before(c.retryAfter)
	c.mu.RUnlock()
	if stale != nil && retry {
		return stale, nil
	}
	result := c.build.DoChan("stats", func() (any, error) {
		if value := c.cached(); value != nil {
			return value, nil
		}
		c.mu.RLock()
		if time.Now().Before(c.retryAfter) && time.Now().Before(c.expires.Add(statsStaleTTL)) && time.Now().Before(c.dayEnds) && c.value != nil {
			value := c.value
			c.mu.RUnlock()
			return value, nil
		}
		c.mu.RUnlock()
		// A disconnected viewer must not cancel the refresh for other viewers.
		queryCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		dayEnds := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
		value, err := queryStats(queryCtx, app)
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.retryAfter = time.Now().Add(statsRetryDelay)
			return nil, err
		}
		c.value, c.expires = value, time.Now().Add(statsCacheTTL)
		c.dayEnds = dayEnds
		c.retryAfter = time.Time{}
		return value, nil
	})
	// Serve the last successful aggregate while refreshing; never extend its
	// lifetime after an error. Cold, previous-UTC-date or more than one-hour-old data must wait.
	if stale != nil {
		return stale, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(map[string]any), nil
	}
}
