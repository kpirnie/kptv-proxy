// work/logos/resolve.go
package logos

import (
	"fmt"
	"kptv-proxy/work/constants"
	"kptv-proxy/work/db"
	"kptv-proxy/work/epgindex"
	"kptv-proxy/work/logger"
	"strings"
)

// Resolver answers the logo URL to advertise for a proxy channel. One is built
// per export pass so the assignment table is read once rather than per channel.
type Resolver struct {
	prefix   string
	assigned map[string]db.ChannelLogo
	epgMap   map[string]string
}

// NewResolver loads the stored logo assignments and returns a resolver that
// emits URLs under the given prefix, which already carries whatever credentials
// the consuming route requires.
func NewResolver(prefix string, epgMap map[string]string) *Resolver {
	assigned, err := db.GetAllChannelLogos()
	if err != nil {
		assigned = map[string]db.ChannelLogo{}
	}

	return &Resolver{
		prefix:   strings.TrimSuffix(prefix, "/"),
		assigned: assigned,
		epgMap:   epgMap,
	}
}

// For resolves the logo URL to advertise for a channel. Everything but the
// configured default is served back through the proxy, so provider URL rotation
// and origin leakage stop mattering.
func (r *Resolver) For(channelName, providerLogo string) string {
	kind, value := r.resolve(channelName, providerLogo)

	switch kind {
	case "upload":
		return r.serve(value)
	case "remote":
		return r.serve(Register(value))
	}

	return constants.Internal.LogoDefaultURL
}

// SourceFor returns the remote URL behind a channel's resolved logo, or an
// empty string when it resolves to an upload or the configured default. The
// warm pass uses it to pre-fetch without rendering an export.
func (r *Resolver) SourceFor(channelName, providerLogo string) string {
	kind, value := r.resolve(channelName, providerLogo)
	if kind == "remote" {
		return value
	}
	return ""
}

// resolve walks the priority order — manual override, uploaded logo, mapped EPG
// icon, provider tvg-logo, configured default — and reports what a channel
// resolves to.
func (r *Resolver) resolve(channelName, providerLogo string) (string, string) {
	if l, ok := r.assigned[channelName]; ok && l.Value != "" {
		switch l.Kind {
		case "upload":
			return "upload", l.Value
		case "override":
			return "remote", l.Value
		case "epg":
			if icon := r.epgIcon(channelName); icon != "" {
				return "remote", icon
			}
			return "remote", l.Value
		}
	}

	if icon := r.epgIcon(channelName); icon != "" {
		return "remote", icon
	}

	if providerLogo != "" {
		return "remote", providerLogo
	}

	return "default", ""
}

// epgIcon returns the icon advertised by the EPG channel mapped to a proxy
// channel, or an empty string when the channel is unmapped or has no icon.
func (r *Resolver) epgIcon(channelName string) string {
	id, ok := r.epgMap[channelName]
	if !ok || id == "" {
		return ""
	}
	return epgindex.IconFor(id)
}

// serve builds the proxied URL for a stored logo hash.
func (r *Resolver) serve(hash string) string {
	if hash == "" {
		logger.Debug("{logos/resolve - serve} empty hash, falling back to default")
		return constants.Internal.LogoDefaultURL
	}
	return fmt.Sprintf("%s/%s", r.prefix, hash)
}
