package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oaklight/autossh-tunnel/internal/api"
	"github.com/oaklight/autossh-tunnel/internal/auth"
	"github.com/oaklight/autossh-tunnel/internal/config"
	"github.com/oaklight/autossh-tunnel/internal/sshtunnel"
	"github.com/oaklight/autossh-tunnel/internal/terminal"
)

//go:embed web/static/* web/templates/*
var embeddedFiles embed.FS

var version = "latest"

func printBanner(port int) {
	line1 := "AutoSSH Tunnel Manager"
	line2 := fmt.Sprintf("Web Console: http://0.0.0.0:%d", port)
	width := len(line1)
	if len(line2) > width {
		width = len(line2)
	}
	border := strings.Repeat("═", width+6)
	fmt.Printf("  ╔%s╗\n", border)
	fmt.Printf("  ║   %-*s   ║\n", width, line1)
	fmt.Printf("  ║   %-*s   ║\n", width, line2)
	fmt.Printf("  ╚%s╝\n", border)
}

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)

	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		if val, err := strconv.Atoi(p); err == nil && val > 0 {
			port = val
		}
	} else if p := os.Getenv("API_PORT"); p != "" {
		if val, err := strconv.Atoi(p); err == nil && val > 0 {
			port = val
		}
	}

	configPath := os.Getenv("AUTOSSH_CONFIG_FILE")
	if configPath == "" {
		configPath = "/etc/autossh/config.yaml"
		// Fallback for local development or single binary
		if _, err := os.Stat("/etc/autossh"); os.IsNotExist(err) {
			configPath = "./config/config.yaml"
		}
	}

	configDir := filepath.Dir(configPath)
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = filepath.Join(configDir, "logs")
	}
	sshConfigDir := os.Getenv("SSH_CONFIG_DIR")
	if sshConfigDir == "" {
		if _, err := os.Stat("/root/.ssh"); err == nil {
			sshConfigDir = "/root/.ssh"
		} else {
			sshConfigDir = filepath.Join(configDir, "keys")
		}
	}
	apiKeyEnv := os.Getenv("API_KEY")

	// Ensure directories exist
	_ = os.MkdirAll(configDir, 0755)
	_ = os.MkdirAll(logDir, 0755)

	printBanner(port)

	// 1. Initialize Config Manager (loads or auto-creates config.yaml with persisted jwt_secret & admin credentials)
	cfgMgr, err := config.NewManager(configPath)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize configuration: %v", err)
	}

	// 2. Initialize Auth Manager (Admin credentials & JWT secret persisted inside config.yaml)
	authMgr := auth.NewManager(cfgMgr, apiKeyEnv)

	// 3. Initialize Tunnel Manager & Supervisor (Pure Go Native SSH Engine)
	tunnelMgr := sshtunnel.NewManager(cfgMgr, logDir, sshConfigDir)
	tunnelMgr.StartSupervisor()

	// 4. Initialize Terminal Manager
	termMgr := terminal.NewSessionManager(cfgMgr)

	staticDir := "web/static"
	// 5. Initialize API Handler
	apiHandler := api.NewHandler(authMgr, cfgMgr, tunnelMgr, termMgr, staticDir, sshConfigDir, version)

	// Initial tunnel sync
	tunnelMgr.SyncWithConfig()

	// Set up HTTP Router
	mux := http.NewServeMux()

	// Static Assets: prioritize local web/static if present, otherwise use embedded FS
	var staticFS http.FileSystem
	if _, err := os.Stat("web/static"); err == nil {
		staticFS = http.Dir("web/static")
	} else {
		sub, err := fs.Sub(embeddedFiles, "web/static")
		if err != nil {
			log.Fatalf("[FATAL] Failed to access embedded static files: %v", err)
		}
		staticFS = http.FS(sub)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(staticFS)))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/favicon.svg", http.StatusMovedPermanently)
	})

	// Helper to load templates (local files or embedded FS)
	loadTemplate := func(tmplName string) (*template.Template, error) {
		localPath := filepath.Join("web/templates", tmplName)
		if _, err := os.Stat(localPath); err == nil {
			return template.ParseFiles(localPath)
		}
		tmplData, err := embeddedFiles.ReadFile("web/templates/" + tmplName)
		if err != nil {
			return nil, err
		}
		return template.New(tmplName).Parse(string(tmplData))
	}

	// Page Handlers
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := loadTemplate("login.html")
		if err != nil {
			http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = tmpl.Execute(w, map[string]interface{}{"Version": version})
	})

	// Protected Page Handlers
	mux.HandleFunc("/", authMgr.Middleware(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		tmpl, err := loadTemplate("index.html")
		if err != nil {
			http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = tmpl.Execute(w, map[string]interface{}{"Version": version})
	}))

	mux.HandleFunc("/help", authMgr.Middleware(func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := loadTemplate("help.html")
		if err != nil {
			http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = tmpl.Execute(w, map[string]interface{}{"Version": version})
	}))

	mux.HandleFunc("/tunnel-detail", authMgr.Middleware(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	}))

	// API Endpoints (Auth routes)
	mux.HandleFunc("/api/auth/login", apiHandler.HandleLogin)
	mux.HandleFunc("/api/auth/logout", apiHandler.HandleLogout)
	mux.HandleFunc("/api/auth/change-password", authMgr.Middleware(apiHandler.HandleChangePassword))
	mux.HandleFunc("/api/auth/me", authMgr.Middleware(apiHandler.HandleAuthMe))

	// API Endpoints (Tunnels & Operations)
	mux.HandleFunc("/api/tunnels", authMgr.Middleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			apiHandler.HandleListTunnels(w, r)
		} else if r.Method == http.MethodPost {
			apiHandler.HandleSaveTunnel(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	mux.HandleFunc("/api/tunnels/", authMgr.Middleware(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
		if path == "start-all" || path == "stop-all" {
			apiHandler.HandleBatchControl(w, r)
			return
		}
		if strings.HasSuffix(path, "/start") || strings.HasSuffix(path, "/stop") || strings.HasSuffix(path, "/restart") {
			apiHandler.HandleControlTunnel(w, r)
			return
		}
		if strings.HasSuffix(path, "/logs") {
			apiHandler.HandleGetLogs(w, r)
			return
		}
		if r.Method == http.MethodGet {
			apiHandler.HandleGetTunnel(w, r)
		} else if r.Method == http.MethodDelete || r.Method == http.MethodPost {
			apiHandler.HandleDeleteTunnel(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	// API Endpoints (Test Connection & YAML & System)
	mux.HandleFunc("/api/config/api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ws_enabled":true}`))
	})
	mux.HandleFunc("/api/test-connection", authMgr.Middleware(apiHandler.HandleTestConnection))
	mux.HandleFunc("/api/config/yaml", authMgr.Middleware(apiHandler.HandleConfigYAML))
	mux.HandleFunc("/api/languages", apiHandler.HandleGetLanguages)
	mux.HandleFunc("/api/system/status", authMgr.Middleware(apiHandler.HandleSystemStatus))
	mux.HandleFunc("/ws/terminal", authMgr.Middleware(apiHandler.HandleTerminalWS))

	// Graceful shutdown handling
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[%s] [INFO] [SERVER] Server listening on http://0.0.0.0:%d",
			time.Now().Format("2006-01-02 15:04:05"), port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] ListenAndServe failed: %v", err)
		}
	}()

	<-stopChan
	log.Println("[INFO] [SERVER] Shutting down gracefully...")
	tunnelMgr.StopAll()
	time.Sleep(200 * time.Millisecond)
	log.Println("[INFO] [SERVER] Server stopped.")
}
