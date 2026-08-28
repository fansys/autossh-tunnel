package config

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// TunnelDirection constants
const (
	DirectionRemoteToLocal = "remote_to_local" // -L: local_port -> remote_host:remote_port
	DirectionLocalToRemote = "local_to_remote" // -R: remote_port -> local_port
	DirectionDynamicSocks5 = "dynamic_socks5"  // -D: local_port dynamic socks proxy
)

// AuthType constants
const (
	AuthTypeKey         = "key"
	AuthTypePassword    = "password"
	AuthTypeInteractive = "interactive"
)

// TunnelConfig represents full configuration options for an SSH tunnel
type TunnelConfig struct {
	// Basic Settings
	Name        string `yaml:"name,omitempty" json:"name"`
	Enabled     *bool  `yaml:"enabled,omitempty" json:"enabled"`
	Direction   string `yaml:"direction,omitempty" json:"direction"`
	RemoteHost  string `yaml:"remote_host" json:"remote_host"`
	RemotePort  string `yaml:"remote_port,omitempty" json:"remote_port"` // can be "8080" or "0.0.0.0:8080"
	LocalPort   string `yaml:"local_port,omitempty" json:"local_port"`   // can be "8080" or "127.0.0.1:8080"

	// Authentication Settings
	AuthType     string `yaml:"auth_type,omitempty" json:"auth_type"` // "key", "password", "interactive"
	Password     string `yaml:"password,omitempty" json:"password,omitempty"`
	PasswordEnv  string `yaml:"password_env,omitempty" json:"password_env,omitempty"`
	IdentityFile string `yaml:"identity_file,omitempty" json:"identity_file,omitempty"`
	Interactive  bool   `yaml:"interactive,omitempty" json:"interactive"`

	// SSH Advanced Tuning Options
	SSHPort                int    `yaml:"ssh_port,omitempty" json:"ssh_port,omitempty"`
	SSHUser                string `yaml:"ssh_user,omitempty" json:"ssh_user,omitempty"`
	ServerAliveInterval    int    `yaml:"server_alive_interval,omitempty" json:"server_alive_interval,omitempty"`
	ServerAliveCountMax    int    `yaml:"server_alive_count_max,omitempty" json:"server_alive_count_max,omitempty"`
	ConnectTimeout         int    `yaml:"connect_timeout,omitempty" json:"connect_timeout,omitempty"`
	StrictHostKeyChecking  string `yaml:"strict_host_key_checking,omitempty" json:"strict_host_key_checking,omitempty"` // "accept-new", "yes", "no"
	Compression            string `yaml:"compression,omitempty" json:"compression,omitempty"`                     // "yes", "no"
	ProxyJump              string `yaml:"proxy_jump,omitempty" json:"proxy_jump,omitempty"`
	ProxyCommand           string `yaml:"proxy_command,omitempty" json:"proxy_command,omitempty"`
	ExtraSSHArgs           string `yaml:"extra_ssh_args,omitempty" json:"extra_ssh_args,omitempty"`

	// Health Check & Retry Policy
	HealthCheckEnabled  *bool `yaml:"health_check_enabled,omitempty" json:"health_check_enabled"`
	HealthCheckInterval int   `yaml:"health_check_interval,omitempty" json:"health_check_interval,omitempty"` // in seconds, default 15
	AutoRestart         *bool `yaml:"auto_restart,omitempty" json:"auto_restart"`
	MaxRetries          int   `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`                       // default 10, 0 = unlimited
	RetryInterval       int   `yaml:"retry_interval,omitempty" json:"retry_interval,omitempty"`               // initial retry backoff in seconds, default 5

	// Write-only payload field (never exported to YAML or returned in GET API)
	PrivateKeyContent string `yaml:"-" json:"private_key_content,omitempty"`

	// Computed Read-Only Fields for UI
	Hash            string `yaml:"-" json:"hash"`
	HasIdentityFile bool   `yaml:"-" json:"has_identity_file"`
	HasPassword     bool   `yaml:"-" json:"has_password"`
}

// Config represents root config.yaml structure (All system state & credentials persisted here)
type Config struct {
	JWTSecret string          `yaml:"jwt_secret,omitempty" json:"-"`
	Username  string          `yaml:"username,omitempty" json:"-"`
	Password  string          `yaml:"password,omitempty" json:"-"` // Persisted bcrypt password hash
	Tunnels   []*TunnelConfig `yaml:"tunnels" json:"tunnels"`
}

type Manager struct {
	mu         sync.RWMutex
	configPath string
	backupDir  string
	keysDir    string
	logsDir    string
	cfg        *Config
}

func generateRandomSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewManager(configPath string) (*Manager, error) {
	configDir := filepath.Dir(configPath)
	m := &Manager{
		configPath: configPath,
		backupDir:  filepath.Join(configDir, "backups"),
		keysDir:    filepath.Join(configDir, "keys"),
		logsDir:    filepath.Join(configDir, "logs"),
	}

	_ = os.MkdirAll(configDir, 0755)
	_ = os.MkdirAll(m.backupDir, 0755)
	_ = os.MkdirAll(m.keysDir, 0700)
	_ = os.MkdirAll(m.logsDir, 0755)

	if err := m.Load(); err != nil {
		// If file doesn't exist, create an auto-initialized config file with persisted credentials
		if os.IsNotExist(err) {
			adminUser := os.Getenv("USERNAME")
			if adminUser == "" {
				adminUser = "admin"
			}
			adminPass := os.Getenv("PASSWORD")
			if adminPass == "" {
				adminPass = "admin888"
			}
			passHash, _ := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)

			jwtSec := os.Getenv("JWT_SECRET")
			if jwtSec == "" {
				jwtSec = generateRandomSecret()
			}

			m.cfg = &Config{
				JWTSecret: jwtSec,
				Username:  adminUser,
				Password:  string(passHash),
				Tunnels:   make([]*TunnelConfig, 0),
			}
			if err := m.Save(); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return m, nil
}

// BuildForwardingSpec generates standard OpenSSH forwarding flags (-L, -R, -D)
func (t *TunnelConfig) BuildForwardingSpec() ([]string, error) {
	dir := t.GetDirection()
	switch dir {
	case DirectionDynamicSocks5:
		if strings.TrimSpace(t.LocalPort) == "" {
			return nil, fmt.Errorf("local_port is required for dynamic SOCKS5 proxy")
		}
		return []string{"-D", strings.TrimSpace(t.LocalPort)}, nil

	case DirectionLocalToRemote:
		// -R [remote_bind:]remote_port:local_host:local_port
		if strings.TrimSpace(t.RemotePort) == "" || strings.TrimSpace(t.LocalPort) == "" {
			return nil, fmt.Errorf("both remote_port and local_port are required")
		}
		remoteSpec := strings.TrimSpace(t.RemotePort)
		localSpec := strings.TrimSpace(t.LocalPort)

		var localHost, localPort string
		if strings.Contains(localSpec, ":") {
			parts := strings.SplitN(localSpec, ":", 2)
			localHost = parts[0]
			localPort = parts[1]
		} else {
			localHost = "localhost"
			localPort = localSpec
		}

		return []string{"-R", fmt.Sprintf("%s:%s:%s", remoteSpec, localHost, localPort)}, nil

	case DirectionRemoteToLocal:
		fallthrough
	default:
		// -L [local_bind:]local_port:remote_host:remote_port
		if strings.TrimSpace(t.RemotePort) == "" || strings.TrimSpace(t.LocalPort) == "" {
			return nil, fmt.Errorf("both local_port and remote_port are required")
		}
		localSpec := strings.TrimSpace(t.LocalPort)
		remoteSpec := strings.TrimSpace(t.RemotePort)

		var remoteHost, remotePort string
		if strings.Contains(remoteSpec, ":") {
			parts := strings.SplitN(remoteSpec, ":", 2)
			remoteHost = parts[0]
			remotePort = parts[1]
		} else {
			remoteHost = "localhost"
			remotePort = remoteSpec
		}

		return []string{"-L", fmt.Sprintf("%s:%s:%s", localSpec, remoteHost, remotePort)}, nil
	}
}

// CalculateHash generates a unique MD5 hash for a tunnel configuration
func (t *TunnelConfig) CalculateHash() string {
	input := fmt.Sprintf("%s|%s|%s|%s|%s|%t",
		t.Name,
		t.RemoteHost,
		t.RemotePort,
		t.LocalPort,
		t.GetDirection(),
		t.Interactive,
	)
	hash := md5.Sum([]byte(input))
	return hex.EncodeToString(hash[:])
}

func (t *TunnelConfig) GetDirection() string {
	if t.Direction != "" {
		return t.Direction
	}
	return DirectionRemoteToLocal
}

func (t *TunnelConfig) IsEnabled() bool {
	if t.Enabled == nil {
		return true
	}
	return *t.Enabled
}

func (t *TunnelConfig) IsHealthCheckEnabled() bool {
	if t.HealthCheckEnabled == nil {
		return true
	}
	return *t.HealthCheckEnabled
}

func (t *TunnelConfig) IsAutoRestart() bool {
	if t.AutoRestart == nil {
		return true
	}
	return *t.AutoRestart
}

func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	if cfg.Tunnels == nil {
		cfg.Tunnels = make([]*TunnelConfig, 0)
	}

	needSave := false

	// 1. JWT Secret (Env > YAML > Generated)
	if envSecret := os.Getenv("JWT_SECRET"); envSecret != "" {
		cfg.JWTSecret = envSecret
	} else if cfg.JWTSecret == "" {
		cfg.JWTSecret = generateRandomSecret()
		needSave = true
	}

	// 2. Username (Env USERNAME > YAML username > "admin")
	if envUser := os.Getenv("USERNAME"); envUser != "" {
		cfg.Username = envUser
	} else if cfg.Username == "" {
		cfg.Username = "admin"
		needSave = true
	}

	// 3. Password (Env PASSWORD > YAML password > "admin888")
	if envPass := os.Getenv("PASSWORD"); envPass != "" {
		passHash, _ := bcrypt.GenerateFromPassword([]byte(envPass), bcrypt.DefaultCost)
		cfg.Password = string(passHash)
		needSave = true
	} else if cfg.Password == "" {
		passHash, _ := bcrypt.GenerateFromPassword([]byte("admin888"), bcrypt.DefaultCost)
		cfg.Password = string(passHash)
		needSave = true
	} else if !strings.HasPrefix(cfg.Password, "$2a$") && !strings.HasPrefix(cfg.Password, "$2b$") {
		// If password is in plaintext in YAML, automatically hash it for security
		passHash, _ := bcrypt.GenerateFromPassword([]byte(cfg.Password), bcrypt.DefaultCost)
		cfg.Password = string(passHash)
		needSave = true
	}

	for _, t := range cfg.Tunnels {
		t.Hash = t.CalculateHash()
		t.HasIdentityFile = (t.IdentityFile != "")
		t.HasPassword = (t.Password != "" || t.PasswordEnv != "")
		if t.AuthType == "" {
			if t.Interactive {
				t.AuthType = AuthTypeInteractive
			} else if t.HasPassword {
				t.AuthType = AuthTypePassword
			} else {
				t.AuthType = AuthTypeKey
			}
		}
	}

	m.cfg = &cfg
	if needSave {
		_ = m.saveLocked()
	}
	return nil
}

