package sshtunnel

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/oaklight/autossh-tunnel/internal/config"
	"github.com/oaklight/autossh-tunnel/internal/monitor"
	"github.com/oaklight/autossh-tunnel/internal/retry"
	"golang.org/x/crypto/ssh"
)

// Status constants
const (
	StatusActive              = "active"               // Connected and forwarding
	StatusDegraded            = "degraded"             // Connected but target unreachable
	StatusRetrying            = "retrying"             // Failed, waiting for exponential backoff retry
	StatusFailed              = "failed"               // Dead and max retries exceeded or fatal error
	StatusStopped             = "stopped"              // Explicitly stopped or disabled
	StatusInteractiveRequired = "interactive_required" // Interactive auth waiting for terminal input
)

type TunnelRuntime struct {
	Config         *config.TunnelConfig      `json:"config"`
	Hash           string                    `json:"hash"`
	Status         string                    `json:"status"`
	StartedAt      *time.Time                `json:"started_at,omitempty"`
	UptimeSeconds  int64                     `json:"uptime_seconds"`
	LastCheckAt    *time.Time                `json:"last_check_at,omitempty"`
	LatencyMs      int64                     `json:"latency_ms"`
	PortReachable  bool                      `json:"port_reachable"`
	Diagnostic     *monitor.DiagnosticResult `json:"diagnostic,omitempty"`
	RetryState     *retry.TunnelRetryState   `json:"retry_state,omitempty"`
	Metrics        TunnelMetrics             `json:"metrics"`

	cancel         context.CancelFunc        `json:"-"`
	sshClient      *ssh.Client               `json:"-"`
	listener       net.Listener              `json:"-"`
	metricsTracker *TunnelMetrics            `json:"-"`
	activeConnsMu  *sync.Mutex               `json:"-"`
	activeConns    map[net.Conn]struct{}     `json:"-"`
	logFile        string                    `json:"-"`
}

func (rt *TunnelRuntime) registerConn(c net.Conn) {
	if c == nil {
		return
	}
	if rt.activeConnsMu == nil {
		return
	}
	rt.activeConnsMu.Lock()
	defer rt.activeConnsMu.Unlock()
	if rt.activeConns == nil {
		rt.activeConns = make(map[net.Conn]struct{})
	}
	rt.activeConns[c] = struct{}{}
}

func (rt *TunnelRuntime) unregisterConn(c net.Conn) {
	if c == nil {
		return
	}
	if rt.activeConnsMu == nil {
		return
	}
	rt.activeConnsMu.Lock()
	defer rt.activeConnsMu.Unlock()
	if rt.activeConns != nil {
		delete(rt.activeConns, c)
	}
}

func (rt *TunnelRuntime) closeAllConns() {
	if rt.activeConnsMu == nil {
		return
	}
	rt.activeConnsMu.Lock()
	defer rt.activeConnsMu.Unlock()

	for c := range rt.activeConns {
		_ = c.Close()
	}
	rt.activeConns = make(map[net.Conn]struct{})
}

type Manager struct {
	mu           sync.RWMutex
	cfgMgr       *config.Manager
	retryCtrl    *retry.Controller
	runtimes     map[string]*TunnelRuntime
	logDir       string
	sshConfigDir string
	stopChan     chan struct{}
}

func NewManager(cfgMgr *config.Manager, logDir string, sshConfigDir string) *Manager {
	_ = os.MkdirAll(logDir, 0777)
	m := &Manager{
		cfgMgr:       cfgMgr,
		retryCtrl:    retry.NewController(),
		runtimes:     make(map[string]*TunnelRuntime),
		logDir:       logDir,
		sshConfigDir: sshConfigDir,
		stopChan:     make(chan struct{}),
	}
	return m
}

func (m *Manager) StartSupervisor() {
	go m.supervisorLoop()
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for hash, rt := range m.runtimes {
		if rt.Status != StatusStopped && rt.Config != nil {
			m.appendLog(rt, fmt.Sprintf("Tunnel [%s] stopped (batch stop all).", rt.Config.Name))
		}
		rt.Status = StatusStopped
		m.stopTunnelLocked(rt)
		m.retryCtrl.Reset(hash)
	}
}

