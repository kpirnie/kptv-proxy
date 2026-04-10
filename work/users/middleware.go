package users

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

const sessionCookieName = "kptv_session"

// RequireAuth wraps a handler, allowing access only to authenticated sessions
// or valid API tokens. Browser requests are redirected to /login, API requests
// get a 401 JSON response.
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isAuthenticated(r) {
			next(w, r)
			return
		}

		// API requests get a JSON 401
		if isAPIRequest(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "unauthorized",
			})
			return
		}

		// Browser requests get redirected to login
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// RequireAuthWithPerm wraps a handler requiring both authentication and a
// specific permission. Only applies permission checks to token-based auth —
// session-based (admin) auth always has full access.
func RequireAuthWithPerm(perm int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check session first — full access
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if GetSession(cookie.Value) != nil {
				next(w, r)
				return
			}
		}

		// Check API token with permission
		token := extractBearerToken(r)
		if token != "" {
			if checkTokenPermission(token, perm) {
				next(w, r)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "unauthorized",
		})
	}
}

// isAuthenticated checks for a valid session cookie or bearer token.
func isAuthenticated(r *http.Request) bool {
	// Check session cookie
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if GetSession(cookie.Value) != nil {
			return true
		}
	}

	// Check bearer token
	token := extractBearerToken(r)
	if token == "" {
		return false
	}

	return checkTokenPermission(token, PermRead)
}

// checkTokenPermission verifies a raw token string has the given permission.
func checkTokenPermission(rawToken string, perm int) bool {
	// We need to find the token by iterating and verifying — tokens are hashed
	tokens, err := GetAllTokens()
	if err != nil {
		return false
	}

	for _, t := range tokens {
		match, err := VerifyPassword(rawToken, t.TokenHash)
		if err != nil || !match {
			continue
		}
		return HasPermission(t.Permissions, perm)
	}

	return false
}

// extractBearerToken pulls the token from the Authorization header.
func extractBearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}

// isAPIRequest returns true if the request is likely from an API client
// rather than a browser.
func isAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/") ||
		r.Header.Get("Accept") == "application/json" ||
		r.Header.Get("Content-Type") == "application/json"
}

// RequireLocalNetwork blocks requests from non-RFC1918 addresses.
func RequireLocalNetwork(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := realIP(r)
		if !isPrivateIP(ip) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// realIP extracts the real IP from X-Forwarded-For or RemoteAddr.
func realIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// X-Forwarded-For can be a comma-separated list — take the first
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[0])
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// isPrivateIP returns true if the IP is RFC1918 or localhost.
func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
	}

	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
