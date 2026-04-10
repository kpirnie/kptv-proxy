package users

import (
	"github.com/gorilla/mux"
)

// SetupUserRoutes registers all authentication and user management routes.
func SetupUserRoutes(router *mux.Router) {
	// Public routes — no auth required
	router.HandleFunc("/login", HandleLoginPage).Methods("GET")
	router.HandleFunc("/login", HandleLogin).Methods("POST")
	router.HandleFunc("/register", HandleRegisterPage).Methods("GET")
	router.HandleFunc("/register", HandleRegister).Methods("POST")
	router.HandleFunc("/logout", HandleLogout).Methods("GET", "POST")

	// Auth check endpoint
	router.HandleFunc("/api/auth/me", RequireAuth(HandleMe)).Methods("GET")

	// Password management — session only
	router.HandleFunc("/api/auth/password", RequireAuth(HandleChangePassword)).Methods("POST")

	// Permission constants — used by frontend to build token UI
	router.HandleFunc("/api/auth/permissions", RequireAuth(HandleGetPermissions)).Methods("GET")

	// Token management — session only
	router.HandleFunc("/api/auth/tokens", RequireAuth(HandleGetTokens)).Methods("GET")
	router.HandleFunc("/api/auth/tokens", RequireAuth(HandleCreateToken)).Methods("POST")
	router.HandleFunc("/api/auth/tokens", RequireAuth(HandleDeleteToken)).Methods("DELETE")
}
