package schedulesdirect

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"kptv-proxy/work/logger"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	sdBaseURL        = "https://json.schedulesdirect.org/20141201"
	tokenCachePath   = "/settings/sd-tokens.json"
	tokenValidHours  = 24
	refreshThreshold = 12 * time.Hour
)

// tokenCacheEntry holds the cached token data for a single SD account.
type tokenCacheEntry struct {
	Username           string    `json:"username"`
	Token              string    `json:"token"`
	ObtainedAt         time.Time `json:"obtainedAt"`
	LastRefreshAttempt time.Time `json:"lastRefreshAttempt"`
}

// tokenCacheFile is the structure of /settings/sd-tokens.json.
type tokenCacheFile struct {
	Accounts []tokenCacheEntry `json:"accounts"`
}

// tokenCache provides thread-safe in-memory access to the token cache,
// backed by disk persistence.
var (
	tokenCacheMu sync.Mutex
)

// GetToken returns a valid SD API token for the given account, refreshing
// if necessary while respecting the 12-hour refresh throttle.
func GetToken(username, password string) (string, error) {
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()

	cache, err := loadTokenCache()
	if err != nil {
		logger.Warn("{schedulesdirect/auth - GetToken} Could not load token cache, will attempt fresh login: %v", err)
		cache = &tokenCacheFile{Accounts: []tokenCacheEntry{}}
	}

	entry := findEntry(cache, username)
	now := time.Now()

	// No cached entry — fresh login required
	if entry == nil {
		logger.Debug("{schedulesdirect/auth - GetToken} No cached token for %s, performing fresh login", username)
		return doLogin(cache, username, password, now)
	}

	tokenAge := now.Sub(entry.ObtainedAt)

	// Token still valid — return immediately
	if tokenAge < tokenValidHours*time.Hour {
		logger.Debug("{schedulesdirect/auth - GetToken} Cached token for %s is valid (%v old)", username, tokenAge.Round(time.Minute))
		return entry.Token, nil
	}

	// Token stale — check refresh throttle
	timeSinceLastAttempt := now.Sub(entry.LastRefreshAttempt)
	if timeSinceLastAttempt < refreshThreshold {
		logger.Warn("{schedulesdirect/auth - GetToken} Token for %s is stale but refresh throttled (last attempt %v ago), using stale token",
			username, timeSinceLastAttempt.Round(time.Minute))
		return entry.Token, nil
	}

	// Safe to refresh — write attempt timestamp to disk BEFORE the API call
	logger.Debug("{schedulesdirect/auth - GetToken} Token for %s is stale and refresh window elapsed, refreshing", username)
	entry.LastRefreshAttempt = now
	if err := saveTokenCache(cache); err != nil {
		logger.Warn("{schedulesdirect/auth - GetToken} Could not persist refresh attempt timestamp: %v", err)
	}

	return doLogin(cache, username, password, now)
}

// doLogin performs the actual SD authentication call and updates the cache.
func doLogin(cache *tokenCacheFile, username, password string, now time.Time) (string, error) {
	hashed := fmt.Sprintf("%x", sha1.Sum([]byte(password)))

	payload := map[string]string{
		"username": username,
		"password": hashed,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal login payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", sdBaseURL+"/token", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kptv-proxy/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("SD login request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code    int    `json:"code"`
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode login response: %w", err)
	}

	if result.Code != 0 || result.Token == "" {
		return "", fmt.Errorf("SD login failed (code %d): %s", result.Code, result.Message)
	}

	// Update or insert cache entry
	entry := findEntry(cache, username)
	if entry == nil {
		cache.Accounts = append(cache.Accounts, tokenCacheEntry{
			Username:           username,
			Token:              result.Token,
			ObtainedAt:         now,
			LastRefreshAttempt: now,
		})
	} else {
		entry.Token = result.Token
		entry.ObtainedAt = now
		entry.LastRefreshAttempt = now
	}

	if err := saveTokenCache(cache); err != nil {
		logger.Warn("{schedulesdirect/auth - doLogin} Could not persist new token: %v", err)
	}

	logger.Debug("{schedulesdirect/auth - doLogin} Successfully obtained new token for %s", username)
	return result.Token, nil
}

// findEntry returns a pointer to the cache entry for the given username, or nil.
func findEntry(cache *tokenCacheFile, username string) *tokenCacheEntry {
	for i := range cache.Accounts {
		if cache.Accounts[i].Username == username {
			return &cache.Accounts[i]
		}
	}
	return nil
}

// loadTokenCache reads and parses the token cache file from disk.
func loadTokenCache() (*tokenCacheFile, error) {
	if _, err := os.Stat(tokenCachePath); os.IsNotExist(err) {
		return &tokenCacheFile{Accounts: []tokenCacheEntry{}}, nil
	}

	data, err := os.ReadFile(tokenCachePath)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return &tokenCacheFile{Accounts: []tokenCacheEntry{}}, nil
	}

	var cache tokenCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}

	if cache.Accounts == nil {
		cache.Accounts = []tokenCacheEntry{}
	}

	return &cache, nil
}

// saveTokenCache writes the token cache to disk as formatted JSON.
func saveTokenCache(cache *tokenCacheFile) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tokenCachePath, data, 0644)
}