func (m *Manager) GetAdminCredentials() (username, passwordHash string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.cfg != nil {
		return m.cfg.Username, m.cfg.Password
	}
	return "admin", ""
}

func (m *Manager) UpdateAdminPassword(newPassword string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	passHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if m.cfg == nil {
		m.cfg = &Config{Tunnels: make([]*TunnelConfig, 0)}
	}

	m.cfg.Password = string(passHash)
	m.cfg.JWTSecret = generateRandomSecret() // Rotate JWT secret to invalidate old tokens
	return m.saveLocked()
}

func (m *Manager) GetJWTSecret() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.cfg != nil && m.cfg.JWTSecret != "" {
		return []byte(m.cfg.JWTSecret)
	}
	return []byte("default-fallback-jwt-secret-key-32")
}

func (m *Manager) RotateJWTSecret() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	newSecret := generateRandomSecret()
	if m.cfg == nil {
		m.cfg = &Config{Tunnels: make([]*TunnelConfig, 0)}
	}
	m.cfg.JWTSecret = newSecret
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	return []byte(newSecret), nil
}

func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a sanitized deep copy (never expose credentials)
	copied := &Config{
		JWTSecret: "",
		Username:  m.cfg.Username,
		Password:  "",
		Tunnels:   make([]*TunnelConfig, len(m.cfg.Tunnels)),
	}
	for i, t := range m.cfg.Tunnels {
		clone := *t
		clone.Hash = t.CalculateHash()
		clone.HasIdentityFile = (t.IdentityFile != "")
		clone.HasPassword = (t.Password != "" || t.PasswordEnv != "")
		clone.Password = ""
		clone.PrivateKeyContent = ""
		copied.Tunnels[i] = &clone
	}
	return copied
}

func (m *Manager) GetTunnel(hash string) *TunnelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, t := range m.cfg.Tunnels {
		h := t.CalculateHash()
		if h == hash || (len(hash) >= 8 && strings.HasPrefix(h, hash)) {
			clone := *t
			clone.Hash = h
			clone.HasIdentityFile = (t.IdentityFile != "")
			clone.HasPassword = (t.Password != "" || t.PasswordEnv != "")
			clone.Password = ""
			clone.PrivateKeyContent = ""
			return &clone
		}
	}
	return nil
}

func (m *Manager) GetTunnelInternal(hash string) *TunnelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, t := range m.cfg.Tunnels {
		h := t.CalculateHash()
		if h == hash || (len(hash) >= 8 && strings.HasPrefix(h, hash)) {
			clone := *t
			clone.Hash = h
			clone.HasIdentityFile = (t.IdentityFile != "")
			clone.HasPassword = (t.Password != "" || t.PasswordEnv != "")
			return &clone
		}
	}
	return nil
}

