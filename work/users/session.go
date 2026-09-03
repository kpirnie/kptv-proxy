package users

import (
	"fmt"
	"kptv-proxy/work/constants"
	"sync"
	"time"
)

// Session holds the data for an authenticated session.
type Session struct {
	UserID    int64
	Username  string
	Name      string
	ExpiresAt time.Time
}

// sessionStore is the in-memory session store.
type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

var store = &sessionStore{
	sessions: make(map[string]*Session),
}

func init() {
	go store.cleanup()
}

// CreateSession generates a new session for a user and returns the session ID.
func CreateSession(userID int64, username, name string, rememberMe bool) (string, error) {
	id, err := generateSessionID()
	if err != nil {
		return "", err
	}

	ttl := constants.Internal.SessionTTL
	if rememberMe {
		ttl = constants.Internal.SessionTTLExtended
	}

	store.mu.Lock()
	store.sessions[id] = &Session{
		UserID:    userID,
		Username:  username,
		Name:      name,
		ExpiresAt: time.Now().Add(ttl),
	}
	store.mu.Unlock()

	return id, nil
}

// GetSession retrieves a session by ID, returning nil if not found or expired.
// An expired entry is evicted on read rather than waiting for the cleanup tick.
func GetSession(id string) *Session {
	store.mu.RLock()
	s, ok := store.sessions[id]
	store.mu.RUnlock()

	if !ok {
		return nil
	}

	if time.Now().After(s.ExpiresAt) {
		DeleteSession(id)
		return nil
	}
	return s
}

// DeleteSession removes a session by ID.
func DeleteSession(id string) {
	store.mu.Lock()
	delete(store.sessions, id)
	store.mu.Unlock()
}

// DeleteSessionsForUser revokes every outstanding session belonging to a user.
func DeleteSessionsForUser(userID int64) {
	store.mu.Lock()
	for id, s := range store.sessions {
		if s.UserID == userID {
			delete(store.sessions, id)
		}
	}
	store.mu.Unlock()
}

// cleanup periodically removes expired sessions.
func (s *sessionStore) cleanup() {
	ticker := time.NewTicker(constants.Internal.SessionCleanupTick)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, session := range s.sessions {
			if now.After(session.ExpiresAt) {
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
	}
}

// generateSessionID creates a cryptographically secure 64-character session ID.
func generateSessionID() (string, error) {
	s, err := randomAlnum(64)
	if err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}
	return s, nil
}
