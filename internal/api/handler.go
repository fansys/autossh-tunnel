package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oaklight/autossh-tunnel/internal/auth"
	"github.com/oaklight/autossh-tunnel/internal/config"
	"github.com/oaklight/autossh-tunnel/internal/monitor"
	"github.com/oaklight/autossh-tunnel/internal/sshtunnel"
	"github.com/oaklight/autossh-tunnel/internal/terminal"
)

type Handler struct {
	authMgr      *auth.Manager
	cfgMgr       *config.Manager
	tunnelMgr    *sshtunnel.Manager
	termMgr      *terminal.SessionManager
	staticDir    string
	sshConfigDir string
	version      string
}

func NewHandler(
	authMgr *auth.Manager,
	cfgMgr *config.Manager,
	tunnelMgr *sshtunnel.Manager,
	termMgr *terminal.SessionManager,
	staticDir string,
	sshConfigDir string,
	version string,
) *Handler {
	return &Handler{
		authMgr:      authMgr,
		cfgMgr:       cfgMgr,
		tunnelMgr:    tunnelMgr,
		termMgr:      termMgr,
		staticDir:    staticDir,
		sshConfigDir: sshConfigDir,
		version:      version,
	}
}

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

// ----------------- Auth Handlers -----------------

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	token, ok := h.authMgr.Authenticate(req.Username, req.Password)
	if !ok {
		errorResponse(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(auth.TokenDuration),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"token":    token,
		"username": req.Username,
	})
}

func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		h.authMgr.Logout(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
	})

	jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.NewPassword) < 6 {
		errorResponse(w, http.StatusBadRequest, "New password must be at least 6 characters")
		return
	}

	if !h.authMgr.ChangePassword(req.OldPassword, req.NewPassword) {
		errorResponse(w, http.StatusBadRequest, "Incorrect old password")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
}

func (h *Handler) HandleAuthMe(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]string{
		"username": h.authMgr.GetUsername(),
	})
}

// ----------------- Tunnel Handlers -----------------

func (h *Handler) HandleListTunnels(w http.ResponseWriter, r *http.Request) {
	runtimes := h.tunnelMgr.GetAllRuntimes()
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"tunnels": runtimes,
		"total":   len(runtimes),
	})
}

func (h *Handler) HandleGetTunnel(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
	rt := h.tunnelMgr.GetRuntime(hash)
	if rt == nil {
		errorResponse(w, http.StatusNotFound, "Tunnel not found")
		return
	}
	jsonResponse(w, http.StatusOK, rt)
}

func (h *Handler) HandleSaveTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var t config.TunnelConfig
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}

	if t.RemoteHost == "" {
		errorResponse(w, http.StatusBadRequest, "remote_host is required")
		return
	}

	if err := h.cfgMgr.ProcessAndSaveTunnel(&t); err != nil {
		errorResponse(w, http.StatusInternalServerError, "Failed to save configuration: "+err.Error())
		return
	}

	// Trigger sync and start if enabled
	h.tunnelMgr.SyncWithConfig()

	hash := t.CalculateHash()
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"hash":    hash,
		"tunnel":  h.tunnelMgr.GetRuntime(hash),
	})
}

func (h *Handler) HandleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	hash := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
	_ = h.tunnelMgr.StopTunnel(hash)

	deleted, err := h.cfgMgr.DeleteTunnel(hash)
	if err != nil {
		errorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	h.tunnelMgr.SyncWithConfig()
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"deleted": deleted,
	})
}

func (h *Handler) HandleControlTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		errorResponse(w, http.StatusBadRequest, "Invalid control URL")
		return
	}
	hash := parts[2]
	action := parts[3]

	var err error
	switch action {
	case "start":
		_ = h.cfgMgr.SetTunnelEnabled(hash, true)
		err = h.tunnelMgr.StartTunnel(hash)
	case "stop":
		_ = h.cfgMgr.SetTunnelEnabled(hash, false)
		err = h.tunnelMgr.StopTunnel(hash)
	case "restart":
		_ = h.cfgMgr.SetTunnelEnabled(hash, true)
		err = h.tunnelMgr.RestartTunnel(hash)
	default:
		errorResponse(w, http.StatusBadRequest, "Unknown action: "+action)
		return
	}

	if err != nil {
		errorResponse(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"tunnel":  h.tunnelMgr.GetRuntime(hash),
	})
}