func (m *Manager) StartAll() {
	m.SyncWithConfig()
}

func (m *Manager) SyncWithConfig() {
	cfg := m.cfgMgr.GetConfigInternal()
	activeHashes := make(map[string]bool)

	for _, t := range cfg.Tunnels {
		hash := t.CalculateHash()
		activeHashes[hash] = true

		m.mu.Lock()
		rt, exists := m.runtimes[hash]
		if !exists {
			rt = &TunnelRuntime{
				Config:         t,
				Hash:           hash,
				Status:         StatusStopped,
				metricsTracker: &TunnelMetrics{},
				activeConnsMu:  &sync.Mutex{},
				activeConns:    make(map[net.Conn]struct{}),
				logFile:        filepath.Join(m.logDir, fmt.Sprintf("%s.log", hash)),
			}
			m.runtimes[hash] = rt
		} else {
			rt.Config = t
		}
		m.mu.Unlock()

		if t.IsEnabled() && !t.Interactive {
			m.mu.RLock()
			status := rt.Status
			m.mu.RUnlock()
			if status == StatusStopped || status == StatusFailed {
				go m.StartTunnel(hash)
			}
		}
	}

	// Clean up removed tunnels
	m.mu.Lock()
	for hash, rt := range m.runtimes {
		if !activeHashes[hash] {
			m.stopTunnelLocked(rt)
			delete(m.runtimes, hash)
		}
	}
	m.mu.Unlock()
}

