package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"kptv-proxy/work/config"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/epgindex"
	"kptv-proxy/work/logger"
	"kptv-proxy/work/schedulesdirect"
	"kptv-proxy/work/utils"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/gzip"
)

// DummyChannelID is the XMLTV id of the synthetic keep-alive channel. It is
// always retained when filtering so the exported guide is never empty.
const DummyChannelID = "kptv-proxy-dummy"

var (
	reEPGChannelID        = regexp.MustCompile(`id="([^"]*)"`)
	reEPGProgrammeChannel = regexp.MustCompile(`channel="([^"]*)"`)
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
// Programme fragments are handed to progSink as they arrive rather than being
// collected in memory; only the channel elements are returned.
func (sp *StreamProxy) FetchEPGData(sources []epgSource, progSink func(string)) []string {

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

			req.Header.Set("User-Agent", "KPTV-Proxy/1.0")
			req.Header.Set("Accept-Encoding", "identity")
			req.Header.Set("Connection", "close")

			var resp *http.Response
			maxRetries := constants.Internal.EPGMaxRetries
			for attempt := 1; attempt <= maxRetries; attempt++ {
				resp, err = sp.HttpClient.Do(req)
				if err != nil {
					logger.Warn("{proxy/epg - FetchEPGData} Attempt %d/%d failed for %s: %v", attempt, maxRetries, source.name, err)
					time.Sleep(constants.Internal.EPGRetryBaseDelay)
					req, _ = http.NewRequest("GET", source.url, nil)
					req.Header.Set("User-Agent", "KPTV-Proxy/1.0")
					req.Header.Set("Accept-Encoding", "identity")
					req.Header.Set("Connection", "close")
					continue
				}
				if resp.StatusCode == http.StatusOK {
					break
				}
				logger.Warn("{proxy/epg - FetchEPGData} Attempt %d/%d HTTP %d from %s", attempt, maxRetries, resp.StatusCode, source.name)
				resp.Body.Close()
				if attempt < maxRetries {
					time.Sleep(constants.Internal.EPGRetryBaseDelay)
					req, _ = http.NewRequest("GET", source.url, nil)
					req.Header.Set("User-Agent", "KPTV-Proxy/1.0")
					req.Header.Set("Accept-Encoding", "identity")
					req.Header.Set("Connection", "close")
				}
			}

			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to fetch from %s after %d attempts: %v", source.name, maxRetries, err)
				return
			}
			if resp.StatusCode != http.StatusOK {
				logger.Error("{proxy/epg - FetchEPGData} HTTP %d from %s after %d attempts", resp.StatusCode, source.name, maxRetries)
				resp.Body.Close()
				return
			}
			defer resp.Body.Close()

			// sniff the first two bytes for the gzip magic number so both
			// plain .xml and .xml.gz sources are handled transparently
			br := bufio.NewReaderSize(resp.Body, 64*1024)
			var reader io.Reader = br
			if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
				gzr, err := gzip.NewReader(br)
				if err != nil {
					logger.Error("{proxy/epg - FetchEPGData} Failed to init gzip reader for %s: %v", source.name, err)
					resp.Body.Close()
					return
				}
				defer gzr.Close()
				reader = gzr
				logger.Debug("{proxy/epg - FetchEPGData} Detected gzip content from %s", source.name)
			}

			// stream-scan the document, emitting complete fragments as they
			// arrive instead of buffering the entire response in memory
			channelCount, programmeCount, bytesRead, err := scanEPGFragments(reader, channelChan, programmeChan)
			if err != nil {
				logger.Error("{proxy/epg - FetchEPGData} Failed to read from %s: %v", source.name, err)
				return
			}

			// if it's empty...
			if bytesRead == 0 {
				logger.Warn("{proxy/epg - FetchEPGData} Empty response body from %s", source.name)
				return
			}

			if channelCount == 0 && programmeCount == 0 {
				logger.Warn("{proxy/epg - FetchEPGData} No channels or programmes found in %s (%d bytes)", source.name, bytesRead)
			} else {
				logger.Debug("{proxy/epg - FetchEPGData} Processed %s: %d channels, %d programmes (%d bytes)", source.name, channelCount, programmeCount, bytesRead)
			}

		}(epgSrc)
	}

	go func() {
		wg.Wait()
		close(channelChan)
		close(programmeChan)
	}()

	var channels []string

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

	var progDrained int
	go func() {
		defer drainWg.Done()
		for programmeData := range programmeChan {
			progSink(programmeData)
			progDrained++
		}
	}()

	drainWg.Wait()

	logger.Debug("{proxy/epg - FetchEPGData} Fetch complete: %d total channels, %d total programmes", len(channels), progDrained)
	return channels
}

