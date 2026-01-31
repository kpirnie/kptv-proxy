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

type epgSource struct {
	url        string
	name       string
	sourceType string
}

func (sp *StreamProxy) GetEPGSources() []epgSource {
	var sources []epgSource

	for i := range sp.Config.Sources {
		src := &sp.Config.Sources[i]
		if src.Username != "" && src.Password != "" {
			sources = append(sources, epgSource{
				url:        fmt.Sprintf("%s/xmltv.php?username=%s&password=%s", src.URL, src.Username, src.Password),
				name:       src.Name,
				sourceType: "xc",
			})
		}
	}

	for i := range sp.Config.Sources {
		src := &sp.Config.Sources[i]
		if src.EPGURL != "" {
			sources = append(sources, epgSource{
				url:        src.EPGURL,
				name:       src.Name,
				sourceType: "m3u8",
			})
		}
	}

	for i := range sp.Config.EPGs {
		epg := &sp.Config.EPGs[i]
		sources = append(sources, epgSource{
			url:        epg.URL,
			name:       epg.Name,
			sourceType: "manual",
		})
	}

	return sources
}

func (sp *StreamProxy) FetchEPGData(sources []epgSource) ([]string, []string) {
	channelChan := make(chan string, len(sources)*100)
	programmeChan := make(chan string, len(sources)*1000)
	var wg sync.WaitGroup

	for _, epgSrc := range sources {
		wg.Add(1)
		go func(source epgSource) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", source.url, nil)
			if err != nil {
				logger.Error("[EPG] Failed to create request for %s: %v", source.name, err)
				return
			}

			resp, err := sp.HttpClient.Do(req)
			if err != nil {
				logger.Error("[EPG] Failed to fetch from %s: %v", source.name, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				logger.Error("[EPG] HTTP %d from %s", resp.StatusCode, source.name)
				return
			}

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				logger.Error("[EPG] Failed to read from %s: %v", source.name, err)
				return
			}

			docStr := string(data)

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
				channelStart = end
			}

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
				progStart = end
			}

			logger.Debug("[EPG] Successfully processed %s (%d bytes)", source.name, len(data))
		}(epgSrc)
	}

	go func() {
		wg.Wait()
		close(channelChan)
		close(programmeChan)
	}()

	var channels []string
	var programmes []string

	for channelData := range channelChan {
		channels = append(channels, channelData)
	}

	for programmeData := range programmeChan {
		programmes = append(programmes, programmeData)
	}

	return channels, programmes
}

func (sp *StreamProxy) FetchAndMergeEPG() string {
	sources := sp.GetEPGSources()
	if len(sources) == 0 {
		logger.Debug("[EPG] No EPG sources configured")
		return ""
	}

	channels, programmes := sp.FetchEPGData(sources)

	var result strings.Builder
	result.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	result.WriteString(`<tv generator-info-name="KPTV-Proxy">` + "\n")

	for _, ch := range channels {
		result.WriteString(ch)
	}

	for _, prog := range programmes {
		result.WriteString(prog)
	}

	result.WriteString("</tv>")

	logger.Debug("[EPG] Merged EPG complete (%d bytes)", result.Len())
	return result.String()
}

// StartEPGWarmup runs an initial EPG warmup and then refreshes every 12 hours.
func (sp *StreamProxy) StartEPGWarmup() {
	// Initial warmup
	sp.Cache.WarmUpEPG(func() string {
		return sp.FetchAndMergeEPG()
	})

	ticker := time.NewTicker(12 * time.Hour)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			sp.Cache.WarmUpEPG(func() string {
				return sp.FetchAndMergeEPG()
			})
		}
	}()
}
