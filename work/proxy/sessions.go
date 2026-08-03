// work/proxy/sessions.go
package proxy

import (
	"sync/atomic"
	"time"

	"github.com/puzpuzpuz/xsync/v3"
)

// FileSession describes one in-flight passthrough delivery. Series episodes and
// local media are served straight to the client without a restreamer, so they
// have no channel entry to report from and are tracked here instead.
type FileSession struct {
	ChannelName string
	SourceName  string
	LogoURL     string
	StartedAt   int64
	Bytes       atomic.Int64
}

var fileSessions = xsync.NewMapOf[string, *FileSession]()

// StartFileSession records a passthrough delivery and returns its handle. The
// caller must pass the same id to EndFileSession when the response completes.
func StartFileSession(id, channelName, sourceName, logoURL string) *FileSession {
	session := &FileSession{
		ChannelName: channelName,
		SourceName:  sourceName,
		LogoURL:     logoURL,
		StartedAt:   time.Now().Unix(),
	}
	fileSessions.Store(id, session)
	return session
}

// EndFileSession drops a completed passthrough delivery.
func EndFileSession(id string) {
	fileSessions.Delete(id)
}

// RangeFileSessions walks every in-flight passthrough delivery.
func RangeFileSessions(visit func(*FileSession) bool) {
	fileSessions.Range(func(_ string, session *FileSession) bool {
		return visit(session)
	})
}