func (m *Manager) StartTunnel(hash string) error {
	m.mu.Lock()
	rt, exists := m.runtimes[hash]
	if !exists {
		t := m.cfgMgr.GetTunnelInternal(hash)
		if t == nil {
			m.mu.Unlock()
			return fmt.Errorf("tunnel with hash %s not found", hash)
		}
		rt = &TunnelRuntime{
			Config:         t,
			Hash:           hash,
			Status:         StatusStopped,
			metricsTracker: &TunnelMetrics{},
			activeConnsMu:  &sync.Mutex{},
			activeConns:    make(map[net.Conn]struct{}),
			logFile:        filepath.Join(m.logDir, fmt.Sprintf("%s.log", hash)),
		}
		m.runtimes[hash] = rt
	}

	// Stop any existing session
	m.stopTunnelLocked(rt)

	if rt.Config.Interactive {
		rt.Status = StatusInteractiveRequired
		m.mu.Unlock()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	rt.cancel = cancel
	rt.Status = StatusActive
	m.mu.Unlock()

	// Launch async connection and forwarding loop
	go m.runTunnelSession(ctx, rt)
	return nil
}

func (m *Manager) StopTunnel(hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rt, exists := m.runtimes[hash]
	if !exists {
		return fmt.Errorf("tunnel %s not found", hash)
	}

	if rt.Config != nil {
		m.appendLog(rt, fmt.Sprintf("Tunnel [%s] stopped by user.", rt.Config.Name))
	}
	rt.Status = StatusStopped
	m.stopTunnelLocked(rt)
	m.retryCtrl.Reset(hash)
	return nil
}

func (m *Manager) RestartTunnel(hash string) error {
	m.mu.RLock()
	if rt, exists := m.runtimes[hash]; exists && rt.Config != nil {
		m.appendLog(rt, fmt.Sprintf("Restarting tunnel [%s]...", rt.Config.Name))
	}
	m.mu.RUnlock()

	_ = m.StopTunnel(hash)
	return m.StartTunnel(hash)
}

func (m *Manager) stopTunnelLocked(rt *TunnelRuntime) {
	hadConnection := (rt.sshClient != nil || rt.listener != nil)

	if rt.cancel != nil {
		rt.cancel()
		rt.cancel = nil
	}
	if rt.listener != nil {
		_ = rt.listener.Close()
		rt.listener = nil
	}
	if rt.sshClient != nil {
		_ = rt.sshClient.Close()
		rt.sshClient = nil
	}

	// Force close all active forwarded TCP connections immediately
	rt.closeAllConns()

	if hadConnection {
		m.appendLog(rt, "SSH session closed and listening ports released.")
	}

	rt.StartedAt = nil
	rt.UptimeSeconds = 0
	rt.PortReachable = false
}

func (m *Manager) appendLog(rt *TunnelRuntime, msg string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	entry := fmt.Sprintf("[%s] %s\n", timestamp, msg)
	f, err := os.OpenFile(rt.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		_, _ = f.WriteString(entry)
		_ = f.Close()
	}
}

func (m *Manager) runTunnelSession(ctx context.Context, rt *TunnelRuntime) {
	t := rt.Config
	m.appendLog(rt, fmt.Sprintf("Starting tunnel [%s] to %s...", t.Name, t.RemoteHost))

	start := time.Now()
	client, err := DialSSH(ctx, t, m.sshConfigDir)
	if err != nil {
		m.handleTunnelFailure(rt, err, "SSH connection handshake failed")
		return
	}

	m.mu.Lock()
	if rt.Status == StatusStopped || ctx.Err() != nil {
		_ = client.Close()
		m.mu.Unlock()
		return
	}
	now := time.Now()
	rt.sshClient = client
	rt.StartedAt = &now
	rt.Status = StatusActive
	rt.LatencyMs = time.Since(start).Milliseconds()
	rt.Diagnostic = nil
	m.mu.Unlock()

	m.appendLog(rt, fmt.Sprintf("SSH connection established in %dms. Starting port forwarding...", rt.LatencyMs))

	// Start KeepAlive
	keepAliveInterval := 30 * time.Second
	if t.ServerAliveInterval > 0 {
		keepAliveInterval = time.Duration(t.ServerAliveInterval) * time.Second
	}
	StartKeepAlive(ctx, client, keepAliveInterval, func() {
		m.handleTunnelFailure(rt, fmt.Errorf("keepalive ping timeout"), "Keepalive ping failed")
	})

	// Setup Forwarding
	dir := t.GetDirection()
	var forwardErr error

	switch dir {
	case config.DirectionDynamicSocks5:
		forwardErr = m.startDynamicSOCKS5(ctx, rt, client)
	case config.DirectionLocalToRemote:
		forwardErr = m.startLocalToRemote(ctx, rt, client)
	case config.DirectionRemoteToLocal:
		fallthrough
	default:
		forwardErr = m.startRemoteToLocal(ctx, rt, client)
	}

	if forwardErr != nil {
		m.handleTunnelFailure(rt, forwardErr, "Port forwarding error")
	}
}

func listenTCPWithReuse(ctx context.Context, address string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var operr error
			err := c.Control(func(fd uintptr) {
				operr = setReuseAddr(fd)
			})
			if err != nil {
				return err
			}
			return operr
		},
	}

	var listener net.Listener
	var lastErr error

	// Retry up to 5 times (50ms interval) to allow clean socket reclamation during rapid restarts
	for i := 0; i < 5; i++ {
		listener, lastErr = lc.Listen(ctx, "tcp", address)
		if lastErr == nil {
			return listener, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	return nil, lastErr
}

func (m *Manager) startRemoteToLocal(ctx context.Context, rt *TunnelRuntime, client *ssh.Client) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	t := rt.Config
	localBind := monitor.NormalizePortAddress(t.LocalPort, "127.0.0.1")

	remoteSpec := strings.TrimSpace(t.RemotePort)
	var remoteTarget string
	if strings.Contains(remoteSpec, ":") {
		remoteTarget = remoteSpec
	} else {
		remoteTarget = net.JoinHostPort("127.0.0.1", remoteSpec)
	}

	listener, err := listenTCPWithReuse(ctx, localBind)
	if err != nil {
		return fmt.Errorf("failed to listen on local port %s: %w", localBind, err)
	}
	defer listener.Close()

	m.mu.Lock()
	if rt.Status == StatusStopped || ctx.Err() != nil {
		_ = listener.Close()
		m.mu.Unlock()
		return nil
	}
	rt.listener = listener
	rt.PortReachable = true
	m.mu.Unlock()

	m.appendLog(rt, fmt.Sprintf("Listening locally on %s, forwarding to remote %s", localBind, remoteTarget))

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		localConn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}

		go func(lConn net.Conn) {
			rt.registerConn(lConn)
			defer rt.unregisterConn(lConn)

			remoteConn, err := client.Dial("tcp", remoteTarget)
			if err != nil {
				_ = lConn.Close()
				m.appendLog(rt, fmt.Sprintf("Failed to dial remote target %s: %v", remoteTarget, err))
				return
			}
			rt.registerConn(remoteConn)
			defer rt.unregisterConn(remoteConn)

			Pipe(lConn, remoteConn, rt.metricsTracker)
		}(localConn)
	}
}