func (h *Handler) HandleBatchControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	action := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
	switch action {
	case "start-all":
		_ = h.cfgMgr.SetAllTunnelsEnabled(true)
		h.tunnelMgr.StartAll()
	case "stop-all":
		_ = h.cfgMgr.SetAllTunnelsEnabled(false)
		h.tunnelMgr.StopAll()
	default:
		errorResponse(w, http.StatusBadRequest, "Unknown batch action")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]bool{"success": true})
}

func (h *Handler) HandleGetLogs(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		errorResponse(w, http.StatusBadRequest, "Invalid log URL")
		return
	}
	hash := parts[2]

	tail := 100
	if tailParam := r.URL.Query().Get("lines"); tailParam != "" {
		if t, err := strconv.Atoi(tailParam); err == nil && t > 0 {
			tail = t
		}
	}

	logs, err := h.tunnelMgr.GetLogs(hash, tail)
	if err != nil {
		errorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"hash": hash,
		"logs": logs,
	})
}

// ----------------- Test Connection Handler -----------------

func (h *Handler) HandleTestConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var t config.TunnelConfig
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		errorResponse(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}

	// If a new private key content was submitted in the test request, temporarily write it to a test key file
	var tempKeyFile string
	if strings.TrimSpace(t.PrivateKeyContent) != "" {
		tempF, err := os.CreateTemp("", "test_key_*")
		if err == nil {
			_ = os.Chmod(tempF.Name(), 0600)
			_, _ = tempF.WriteString(strings.TrimSpace(t.PrivateKeyContent) + "\n")
			_ = tempF.Close()
			tempKeyFile = tempF.Name()
			t.IdentityFile = tempKeyFile
			defer os.Remove(tempKeyFile)
		}
	} else if t.Hash != "" {
		if existing := h.cfgMgr.GetTunnelInternal(t.Hash); existing != nil {
			if t.IdentityFile == "" && existing.IdentityFile != "" {
				t.IdentityFile = existing.IdentityFile
			}
			if t.Password == "" && existing.Password != "" {
				t.Password = existing.Password
			}
			if t.PasswordEnv == "" && existing.PasswordEnv != "" {
				t.PasswordEnv = existing.PasswordEnv
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	start := time.Now()
	client, err := sshtunnel.DialSSH(ctx, &t, h.sshConfigDir)
	elapsed := time.Since(start).Milliseconds()

	if err == nil {
		_ = client.Close()
		jsonResponse(w, http.StatusOK, monitor.DiagnosticResult{
			ErrorCode: "OK",
			Title:     "连接成功 (Connection Successful)",
			Success:   true,
			LatencyMs: elapsed,
		})
		return
	}

	diag := monitor.DiagnoseSSHError(err.Error())
	diag.LatencyMs = elapsed
	jsonResponse(w, http.StatusOK, diag)
}

// ----------------- Raw YAML Config Handlers -----------------

func (h *Handler) HandleConfigYAML(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		yamlStr, err := h.cfgMgr.GetRawYAML()
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"yaml": yamlStr})

	case http.MethodPost:
		var req struct {
			YAML string `json:"yaml"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			errorResponse(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}

		if err := h.cfgMgr.SaveRawYAML(req.YAML); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}

		h.tunnelMgr.SyncWithConfig()
		jsonResponse(w, http.StatusOK, map[string]bool{"success": true})

	default:
		errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// ----------------- Languages & System Handlers -----------------

func (h *Handler) HandleGetLanguages(w http.ResponseWriter, r *http.Request) {
	languages := []map[string]string{
		{"code": "zh", "name": "简体中文"},
		{"code": "en", "name": "English"},
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"languages": languages})
}

func (h *Handler) HandleSystemStatus(w http.ResponseWriter, r *http.Request) {
	runtimes := h.tunnelMgr.GetAllRuntimes()
	var active, degraded, failed, stopped int
	for _, rt := range runtimes {
		switch rt.Status {
		case sshtunnel.StatusActive:
			active++
		case sshtunnel.StatusDegraded:
			degraded++
		case sshtunnel.StatusFailed, sshtunnel.StatusRetrying:
			failed++
		default:
			stopped++
		}
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"version": h.version,
		"stats": map[string]int{
			"total":    len(runtimes),
			"active":   active,
			"degraded": degraded,
			"failed":   failed,
			"stopped":  stopped,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func (h *Handler) HandleTerminalWS(w http.ResponseWriter, r *http.Request) {
	h.termMgr.HandleTerminalWS(w, r)
}