func (m *Manager) GetConfigInternal() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copied := &Config{
		JWTSecret: m.cfg.JWTSecret,
		Username:  m.cfg.Username,
		Password:  m.cfg.Password,
		Tunnels:   make([]*TunnelConfig, len(m.cfg.Tunnels)),
	}
	for i, t := range m.cfg.Tunnels {
		clone := *t
		clone.Hash = t.CalculateHash()
		clone.HasIdentityFile = (t.IdentityFile != "")
		clone.HasPassword = (t.Password != "" || t.PasswordEnv != "")
		copied.Tunnels[i] = &clone
	}
	return copied
}

func (m *Manager) GetRawYAML() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Manager) SaveRawYAML(rawContent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var temp Config
	if err := yaml.Unmarshal([]byte(rawContent), &temp); err != nil {
		return fmt.Errorf("invalid YAML syntax: %w", err)
	}

	_ = m.createBackupLocked()

	if err := os.WriteFile(m.configPath, []byte(rawContent), 0644); err != nil {
		return err
	}

	m.cfg = &temp
	for _, t := range m.cfg.Tunnels {
		t.Hash = t.CalculateHash()
	}
	return nil
}

func (m *Manager) SetTunnelEnabled(hash string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.cfg.Tunnels {
		h := t.CalculateHash()
		if h == hash || (len(hash) >= 8 && strings.HasPrefix(h, hash)) {
			t.Enabled = &enabled
			return m.saveLocked()
		}
	}
	return nil
}

func (m *Manager) SetAllTunnelsEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.cfg.Tunnels {
		t.Enabled = &enabled
	}
	return m.saveLocked()
}

func (m *Manager) ProcessAndSaveTunnel(tunnel *TunnelConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.TrimSpace(tunnel.PrivateKeyContent) != "" {
		keyHash := tunnel.CalculateHash()
		keyFileName := fmt.Sprintf("id_%s.key", keyHash[:8])
		keyFilePath := filepath.Join(m.keysDir, keyFileName)

		content := strings.TrimSpace(tunnel.PrivateKeyContent) + "\n"
		if err := os.WriteFile(keyFilePath, []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to save private key file: %w", err)
		}
		tunnel.IdentityFile = keyFilePath
		tunnel.PrivateKeyContent = ""
	}

	if tunnel.Direction == "" {
		tunnel.Direction = DirectionRemoteToLocal
	}
	if tunnel.AuthType == AuthTypeInteractive {
		tunnel.Interactive = true
	}

	tunnelHash := tunnel.CalculateHash()

	found := false
	for i, existing := range m.cfg.Tunnels {
		if existing.CalculateHash() == tunnelHash || (tunnel.Hash != "" && existing.CalculateHash() == tunnel.Hash) {
			if tunnel.AuthType == AuthTypePassword {
				if tunnel.Password == "" && tunnel.PasswordEnv == "" && existing.Password != "" {
					tunnel.Password = existing.Password
					tunnel.PasswordEnv = existing.PasswordEnv
				}
				tunnel.IdentityFile = ""
			} else if tunnel.AuthType == AuthTypeKey {
				tunnel.Password = ""
				tunnel.PasswordEnv = ""
				if tunnel.IdentityFile == "" && existing.IdentityFile != "" {
					tunnel.IdentityFile = existing.IdentityFile
				}
			}

			m.cfg.Tunnels[i] = tunnel
			found = true
			break
		}
	}
	if !found {
		m.cfg.Tunnels = append(m.cfg.Tunnels, tunnel)
	}

	return m.saveLocked()
}

func (m *Manager) DeleteTunnel(hash string) (*TunnelConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted *TunnelConfig
	newTunnels := make([]*TunnelConfig, 0, len(m.cfg.Tunnels))

	for _, t := range m.cfg.Tunnels {
		h := t.CalculateHash()
		if h == hash || (len(hash) >= 8 && strings.HasPrefix(h, hash)) {
			deleted = t
			if strings.HasPrefix(t.IdentityFile, m.keysDir) {
				_ = os.Remove(t.IdentityFile)
			}
		} else {
			newTunnels = append(newTunnels, t)
		}
	}

	if deleted == nil {
		return nil, fmt.Errorf("tunnel not found: %s", hash)
	}

	m.cfg.Tunnels = newTunnels
	if err := m.saveLocked(); err != nil {
		return nil, err
	}
	return deleted, nil
}

func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	_ = m.createBackupLocked()

	data, err := yaml.Marshal(m.cfg)
	if err != nil {
		return fmt.Errorf("failed to encode YAML: %w", err)
	}

	return os.WriteFile(m.configPath, data, 0644)
}

func (m *Manager) createBackupLocked() error {
	if _, err := os.Stat(m.configPath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format("20060102_150405")
	backupFile := filepath.Join(m.backupDir, fmt.Sprintf("config.yaml.%s", timestamp))
	return os.WriteFile(backupFile, data, 0644)
}
