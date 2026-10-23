package onboard

import (
	"context"
	"sync"
)

type cypressMetadataKey struct{}
type cypressMetadataEntry struct {
	data []byte
	err  error
}
type cypressMetadataCache struct {
	sync.Mutex
	entries map[string]cypressMetadataEntry
}

// WithCypressMetadataCache scopes both successful and failed metadata lookups to
// one validation invocation, including matrix rows and local preflight.
func WithCypressMetadataCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(cypressMetadataKey{}).(*cypressMetadataCache); ok {
		return ctx
	}
	return context.WithValue(ctx, cypressMetadataKey{}, &cypressMetadataCache{entries: map[string]cypressMetadataEntry{}})
}

func cachedCypressMetadata(ctx context.Context, key string, fetch func() ([]byte, error)) ([]byte, error) {
	cache, ok := ctx.Value(cypressMetadataKey{}).(*cypressMetadataCache)
	if !ok {
		return fetch()
	}
	cache.Lock()
	defer cache.Unlock()
	if entry, ok := cache.entries[key]; ok {
		return entry.data, entry.err
	}
	data, err := fetch()
	cache.entries[key] = cypressMetadataEntry{data, err}
	return data, err
}
