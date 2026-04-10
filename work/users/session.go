package users

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

const (
	sessionTTL         = 24 * time.Hour
	sessionTTLExtended = 30 * 24 * time.Hour
	sessionCleanupTick = 15 * time.Minute
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

	ttl := sessionTTL
	if rememberMe {
		ttl = sessionTTLExtended
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
func GetSession(id string) *Session {
	store.mu.RLock()
	s, ok := store.sessions[id]
	store.mu.RUnlock()

	if !ok || time.Now().After(s.ExpiresAt) {
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

// cleanup periodically removes expired sessions.
func (s *sessionStore) cleanup() {
	ticker := time.NewTicker(sessionCleanupTick)
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
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	bytes := make([]byte, 64)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}
	for i, b := range bytes {
		bytes[i] = chars[b%byte(len(chars))]
	}
	return string(bytes), nil
}
