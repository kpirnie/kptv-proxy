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

// For resolves the logo for a channel in priority order: manual override,
// uploaded logo, the mapped EPG icon, the provider tvg-logo, then the
// configured default. Everything but the default is served back through the
// proxy, so provider URL rotation and origin leakage stop mattering.
func (r *Resolver) For(channelName, providerLogo string) string {
	if l, ok := r.assigned[channelName]; ok && l.Value != "" {
		switch l.Kind {
		case "upload":
			return r.serve(l.Value)
		case "override":
			return r.serve(Register(l.Value))
		case "epg":
			if icon := r.epgIcon(channelName); icon != "" {
				return r.serve(Register(icon))
			}
			return r.serve(Register(l.Value))
		}
	}

	if icon := r.epgIcon(channelName); icon != "" {
		return r.serve(Register(icon))
	}

	if providerLogo != "" {
		return r.serve(Register(providerLogo))
	}

	return constants.Internal.LogoDefaultURL
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
