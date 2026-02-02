package proxy

import (
	"io"
	"kptv-proxy/work/logger"
	"net/http"
	"strings"
	"sync"
	"time"
)

// epgSource represents a single EPG data provider with its connection details.
// Each source is categorized by type to indicate its origin: Xtream Codes API
// endpoints, M3U8 playlist-associated EPGs, or manually configured EPG URLs.
type epgSource struct {
	url        string // full URL to fetch the XMLTV data from
	name       string // human-readable name of the source for logging
	sourceType string // origin category: "xc", "m3u8", or "manual"
}

// GetEPGSources aggregates all configured EPG data sources into a unified slice.
// It collects sources from three distinct configuration paths in priority order:
//   - Xtream Codes sources with valid credentials (constructed from base URL + auth)
//   - M3U8 sources that have an explicit EPG URL defined
//   - Manually configured standalone EPG endpoints
//
// This provides a single entry point for downstream consumers to retrieve every
// available EPG source regardless of its configuration origin.
func (sp *StreamProxy) GetEPGSources() []epgSource {
	var sources []epgSource

	logger.Debug("{proxy/epg - GetEPGSources} Collecting EPG sources from configuration")

	// collect manually configured standalone EPG endpoints
	for i := range sp.Config.EPGs {
		epg := &sp.Config.EPGs[i]
		sources = append(sources, epgSource{
			url:        epg.URL,
			name:       epg.Name,
			sourceType: "manual",
		})
		logger.Debug("{proxy/epg - GetEPGSources} Added manual EPG source: %s", epg.Name)
	}

	logger.Debug("{proxy/epg - GetEPGSources} Collected %d total EPG sources", len(sources))
	return sources
}

// FetchEPGData concurrently retrieves XMLTV data from all provided EPG sources,
// parsing the raw XML to extract channel definitions and programme listings into
// separate slices. Each source is fetched in its own goroutine with a 30-second
// timeout to prevent any single slow or unresponsive source from blocking the
// entire aggregation process.
//
// The function streams and parses XML data incrementally to handle large EPG files
// without loading them entirely into memory.
func (sp *StreamProxy) FetchEPGData(sources []epgSource) ([]string, []string) {

	logger.Debug("{proxy/epg - FetchEPGData} Starting concurrent fetch for %d sources", len(sources))

	channelChan := make(chan string, len(sources)*100)
	programmeChan := make(chan string, len(sources)*1000)
	var wg sync.WaitGroup

	for _, epgSrc := range sources {
		wg.Add(1)
		go func(source epgSource) {
			defer wg.Done()

			logger.Debug("{proxy/epg - FetchEPGData} Fetching from %s (%s)", source.name, source.sourceType)

			req, err := http.NewRequest("GET", source.url, nil)
			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to create request for %s: %v", source.name, err)
				return
			}

			// set a couple extra headers
			req.Header.Set("User-Agent", "KPTV-Proxy/1.0")
			req.Header.Set("Accept-Encoding", "identity")
			req.Header.Set("Connection", "close")

			// execute the request
			resp, err := sp.HttpClient.Do(req)
			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to fetch from %s: %v", source.name, err)
				return
			}
			defer resp.Body.Close()

			// make sure we have a good response
			if resp.StatusCode != http.StatusOK {
				logger.Error("{proxy/epg - FetchEPGData} HTTP %d from %s", resp.StatusCode, source.name)
				return
			}

			// make sure we can read the data
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to read from %s: %v", source.name, err)
				return
			}

			docStr := string(data)

			// if it's empty...
			if len(data) == 0 {
				logger.Warn("{proxy/epg - FetchEPGData} Empty response body from %s", source.name)
				return
			}

			// Extract <channel> elements
			channelCount := 0
			channelStart := 0
			for {
				start := strings.Index(docStr[channelStart:], "<channel ")
				if start == -1 {
					break
				}
				start += channelStart
				end := strings.Index(docStr[start:], "</channel>")
				if end == -1 {
					break
				}
				end += start + len("</channel>")
				channelChan <- docStr[start:end] + "\n"
				channelCount++
				channelStart = end
			}

			// Extract <programme> elements
			programmeCount := 0
			progStart := 0
			for {
				start := strings.Index(docStr[progStart:], "<programme ")
				if start == -1 {
					break
				}
				start += progStart
				end := strings.Index(docStr[start:], "</programme>")
				if end == -1 {
					break
				}
				end += start + len("</programme>")
				programmeChan <- docStr[start:end] + "\n"
				programmeCount++
				progStart = end
			}

			if channelCount == 0 && programmeCount == 0 {
				logger.Warn("{proxy/epg - FetchEPGData} No channels or programmes found in %s (%d bytes)", source.name, len(data))
			} else {
				logger.Debug("{proxy/epg - FetchEPGData} Processed %s: %d channels, %d programmes (%d bytes)", source.name, channelCount, programmeCount, len(data))
			}
		}(epgSrc)
	}

	go func() {
		wg.Wait()
		close(channelChan)
		close(programmeChan)
	}()

	var channels []string
	var programmes []string

	// Drain both channels concurrently to prevent deadlock.
	// If we drain sequentially (all channels then all programmes),
	// goroutines writing to programmeChan can block when its buffer
	// fills, which prevents wg.Wait() from completing, which prevents
	// channelChan from closing — a classic deadlock.
	var drainWg sync.WaitGroup
	drainWg.Add(2)

	go func() {
		defer drainWg.Done()
		for channelData := range channelChan {
			channels = append(channels, channelData)
		}
	}()

	go func() {
		defer drainWg.Done()
		for programmeData := range programmeChan {
			programmes = append(programmes, programmeData)
		}
	}()

	drainWg.Wait()

	logger.Debug("{proxy/epg - FetchEPGData} Fetch complete: %d total channels, %d total programmes", len(channels), len(programmes))
	return channels, programmes
}

