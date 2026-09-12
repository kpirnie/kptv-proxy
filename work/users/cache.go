package users

import (
	"kptv-proxy/work/constants"
	"sync"
	"time"
)

// cachedSession is a successful session lookup held for a short TTL so repeated
// authenticated requests do not each read the database.
type cachedSession struct {
	session   *Session
	expiresAt time.Time
}

// cachedToken is a successful token lookup held for a short TTL.
type cachedToken struct {
	permissions int
	expiresAt   time.Time
}

// Only successful lookups are cached. Caching misses would let unauthenticated
// callers grow these maps with arbitrary keys.
var (
	sessionCacheMu sync.RWMutex
	sessionCache   = make(map[string]cachedSession)

	tokenCacheMu sync.RWMutex
	tokenCache   = make(map[string]cachedToken)
)

// lookupSessionCache returns the cached session for a hashed id, if the entry
// exists and has not passed its TTL.
func lookupSessionCache(idHash string) (*Session, bool) {
	sessionCacheMu.RLock()
	entry, ok := sessionCache[idHash]
	sessionCacheMu.RUnlock()

	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.session, true
}

// storeSessionCache caches a session under its hashed id. The entry never
// outlives the session itself.
func storeSessionCache(idHash string, session *Session) {
	expires := time.Now().Add(constants.Internal.AuthCacheTTL)
	if session.ExpiresAt.Before(expires) {
		expires = session.ExpiresAt
	}

	sessionCacheMu.Lock()
	sessionCache[idHash] = cachedSession{session: session, expiresAt: expires}
	sessionCacheMu.Unlock()
}

// dropSessionCache removes a single cached session by hashed id.
func dropSessionCache(idHash string) {
	sessionCacheMu.Lock()
	delete(sessionCache, idHash)
	sessionCacheMu.Unlock()
}

// dropUserSessionCache removes every cached session belonging to a user, so a
// revoked login cannot be served from cache.
func dropUserSessionCache(userID int64) {
	sessionCacheMu.Lock()
	for hash, entry := range sessionCache {
		if entry.session != nil && entry.session.UserID == userID {
			delete(sessionCache, hash)
		}
	}
	sessionCacheMu.Unlock()
}

// lookupTokenCache returns the cached permission bitmask for a token hash, if
// the entry exists and has not passed its TTL.
func lookupTokenCache(hash string) (int, bool) {
	tokenCacheMu.RLock()
	entry, ok := tokenCache[hash]
	tokenCacheMu.RUnlock()

	if !ok || time.Now().After(entry.expiresAt) {
		return 0, false
	}
	return entry.permissions, true
}

// storeTokenCache caches a token's permission bitmask under its hash.
func storeTokenCache(hash string, permissions int) {
	tokenCacheMu.Lock()
	tokenCache[hash] = cachedToken{
		permissions: permissions,
		expiresAt:   time.Now().Add(constants.Internal.AuthCacheTTL),
	}
	tokenCacheMu.Unlock()
}

// FlushTokenCache clears every cached token. Called whenever the token table
// changes, since tokens are revoked by id rather than by hash.
func FlushTokenCache() {
	tokenCacheMu.Lock()
	tokenCache = make(map[string]cachedToken)
	tokenCacheMu.Unlock()
}

// pruneAuthCaches drops entries that have passed their TTL.
func pruneAuthCaches() {
	now := time.Now()

	sessionCacheMu.Lock()
	for hash, entry := range sessionCache {
		if now.After(entry.expiresAt) {
			delete(sessionCache, hash)
		}
	}
	sessionCacheMu.Unlock()

	tokenCacheMu.Lock()
	for hash, entry := range tokenCache {
		if now.After(entry.expiresAt) {
			delete(tokenCache, hash)
		}
	}
	tokenCacheMu.Unlock()
}
