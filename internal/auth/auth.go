package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/oaklight/autossh-tunnel/internal/config"
	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookieName = "autossh_jwt"
	TokenDuration     = 7 * 24 * time.Hour // Valid for 7 days across restarts
)

type Manager struct {
	cfgMgr  *config.Manager
	apiKeys []string
}

func NewManager(cfgMgr *config.Manager, apiKeyEnv string) *Manager {
	m := &Manager{
		cfgMgr: cfgMgr,
	}

	if apiKeyEnv != "" {
		keys := strings.Split(apiKeyEnv, ",")
		for _, k := range keys {
			k = strings.TrimSpace(k)
			if k != "" {
				m.apiKeys = append(m.apiKeys, k)
			}
		}
	}

	return m
}

func (m *Manager) getJWTSecret() []byte {
	if m.cfgMgr != nil {
		return m.cfgMgr.GetJWTSecret()
	}
	return []byte("default-fallback-jwt-secret-key-32")
}

// Authenticate verifies credentials from config.yaml and returns a signed JWT
func (m *Manager) Authenticate(username, password string) (string, bool) {
	if m.cfgMgr == nil {
		return "", false
	}

	storedUser, storedHash := m.cfgMgr.GetAdminCredentials()
	if username != storedUser {
		return "", false
	}

	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)); err != nil {
		return "", false
	}

	token, err := GenerateJWT(username, m.getJWTSecret(), TokenDuration)
	if err != nil {
		return "", false
	}
	return token, true
}

func (m *Manager) Logout(token string) {
	// JWT is stateless; client removes cookie/token.
}

func (m *Manager) ChangePassword(oldPassword, newPassword string) bool {
	if m.cfgMgr == nil {
		return false
	}

	_, storedHash := m.cfgMgr.GetAdminCredentials()
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(oldPassword)); err != nil {
		return false
	}

	// Update password in config.yaml and rotate JWT secret
	if err := m.cfgMgr.UpdateAdminPassword(newPassword); err != nil {
		return false
	}
	return true
}

// ValidateToken validates a JWT token or pre-configured API Key
func (m *Manager) ValidateToken(token string) bool {
	if token == "" {
		return false
	}

	// 1. Check API Key
	if m.ValidateAPIKey(token) {
		return true
	}

	// 2. Validate JWT signature and expiration
	secret := m.getJWTSecret()
	claims, err := ValidateJWT(token, secret)
	if err != nil || claims == nil {
		return false
	}

	if m.cfgMgr == nil {
		return false
	}
	storedUser, _ := m.cfgMgr.GetAdminCredentials()
	return claims.Subject == storedUser
}

func (m *Manager) ValidateAPIKey(apiKey string) bool {
	if len(m.apiKeys) == 0 {
		return false
	}
	apiKey = strings.TrimSpace(apiKey)
	for _, k := range m.apiKeys {
		if k == apiKey {
			return true
		}
	}
	return false
}

func (m *Manager) GetUsername() string {
	if m.cfgMgr != nil {
		user, _ := m.cfgMgr.GetAdminCredentials()
		return user
	}
	return "admin"
}

func (m *Manager) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Check Authorization Bearer header
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if m.ValidateToken(token) {
				next(w, r)
				return
			}
		}

		// 2. Check query parameter ?api_key= or ?token=
		if key := r.URL.Query().Get("api_key"); key != "" {
			if m.ValidateToken(key) {
				next(w, r)
				return
			}
		}
		if tok := r.URL.Query().Get("token"); tok != "" {
			if m.ValidateToken(tok) {
				next(w, r)
				return
			}
		}

		// 3. Check JWT Cookie (autossh_jwt or legacy autossh_session)
		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			if m.ValidateToken(cookie.Value) {
				next(w, r)
				return
			}
		}
		if cookie, err := r.Cookie("autossh_session"); err == nil {
			if m.ValidateToken(cookie.Value) {
				next(w, r)
				return
			}
		}

		// Not authenticated
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized. Please login or provide a valid JWT token."}`))
			return
		}

		// Redirect to login page for browser access
		http.Redirect(w, r, "/login?redirect="+r.URL.Path, http.StatusFound)
	}
}
