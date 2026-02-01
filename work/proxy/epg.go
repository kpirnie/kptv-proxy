package proxy

import (
	"context"
	"fmt"
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

	// collect Xtream Codes sources that have valid credentials
	for i := range sp.Config.Sources {
		src := &sp.Config.Sources[i]
		if src.Username != "" && src.Password != "" {
			sources = append(sources, epgSource{
				url:        fmt.Sprintf("%s/xmltv.php?username=%s&password=%s", src.URL, src.Username, src.Password),
				name:       src.Name,
				sourceType: "xc",
			})
			logger.Debug("{proxy/epg - GetEPGSources} Added XC source: %s", src.Name)
		}
	}

	// collect M3U8 sources that define a separate EPG URL
	for i := range sp.Config.Sources {
		src := &sp.Config.Sources[i]
		if src.EPGURL != "" {
			sources = append(sources, epgSource{
				url:        src.EPGURL,
				name:       src.Name,
				sourceType: "m3u8",
			})
			logger.Debug("{proxy/epg - GetEPGSources} Added M3U8 EPG source: %s", src.Name)
		}
	}

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
// The function uses buffered channels to safely collect results from concurrent
// goroutines, then drains them into ordered slices once all fetches complete.
// Individual source failures are logged and skipped without affecting other sources.
func (sp *StreamProxy) FetchEPGData(sources []epgSource) ([]string, []string) {

	logger.Debug("{proxy/epg - FetchEPGData} Starting concurrent fetch for %d sources", len(sources))

	// buffered channels to collect parsed XML fragments from concurrent goroutines
	channelChan := make(chan string, len(sources)*100)
	programmeChan := make(chan string, len(sources)*1000)
	var wg sync.WaitGroup

	for _, epgSrc := range sources {
		wg.Add(1)
		go func(source epgSource) {
			defer wg.Done()

			logger.Debug("{proxy/epg - FetchEPGData} Fetching from %s (%s)", source.name, source.sourceType)

			// enforce a 30-second timeout per source to avoid blocking on slow providers
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// setup the request
			req, err := http.NewRequestWithContext(ctx, "GET", source.url, nil)
			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to create request for %s: %v", source.name, err)
				return
			}

			// execute the actual request
			resp, err := sp.HttpClient.Do(req)
			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to fetch from %s: %v", source.name, err)
				return
			}
			defer resp.Body.Close()

			// invalid status code
			if resp.StatusCode != http.StatusOK {
				logger.Error("{proxy/epg - FetchEPGData} HTTP %d from %s", resp.StatusCode, source.name)
				return
			}

			// read the entire response body for string-based XML parsing
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to read from %s: %v", source.name, err)
				return
			}

			docStr := string(data)

			// if theres an empty response
			if len(data) == 0 {
				logger.Warn("{proxy/epg - FetchEPGData} Empty response body from %s", source.name)
				return
			}

			// extract all <channel> elements by scanning for open/close tag pairs
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
					logger.Warn("{proxy/epg - FetchEPGData} Malformed <channel> element in %s (missing closing tag)", source.name)
					break
				}
				end += start + len("</channel>")
				channelChan <- docStr[start:end] + "\n"
				channelCount++
				channelStart = end
			}

			// extract all <programme> elements using the same scanning approach
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
					logger.Warn("{proxy/epg - FetchEPGData} Malformed <programme> element in %s (missing closing tag)", source.name)
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

	// close channels once all goroutines complete to unblock the drain loops below
	go func() {
		wg.Wait()
		close(channelChan)
		close(programmeChan)
	}()

	// drain both channels into ordered slices for the caller
	var channels []string
	var programmes []string

	for channelData := range channelChan {
		channels = append(channels, channelData)
	}

	for programmeData := range programmeChan {
		programmes = append(programmes, programmeData)
	}

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