// FetchAndMergeEPG orchestrates the complete EPG aggregation pipeline: collecting
// all configured sources, fetching their data concurrently, and merging the results
// into a single valid XMLTV document. Programme elements are spilled to a temp
// file as they arrive and streamed back out during the merge, so peak memory is
// bounded by the channel list rather than the full document size.
//
// Streams the merged document into w and returns false if no EPG sources are
// configured or no data was retrieved, allowing callers to handle the no-data
// case without additional error checking.
func (sp *StreamProxy) FetchAndMergeEPG(w io.Writer) (bool, error) {
	logger.Debug("{proxy/epg - FetchAndMergeEPG} Starting EPG aggregation pipeline")

	sources := sp.GetEPGSources()
	hasURLSources := len(sources) > 0

	hasSDSources := false
	for _, acc := range sp.Config.SDAccounts {
		if acc.Enabled {
			hasSDSources = true
			break
		}
	}

	if !hasURLSources && !hasSDSources {
		logger.Warn("{proxy/epg - FetchAndMergeEPG} No EPG sources configured, skipping merge")
		return false, nil
	}

	// load the channel mapping up front so programmes can be expanded as
	// they stream through instead of being collected and filtered later
	rev := loadExportIDMap()

	// spill programmes to a temp file as they arrive; programmes are the
	// bulk of the document and holding them in memory is what drove the
	// multi-GB refresh spikes
	spill, err := os.CreateTemp("", "kptv-epg-progs-*.tmp")
	if err != nil {
		logger.Error("{proxy/epg - FetchAndMergeEPG} Failed to create programme spill file: %v", err)
		return false, err
	}
	defer os.Remove(spill.Name())
	defer spill.Close()
	spillW := bufio.NewWriterSize(spill, 256*1024)

	var (
		progMu     sync.Mutex
		progCount  int
		spillErr   error
		indexProgs []string // dummy + export copies, fed to the programme index

	)

	// progSink writes one programme fragment plus one rewritten copy per
	// proxy channel mapped to its epg id. Fragments without a channel
	// attribute are dropped.
	progSink := func(prog string) {
		m := reEPGProgrammeChannel.FindStringSubmatch(prog)
		if m == nil {
			return
		}
		progMu.Lock()
		defer progMu.Unlock()
		if spillErr != nil {
			return
		}
		// always keep the original source entry so the full guide exports
		if _, err := spillW.WriteString(prog); err != nil {
			spillErr = err
			return
		}
		progCount++

		// index the dummy entry and the originals of mapped ids so raw-id
		// guide lookups resolve
		if m[1] == DummyChannelID || len(rev[m[1]]) > 0 {
			indexProgs = append(indexProgs, prog)
		}

		// additionally emit one copy per proxy channel mapped to this epg id
		for _, exportID := range rev[m[1]] {
			rewritten := strings.Replace(prog, `channel="`+m[1]+`"`, `channel="`+exportID+`"`, 1)
			if _, err := spillW.WriteString(rewritten); err != nil {
				spillErr = err
				return
			}
			indexProgs = append(indexProgs, rewritten)
			progCount++
		}
	}

	// new — pre-seed with the dummy entry so the document is never empty
	dummyCh, dummyProg := generateDummyEPGEntry()
	allChannels := []string{dummyCh}
	progSink(dummyProg)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	// Fetch URL-based sources concurrently
	if hasURLSources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			channels := sp.FetchEPGData(sources, progSink)
			mu.Lock()
			allChannels = append(allChannels, channels...)
			mu.Unlock()
			logger.Debug("{proxy/epg - FetchAndMergeEPG} URL sources complete: %d channels", len(channels))
		}()
	}

	// Fetch each enabled SD account concurrently
	for _, acc := range sp.Config.SDAccounts {
		if !acc.Enabled {
			continue
		}
		wg.Add(1)
		go func(account config.SDAccount) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			data, err := schedulesdirect.FetchAccount(ctx, account)
			if err != nil {
				logger.Error("{proxy/epg - FetchAndMergeEPG} SD account %s failed: %v", account.Name, err)
				return
			}

			channels, programmes := schedulesdirect.GenerateXMLTV(data)

			for _, prog := range programmes {
				progSink(prog)
			}

			mu.Lock()
			allChannels = append(allChannels, channels...)
			mu.Unlock()

			logger.Debug("{proxy/epg - FetchAndMergeEPG} SD account %s complete: %d channels, %d programmes",
				account.Name, len(channels), len(programmes))
		}(acc)
	}

	wg.Wait()

	if spillErr != nil {
		logger.Error("{proxy/epg - FetchAndMergeEPG} Failed writing programme spill: %v", spillErr)
		return false, spillErr
	}

	// index the raw, unfiltered channel list so the mapping picker always
	// offers real source ids, never the rewritten per-channel export ids
	epgindex.RebuildFromSlices(allChannels)

	// index the export-copy programmes so XC get_short_epg /
	// get_simple_data_table lookups work after warmup and scheduled
	// refreshes, not only after an HTTP cache-miss rebuild
	epgindex.RebuildProgrammesFromSlices(indexProgs)

	// restrict the exported guide to channels that are actually mapped in the app
	allChannels = expandMappedChannels(allChannels, rev)

	// flush the spill and rewind it for the merge copy
	if err := spillW.Flush(); err != nil {
		logger.Error("{proxy/epg - FetchAndMergeEPG} Failed flushing programme spill: %v", err)
		return false, err
	}
	if _, err := spill.Seek(0, io.SeekStart); err != nil {
		logger.Error("{proxy/epg - FetchAndMergeEPG} Failed rewinding programme spill: %v", err)
		return false, err
	}

	const (
		epgHeader = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<tv generator-info-name="KPTV-Proxy">` + "\n"
		epgFooter = "</tv>"
	)

	// stream the merged document directly to the writer instead of building
	// it in memory; the caller owns buffering and atomic commit
	var written int64
	writeChunk := func(s string) error {
		n, err := io.WriteString(w, s)
		written += int64(n)
		return err
	}

	if err := writeChunk(epgHeader); err != nil {
		return false, err
	}
	for _, ch := range allChannels {
		if err := writeChunk(ch); err != nil {
			return false, err
		}
	}

	// stream the spilled programmes straight into the output
	n, err := io.Copy(w, spill)
	written += n
	if err != nil {
		return false, err
	}

	if err := writeChunk(epgFooter); err != nil {
		return false, err
	}

	logger.Debug("{proxy/epg - FetchAndMergeEPG} Merged EPG complete: %d channels, %d programmes (%d bytes)",
		len(allChannels), progCount, written)

	// the EPG has been streamed out
	return true, nil
}

// StartEPGWarmup performs an initial EPG cache warmup on startup and then schedules
// automatic refreshes every 12 hours to keep the cached EPG data current. The initial
// warmup runs synchronously to ensure EPG data is available before the proxy begins
// serving requests, while subsequent refreshes run in a background goroutine to avoid
// blocking normal proxy operations.
func (sp *StreamProxy) StartEPGWarmup() {
	logger.Debug("{proxy/epg - StartEPGWarmup} Running initial EPG cache warmup")

	// initial warmup
	sp.Cache.WarmUpEPG(sp.FetchAndMergeEPG)
	logger.Debug("{proxy/epg - StartEPGWarmup} Initial warmup complete, scheduling 12-hour refresh cycle")

	// schedule periodic background refreshes every 12 hours
	ticker := time.NewTicker(constants.Internal.EPGRefreshInterval)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			logger.Debug("{proxy/epg - StartEPGWarmup} Starting scheduled EPG refresh")

			// scheduled refresh
			sp.Cache.WarmUpEPG(sp.FetchAndMergeEPG)
			logger.Debug("{proxy/epg - StartEPGWarmup} Scheduled EPG refresh complete")
		}
	}()
}

// ChannelEPGMap returns a channel-name -> mapped epg_id map for every proxy
// channel that has a non-empty manual EPG mapping. Returns an empty map on error.
func ChannelEPGMap() map[string]string {
	m, err := db.GetAllChannelEPGMap()
	if err != nil {
		logger.Error("{proxy/epg - ChannelEPGMap} Failed to load channel EPG map: %v", err)
		return map[string]string{}
	}
	return m
}

// EPGIDForChannel resolves the XMLTV id to advertise for a proxy channel.
// Mapped channels advertise their raw mapped epg id, matching the original
// source entries in the exported guide. Channels without a mapping resolve
// to the dummy id.
func EPGIDForChannel(channelName string, mapped map[string]string) string {
	if id, ok := mapped[channelName]; ok && id != "" {
		return id
	}
	return DummyChannelID
}

// loadExportIDMap builds the reverse channel mapping: source epg_id -> the
// per-channel export ids of every proxy channel mapped to it. Returns an
// empty map if the mapping cannot be loaded, so callers fall back to
// exporting source entries unexpanded.
func loadExportIDMap() map[string][]string {
	chMap, err := db.GetAllChannelEPGMap()
	if err != nil {
		logger.Error("{proxy/epg - loadExportIDMap} Failed to load mapping, exporting unexpanded: %v", err)
		return map[string][]string{}
	}

	rev := make(map[string][]string, len(chMap))
	for channelName, id := range chMap {
		if id != "" {
			rev[id] = append(rev[id], utils.SanitizeChannelName(channelName))
		}
	}
	return rev
}

// expandMappedChannels expands channel elements so every mapped proxy channel
// gets its own guide entry. Source elements are duplicated once per proxy
// channel mapped to that EPG id, with the id rewritten to the per-channel
// export id so playlist tvg-ids always match.
func expandMappedChannels(channels []string, rev map[string][]string) []string {
	fChannels := make([]string, 0, len(channels))
	for _, ch := range channels {
		m := reEPGChannelID.FindStringSubmatch(ch)
		if m == nil {
			continue
		}
		// always keep the original source entry so the full guide exports
		fChannels = append(fChannels, ch)
		// additionally emit one copy per proxy channel mapped to this epg id
		for _, exportID := range rev[m[1]] {
			fChannels = append(fChannels, strings.Replace(ch, `id="`+m[1]+`"`, `id="`+exportID+`"`, 1))
		}
	}
	return fChannels
}

// generateDummyEPGEntry returns a static XMLTV channel and 24-hour programme
// element. This guarantees clients always receive a structurally valid, non-empty
// XMLTV document even when no EPG sources are configured or all sources fail,
// preventing players such as Tivimate from rejecting the guide response entirely.
func generateDummyEPGEntry() (string, string) {
	const (
		dummyChannelID = "kptv-proxy-dummy"
		xmltvTime      = "20060102150405 -0700"
	)

	now := time.Now().UTC()

	channel := "<channel id=\"kptv-proxy-dummy\">\n" +
		"  <display-name>KPTV Proxy Dummy Channel</display-name>\n" +
		"</channel>\n"

	programme := fmt.Sprintf(
		"<programme start=\"%s\" stop=\"%s\" channel=\"%s\">\n"+
			"  <title lang=\"en\">KPTV Proxy Dummy Channel</title>\n"+
			"  <desc lang=\"en\">KPTV Proxy is active. Add EPG sources to populate your guide.</desc>\n"+
			"</programme>\n",
		now.Format(xmltvTime),
		now.Add(24*time.Hour).Format(xmltvTime),
		dummyChannelID,
	)

	return channel, programme
}

// scanEPGFragments incrementally reads an XMLTV document from r, extracting
// complete <channel> and <programme> elements as they stream in and sending
// each fragment to its respective output channel. Only a small sliding window
// of the document is held in memory at any time, bounding peak usage by the
// largest single fragment rather than the full document size. Returns the
// channel count, programme count, and total bytes read.
func scanEPGFragments(r io.Reader, channelChan, programmeChan chan<- string) (int, int, int64, error) {
	var (
		buf            []byte
		tmp            = make([]byte, 64*1024)
		totalRead      int64
		channelCount   int
		programmeCount int
	)

	for {
		n, readErr := r.Read(tmp)
		if n > 0 {
			totalRead += int64(n)
			buf = append(buf, tmp[:n]...)

			// extract every complete fragment currently in the buffer
			for {
				chIdx := bytes.Index(buf, []byte("<channel "))
				prIdx := bytes.Index(buf, []byte("<programme "))

				// pick whichever fragment opener appears earliest
				start := -1
				var closeTag []byte
				isChannel := false
				if chIdx != -1 && (prIdx == -1 || chIdx < prIdx) {
					start, closeTag, isChannel = chIdx, []byte("</channel>"), true
				} else if prIdx != -1 {
					start, closeTag = prIdx, []byte("</programme>")
				}

				// no opener in the buffer; keep a small tail in case a tag
				// is split across the read boundary, discard the rest
				if start == -1 {
					if len(buf) > 16 {
						buf = buf[len(buf)-16:]
					}
					break
				}

				// opener found but the fragment isn't complete yet; drop
				// everything before it and read more
				end := bytes.Index(buf[start:], closeTag)
				if end == -1 {
					buf = buf[start:]
					break
				}
				end += start + len(closeTag)

				if isChannel {
					channelChan <- string(buf[start:end]) + "\n"
					channelCount++
				} else {
					programmeChan <- string(buf[start:end]) + "\n"
					programmeCount++
				}
				buf = buf[end:]
			}
		}
		if readErr == io.EOF {
			return channelCount, programmeCount, totalRead, nil
		}
		if readErr != nil {
			return channelCount, programmeCount, totalRead, readErr
		}
	}
}