// FetchAndMergeEPG orchestrates the complete EPG aggregation pipeline: collecting
// all configured sources, fetching their data concurrently, and merging the results
// into a single valid XMLTV document. The merged output contains all channel
// definitions followed by all programme listings, wrapped in proper XML structure
// with UTF-8 encoding declaration.
//
// Returns an empty string if no EPG sources are configured, allowing callers to
// handle the no-data case without additional error checking.
func (sp *StreamProxy) FetchAndMergeEPG() string {
	logger.Debug("{proxy/epg - FetchAndMergeEPG} Starting EPG aggregation pipeline")

	// get the sources
	sources := sp.GetEPGSources()
	if len(sources) == 0 {
		logger.Warn("{proxy/epg - FetchAndMergeEPG} No EPG sources configured, skipping merge")
		return ""
	}

	channels, programmes := sp.FetchEPGData(sources)

	// do we have data?
	if len(channels) == 0 && len(programmes) == 0 {
		logger.Warn("{proxy/epg - FetchAndMergeEPG} No EPG data retrieved from any source")
		return ""
	}

	// build the merged XMLTV document with channels first, then programmes
	var result strings.Builder
	result.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	result.WriteString(`<tv generator-info-name="KPTV-Proxy">` + "\n")

	// write out the data
	for _, ch := range channels {
		result.WriteString(ch)
	}
	for _, prog := range programmes {
		result.WriteString(prog)
	}

	// end the xml
	result.WriteString("</tv>")

	logger.Debug("{proxy/epg - FetchAndMergeEPG} Merged EPG complete: %d channels, %d programmes (%d bytes)", len(channels), len(programmes), result.Len())
	return result.String()
}

// StartEPGWarmup performs an initial EPG cache warmup on startup and then schedules
// automatic refreshes every 12 hours to keep the cached EPG data current. The initial
// warmup runs synchronously to ensure EPG data is available before the proxy begins
// serving requests, while subsequent refreshes run in a background goroutine to avoid
// blocking normal proxy operations.
func (sp *StreamProxy) StartEPGWarmup() {
	logger.Debug("{proxy/epg - StartEPGWarmup} Running initial EPG cache warmup")

	// run the initial warmup synchronously so EPG data is ready at startup
	sp.Cache.WarmUpEPG(func() string {
		return sp.FetchAndMergeEPG()
	})

	logger.Debug("{proxy/epg - StartEPGWarmup} Initial warmup complete, scheduling 12-hour refresh cycle")

	// schedule periodic background refreshes every 12 hours
	ticker := time.NewTicker(12 * time.Hour)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			logger.Debug("{proxy/epg - StartEPGWarmup} Starting scheduled EPG refresh")
			sp.Cache.WarmUpEPG(func() string {
				return sp.FetchAndMergeEPG()
			})
			logger.Debug("{proxy/epg - StartEPGWarmup} Scheduled EPG refresh complete")
		}
	}()
}
