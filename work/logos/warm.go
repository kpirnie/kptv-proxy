// work/logos/warm.go
package logos

import (
	"kptv-proxy/work/constants"
	"kptv-proxy/work/logger"
	"sync"
)

// Warm fetches and caches a set of remote logo URLs, registering each so a
// later request resolves without an export having run. Fetches are bounded so a
// full channel list does not open one connection per logo.
func Warm(urls []string) {
	if len(urls) == 0 {
		return
	}

	sem := make(chan struct{}, constants.Internal.LogoWarmConcurrency)
	var wg sync.WaitGroup
	var cached int
	var mu sync.Mutex

	for _, url := range urls {
		wg.Add(1)
		sem <- struct{}{}

		go func(u string) {
			defer wg.Done()
			defer func() { <-sem }()

			Register(u)

			if _, err := EnsureCached(u); err != nil {
				logger.Debug("{logos/warm - Warm} fetch failed for %s: %v", u, err)
				return
			}

			mu.Lock()
			cached++
			mu.Unlock()
		}(url)
	}

	wg.Wait()

	logger.Debug("{logos/warm - Warm} warmed %d of %d logos", cached, len(urls))
}
