package terminal

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/oaklight/autossh-tunnel/internal/config"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Origin checked by auth middleware
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type SessionManager struct {
	mu        sync.Mutex
	cfgMgr    *config.Manager
	activeMap map[string]bool
}

func NewSessionManager(cfgMgr *config.Manager) *SessionManager {
	return &SessionManager{
		cfgMgr:    cfgMgr,
		activeMap: make(map[string]bool),
	}
}

// HandleTerminalWS handles incoming WebSocket connections and binds them to a PTY running SSH or autossh-cli auth
func (sm *SessionManager) HandleTerminalWS(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	if hash == "" {
		http.Error(w, "missing tunnel hash", http.StatusBadRequest)
		return
	}

	tunnel := sm.cfgMgr.GetTunnel(hash)
	if tunnel == nil {
		http.Error(w, "tunnel not found", http.StatusNotFound)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	// Build interactive SSH command
	args := []string{
		"-o", "StrictHostKeyChecking=accept-new",
	}

	if tunnel.SSHPort > 0 {
		args = append(args, "-p", fmt.Sprintf("%d", tunnel.SSHPort))
	}
	if tunnel.IdentityFile != "" {
		args = append(args, "-i", tunnel.IdentityFile)
	}
	if tunnel.ProxyJump != "" {
		args = append(args, "-J", tunnel.ProxyJump)
	}

	// Port forwarding
	fwdFlags, err := tunnel.BuildForwardingSpec()
	if err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("\r\nConfiguration error: %v\r\n", err)))
		return
	}
	args = append(args, fwdFlags...)

	args = append(args, "-N", tunnel.RemoteHost)

	cmd := exec.Command("ssh", args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	// Start command in a pseudo-terminal
	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("\r\nFailed to start PTY: %v\r\n", err)))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	// Channel to signal session end
	done := make(chan struct{})
	var closeOnce sync.Once
	safeCloseDone := func() {
		closeOnce.Do(func() {
			close(done)
		})
	}

	// Goroutine: PTY -> WebSocket
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				safeCloseDone()
				return
			}
			if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				safeCloseDone()
				return
			}
		}
	}()

	// Goroutine: WebSocket -> PTY
	go func() {
		for {
			messageType, p, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
				_, _ = ptmx.Write(p)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Minute): // 10 minute max interactive session
	}
}
