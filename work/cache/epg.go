package cache

import (
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

type EPGCache struct {
	cache    *ristretto.Cache[uint64, string]
	duration time.Duration
}

func NewEPGCache(duration time.Duration) *EPGCache {
	cache, err := ristretto.NewCache(&ristretto.Config[uint64, string]{
		NumCounters: 100,
		MaxCost:     100 << 20,
		BufferItems: 64,
	})
	if err != nil {
		panic(err)
	}

	return &EPGCache{
		cache:    cache,
		duration: duration,
	}
}

func (ec *EPGCache) Get() (string, bool) {
	value, found := ec.cache.Get(hashKey("epg:merged"))
	return value, found
}

func (ec *EPGCache) Set(value string) {
	ec.cache.SetWithTTL(hashKey("epg:merged"), value, int64(len(value)), ec.duration)
}

func (ec *EPGCache) Close() {
	ec.cache.Close()
}
