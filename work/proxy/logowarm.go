// work/proxy/logowarm.go
package proxy

import (
	"kptv-proxy/work/logos"
	"kptv-proxy/work/types"
)

// warmChannelLogos pre-fetches the resolved logo for every live channel so the
// first client request is served from disk rather than the provider CDN. Only
// the source URL matters here, so the resolver needs no serving prefix.
func (sp *StreamProxy) warmChannelLogos() {
	resolver := logos.NewResolver("", ChannelEPGMap())

	seen := make(map[string]struct{})
	urls := make([]string, 0)

	sp.Channels.Range(func(name string, channel *types.Channel) bool {
		channel.Mu.RLock()

		if len(channel.Streams) > 0 && streamContentType(channel.Streams[0]) == "live" {
			if url := resolver.SourceFor(name, channel.Streams[0].Attributes["tvg-logo"]); url != "" {
				if _, dup := seen[url]; !dup {
					seen[url] = struct{}{}
					urls = append(urls, url)
				}
			}
		}

		channel.Mu.RUnlock()
		return true
	})

	logos.Warm(urls)
}