func (m *Manager) startLocalToRemote(ctx context.Context, rt *TunnelRuntime, client *ssh.Client) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	t := rt.Config
	localTarget := monitor.NormalizePortAddress(t.LocalPort, "127.0.0.1")

	remoteSpec := strings.TrimSpace(t.RemotePort)
	var remoteBind string
	if strings.Contains(remoteSpec, ":") {
		remoteBind = remoteSpec
	} else {
		remoteBind = net.JoinHostPort("0.0.0.0", remoteSpec)
	}

	remoteListener, err := client.Listen("tcp", remoteBind)
	if err != nil {
		return fmt.Errorf("remote SSH server failed to bind port %s: %w", remoteBind, err)
	}
	defer remoteListener.Close()

	m.mu.Lock()
	if rt.Status == StatusStopped || ctx.Err() != nil {
		_ = remoteListener.Close()
		m.mu.Unlock()
		return nil
	}
	rt.listener = remoteListener
	rt.PortReachable = true
	m.mu.Unlock()

	m.appendLog(rt, fmt.Sprintf("Remote server listening on %s, forwarding to local %s", remoteBind, localTarget))

	go func() {
		<-ctx.Done()
		_ = remoteListener.Close()
	}()

	for {
		remoteConn, err := remoteListener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}

		go func(rConn net.Conn) {
			rt.registerConn(rConn)
			defer rt.unregisterConn(rConn)

			localConn, err := net.Dial("tcp", localTarget)
			if err != nil {
				_ = rConn.Close()
				m.appendLog(rt, fmt.Sprintf("Failed to dial local service %s: %v", localTarget, err))
				return
			}
			rt.registerConn(localConn)
			defer rt.unregisterConn(localConn)

			Pipe(rConn, localConn, rt.metricsTracker)
		}(remoteConn)
	}
}

func (m *Manager) startDynamicSOCKS5(ctx context.Context, rt *TunnelRuntime, client *ssh.Client) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	t := rt.Config
	localBind := monitor.NormalizePortAddress(t.LocalPort, "127.0.0.1")

	listener, err := listenTCPWithReuse(ctx, localBind)
	if err != nil {
		return fmt.Errorf("failed to listen on local SOCKS5 port %s: %w", localBind, err)
	}
	defer listener.Close()

	m.mu.Lock()
	if rt.Status == StatusStopped || ctx.Err() != nil {
		_ = listener.Close()
		m.mu.Unlock()
		return nil
	}
	rt.listener = listener
	rt.PortReachable = true
	m.mu.Unlock()

	m.appendLog(rt, fmt.Sprintf("Dynamic SOCKS5 proxy listening on %s", localBind))

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}

		go func(conn net.Conn) {
			rt.registerConn(conn)
			defer rt.unregisterConn(conn)
			_ = HandleSOCKS5(conn, client, rt.metricsTracker)
		}(clientConn)
	}
}

