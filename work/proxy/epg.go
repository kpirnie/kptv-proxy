package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (sp *StreamProxy) RefreshEPGCache() {
	//sp.Logger.Debug("[EPG_REFRESH] Starting background EPG refresh")

	var epgSources []struct {
		url        string
		name       string
		sourceType string
	}

	for i := range sp.Config.Sources {
		src := &sp.Config.Sources[i]
		if src.Username != "" && src.Password != "" {
			epgSources = append(epgSources, struct {
				url        string
				name       string
				sourceType string
			}{
				url:        fmt.Sprintf("%s/xmltv.php?username=%s&password=%s", src.URL, src.Username, src.Password),
				name:       src.Name,
				sourceType: "xc",
			})
		}
	}

	for i := range sp.Config.Sources {
		src := &sp.Config.Sources[i]
		if src.EPGURL != "" {
			epgSources = append(epgSources, struct {
				url        string
				name       string
				sourceType string
			}{
				url:        src.EPGURL,
				name:       src.Name,
				sourceType: "m3u8",
			})
		}
	}

	for i := range sp.Config.EPGs {
		epg := &sp.Config.EPGs[i]
		epgSources = append(epgSources, struct {
			url        string
			name       string
			sourceType string
		}{
			url:        epg.URL,
			name:       epg.Name,
			sourceType: "manual",
		})
	}

	if len(epgSources) == 0 {
		return
	}

	var result strings.Builder
	result.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	result.WriteString(`<tv generator-info-name="KPTV-Proxy">` + "\n")

	type epgData struct {
		channels   []string
		programmes []string
	}

	dataChan := make(chan epgData, len(epgSources))
	var wg sync.WaitGroup

	for _, epgSrc := range epgSources {
		wg.Add(1)
		go func(source struct {
			url        string
			name       string
			sourceType string
		}) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", source.url, nil)
			if err != nil {
				return
			}

			resp, err := sp.HttpClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return
			}

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return
			}

			docStr := string(data)
			var channels []string
			var programmes []string

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
				channels = append(channels, docStr[start:end]+"\n")
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
				programmes = append(programmes, docStr[start:end]+"\n")
				progStart = end
			}

			dataChan <- epgData{channels: channels, programmes: programmes}
		}(epgSrc)
	}

	go func() {
		wg.Wait()
		close(dataChan)
	}()

	for data := range dataChan {
		for _, ch := range data.channels {
			result.WriteString(ch)
		}
	}

	for data := range dataChan {
		for _, prog := range data.programmes {
			result.WriteString(prog)
		}
	}

	result.WriteString("</tv>")

	sp.EPGCache.SetEPG([]byte(result.String()))
	//sp.Logger.Debug("[EPG_REFRESH] Background refresh complete (%d bytes)", result.Len())
}
