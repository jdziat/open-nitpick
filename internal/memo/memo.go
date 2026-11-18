// Package memo shares immutable results within one operation.
package memo

import (
	"context"
	"errors"
	"sync"
)

// Cache coalesces concurrent lookups. Its zero value is ready to use.
// Results belong to one snapshot and must not be modified by callers.
type Cache[K comparable, V any] struct {
	mu      sync.Mutex
	entries map[K]*entry[V]
}

type entry[V any] struct {
	ready chan struct{}
	value V
	err   error
}

// Get retains successful loads; failures leave no retained result.
func (c *Cache[K, V]) Get(ctx context.Context, key K, load func() (V, error)) (V, error) {
	return c.GetIf(ctx, key, load, nil)
}

// GetIf shares loads but retains a result only when keep accepts it.
// keep must not call this cache; it runs while the cache is locked.
func (c *Cache[K, V]) GetIf(ctx context.Context, key K, load func() (V, error), keep func(V) bool) (V, error) {
	var zero V
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		c.mu.Lock()
		if found, ok := c.entries[key]; ok {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-found.ready:
				if errors.Is(found.err, context.Canceled) || errors.Is(found.err, context.DeadlineExceeded) {
					continue
				}
				return found.value, found.err
			}
		}
		found := &entry[V]{ready: make(chan struct{})}
		if c.entries == nil {
			c.entries = make(map[K]*entry[V])
		}
		c.entries[key] = found
		c.mu.Unlock()
		found.value, found.err = load()
		c.mu.Lock()
		if found.err != nil || (keep != nil && !keep(found.value)) {
			delete(c.entries, key)
		}
		close(found.ready)
		c.mu.Unlock()
		return found.value, found.err
	}
}
