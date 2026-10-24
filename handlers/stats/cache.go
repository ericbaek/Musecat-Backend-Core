package stats

import (
	"context"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/sync/singleflight"
)

const statsCacheTTL = 30 * time.Minute

type statsCache struct {
	mu      sync.RWMutex
	value   map[string]any
	expires time.Time
	build   singleflight.Group
}

func getStatsCache(app core.App) *statsCache {
	return app.Store().GetOrSet("publicStatsCache", func() any { return &statsCache{} }).(*statsCache)
}

func (c *statsCache) cached() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Now().Before(c.expires) {
		return c.value
	}
	return nil
}

func (c *statsCache) load(ctx context.Context, app core.App) (map[string]any, error) {
	if value := c.cached(); value != nil {
		return value, nil
	}
	result := c.build.DoChan("stats", func() (any, error) {
		if value := c.cached(); value != nil {
			return value, nil
		}
		// A disconnected viewer must not cancel the refresh for other viewers.
		queryCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		value, err := queryStats(queryCtx, app)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.value, c.expires = value, time.Now().Add(statsCacheTTL)
		c.mu.Unlock()
		return value, nil
	})
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