func (m *Manager) handleTunnelFailure(rt *TunnelRuntime, err error, contextMsg string) {
	m.mu.Lock()
	if rt.Status == StatusStopped {
		m.mu.Unlock()
		return
	}

	m.stopTunnelLocked(rt)

	rawErrMsg := fmt.Sprintf("%s: %v", contextMsg, err)
	m.appendLog(rt, "ERROR: "+rawErrMsg)

	diag := monitor.DiagnoseSSHError(rawErrMsg)
	rt.Diagnostic = &diag

	hash := rt.Hash
	shouldRestart := rt.Config.IsAutoRestart() && rt.Config.IsEnabled() && !rt.Config.Interactive

	if !shouldRestart {
		rt.Status = StatusFailed
		m.mu.Unlock()
		return
	}

	shouldRetry, delay := m.retryCtrl.RecordFailure(hash, rt.Config.MaxRetries, rt.Config.RetryInterval)
	if !shouldRetry {
		rt.Status = StatusFailed
		rt.RetryState = m.retryCtrl.GetState(hash)
		m.mu.Unlock()
		return
	}

	rt.Status = StatusRetrying
	rt.RetryState = m.retryCtrl.GetState(hash)
	m.mu.Unlock()

	m.appendLog(rt, fmt.Sprintf("Tunnel disconnected. Retrying in %v (Attempt %d/%d)...",
		delay, rt.RetryState.Count, rt.RetryState.MaxRetries))

	go func(h string, d time.Duration) {
		time.Sleep(d)
		m.mu.RLock()
		cur, ok := m.runtimes[h]
		if !ok || cur.Status != StatusRetrying {
			m.mu.RUnlock()
			return
		}
		m.mu.RUnlock()
		_ = m.StartTunnel(h)
	}(hash, delay)
}

func (m *Manager) supervisorLoop() {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.runHealthChecks()
		}
	}
}

func (m *Manager) runHealthChecks() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	for hash, rt := range m.runtimes {
		if rt.metricsTracker != nil {
			rt.Metrics = rt.metricsTracker.Snapshot()
		}

		if rt.Status != StatusActive && rt.Status != StatusDegraded {
			continue
		}

		if rt.StartedAt != nil {
			rt.UptimeSeconds = int64(now.Sub(*rt.StartedAt).Seconds())
		}

		if rt.UptimeSeconds > 30 {
			m.retryCtrl.RecordSuccess(hash)
		}

		rt.LastCheckAt = &now
		rt.RetryState = m.retryCtrl.GetState(hash)
	}
}

func (m *Manager) GetRuntime(hash string) *TunnelRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rt, exists := m.runtimes[hash]
	if !exists {
		return nil
	}
	cp := &TunnelRuntime{
		Hash:          rt.Hash,
		Status:        rt.Status,
		StartedAt:     rt.StartedAt,
		UptimeSeconds: rt.UptimeSeconds,
		LastCheckAt:   rt.LastCheckAt,
		LatencyMs:     rt.LatencyMs,
		PortReachable: rt.PortReachable,
		Diagnostic:    rt.Diagnostic,
		RetryState:    rt.RetryState,
	}
	if rt.metricsTracker != nil {
		cp.Metrics = rt.metricsTracker.Snapshot()
	}
	if rt.Config != nil {
		tClone := *rt.Config
		tClone.Password = ""
		tClone.PrivateKeyContent = ""
		cp.Config = &tClone
	}
	return cp
}

func (m *Manager) GetAllRuntimes() []*TunnelRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*TunnelRuntime, 0, len(m.runtimes))
	for _, rt := range m.runtimes {
		cp := &TunnelRuntime{
			Hash:          rt.Hash,
			Status:        rt.Status,
			StartedAt:     rt.StartedAt,
			UptimeSeconds: rt.UptimeSeconds,
			LastCheckAt:   rt.LastCheckAt,
			LatencyMs:     rt.LatencyMs,
			PortReachable: rt.PortReachable,
			Diagnostic:    rt.Diagnostic,
			RetryState:    rt.RetryState,
		}
		if rt.metricsTracker != nil {
			cp.Metrics = rt.metricsTracker.Snapshot()
		}
		if rt.Config != nil {
			tClone := *rt.Config
			tClone.Password = ""
			tClone.PrivateKeyContent = ""
			cp.Config = &tClone
		}
		result = append(result, cp)
	}
	return result
}

func (m *Manager) GetLogs(hash string, tailLines int) (string, error) {
	m.mu.RLock()
	rt, exists := m.runtimes[hash]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("tunnel not found: %s", hash)
	}

	data, err := os.ReadFile(rt.logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	if tailLines <= 0 {
		return string(data), nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > tailLines {
		return strings.Join(lines[len(lines)-tailLines:], "\n"), nil
	}
	return string(data), nil
}
