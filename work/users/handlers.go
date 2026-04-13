package users

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// limits registration attempts to prevent hammering during the setup window
var registerLimiter = struct {
	mu       sync.Mutex
	attempts int
	resetAt  time.Time
}{resetAt: time.Now().Add(time.Hour)}

// HandleRegisterPage serves the registration form.
func HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	count, err := UserCount()
	if err != nil || count > 0 {
		// Already logged in
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if GetSession(cookie.Value) != nil {
				http.Redirect(w, r, "/?notice=already_logged_in", http.StatusFound)
				return
			}
		}
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	http.ServeFile(w, r, "/static/register.html")
}

// HandleRegister processes the registration form submission.
func HandleRegister(w http.ResponseWriter, r *http.Request) {
	registerLimiter.mu.Lock()
	if time.Now().After(registerLimiter.resetAt) {
		registerLimiter.attempts = 0
		registerLimiter.resetAt = time.Now().Add(time.Hour)
	}
	registerLimiter.attempts++
	attempts := registerLimiter.attempts
	registerLimiter.mu.Unlock()

	if attempts > 5 {
		http.Error(w, "Too many registration attempts", http.StatusTooManyRequests)
		return
	}

	count, err := UserCount()
	if err != nil || count > 0 {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if name == "" || email == "" || username == "" || password == "" {
		http.Redirect(w, r, "/register?error=All+fields+are+required", http.StatusFound)
		return
	}

	if len(password) < 8 {
		http.Redirect(w, r, "/register?error=Password+must+be+at+least+8+characters", http.StatusFound)
		return
	}

	hash, err := HashPassword(password)
	if err != nil {
		http.Redirect(w, r, "/register?error=Failed+to+hash+password", http.StatusFound)
		return
	}

	if _, err := CreateUser(name, email, username, hash); err != nil {
		http.Redirect(w, r, "/register?error=Username+or+email+already+exists", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/login", http.StatusFound)
}

// HandleLoginPage serves the login form.
func HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	count, err := UserCount()
	if err == nil && count == 0 {
		http.Redirect(w, r, "/register", http.StatusFound)
		return
	}
	// Already logged in
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if GetSession(cookie.Value) != nil {
			http.Redirect(w, r, "/?notice=already_logged_in", http.StatusFound)
			return
		}
	}
	http.ServeFile(w, r, "/static/login.html")
}

// HandleLogin processes the login form submission.
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	identifier := strings.TrimSpace(r.FormValue("identifier"))
	password := r.FormValue("password")
	rememberMe := r.FormValue("remember_me") == "on"

	if identifier == "" || password == "" {
		http.Redirect(w, r, "/login?error=All+fields+are+required", http.StatusFound)
		return
	}

	// Try username first, then email
	user, err := GetUserByUsername(identifier)
	if err != nil {
		user, err = GetUserByEmail(identifier)
		if err != nil {
			http.Redirect(w, r, "/login?error=Invalid+credentials", http.StatusFound)
			return
		}
	}

	match, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !match {
		http.Redirect(w, r, "/login?error=Invalid+credentials", http.StatusFound)
		return
	}

	sessionID, err := CreateSession(user.ID, user.Username, user.Name, rememberMe)
	if err != nil {
		http.Redirect(w, r, "/login?error=Failed+to+create+session", http.StatusFound)
		return
	}

	UpdateLastLogin(user.ID)

	maxAge := 0
	if rememberMe {
		maxAge = int(sessionTTLExtended.Seconds())
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})

	http.Redirect(w, r, "/", http.StatusFound)
}

// HandleLogout clears the session and redirects to login.
func HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		DeleteSession(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusFound)
}

// HandleChangePassword processes a password change request.
func HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	session := GetSession(cookie.Value)
	if session == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if len(req.NewPassword) < 8 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Password must be at least 8 characters"})
		return
	}

	user, err := GetUserByUsername(session.Username)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	match, err := VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !match {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Current password is incorrect"})
		return
	}

	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := UpdatePassword(user.ID, hash); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// HandleGetTokens returns all API tokens (hashes excluded).
func HandleGetTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	tokens, err := GetAllTokens()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	type safeToken struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Permissions int    `json:"permissions"`
	}

	safe := make([]safeToken, len(tokens))
	for i, t := range tokens {
		safe[i] = safeToken{ID: t.ID, Name: t.Name, Permissions: t.Permissions}
	}

	json.NewEncoder(w).Encode(safe)
}

// HandleCreateToken generates a new API token and returns the raw value once.
func HandleCreateToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Name        string `json:"name"`
		Permissions int    `json:"permissions"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Name is required"})
		return
	}

	rawToken, err := GenerateToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	hash, err := HashPassword(rawToken)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	id, err := CreateToken(req.Name, hash, req.Permissions)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Return the raw token exactly once — it cannot be retrieved again
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":    id,
		"name":  req.Name,
		"token": rawToken,
	})
}

// HandleDeleteToken removes an API token by ID.
func HandleDeleteToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		ID int64 `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if err := DeleteToken(req.ID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// HandleMe returns basic info about the current authenticated session.
func HandleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	session := GetSession(cookie.Value)
	if session == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"username": session.Username,
		"name":     session.Name,
	})
}

// HandleGetPermissions returns the permission constants for the frontend.
func HandleGetPermissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{
		"read":        PermRead,
		"configWrite": PermConfigWrite,
		"restart":     PermRestart,
		"streams":     PermStreams,
		"logs":        PermLogs,
		"xcAccounts":  PermXCAccounts,
		"epgs":        PermEPGs,
		"sd":          PermSD,
		"all":         PermAll,
	})
}

// getTokenID is a helper to parse token ID from URL path.
func getTokenID(r *http.Request) (int64, error) {
	parts := strings.Split(r.URL.Path, "/")
	return strconv.ParseInt(parts[len(parts)-1], 10, 64)
}
