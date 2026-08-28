package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigManagerAndPrivateKeySaving(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cfg_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.yaml")
	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	testKeyContent := "-----BEGIN OPENSSH PRIVATE KEY-----\ntest-private-key-content-12345\n-----END OPENSSH PRIVATE KEY-----"

	tunnel := &TunnelConfig{
		Name:              "test-tunnel",
		RemoteHost:        "user@example.com",
		RemotePort:        "8080",
		LocalPort:         "8080",
		Direction:         DirectionRemoteToLocal,
		PrivateKeyContent: testKeyContent,
	}

	// 1. Save tunnel with private key content
	if err := mgr.ProcessAndSaveTunnel(tunnel); err != nil {
		t.Fatalf("ProcessAndSaveTunnel failed: %v", err)
	}

	// 2. Verify key was written to file with 0600 permissions
	if tunnel.IdentityFile == "" {
		t.Fatalf("IdentityFile was not set")
	}
	keyData, err := os.ReadFile(tunnel.IdentityFile)
	if err != nil {
		t.Fatalf("Failed to read saved key file: %v", err)
	}
	if !strings.Contains(string(keyData), "test-private-key-content-12345") {
		t.Errorf("Key file content mismatch")
	}

	// 3. Verify GetConfig() DOES NOT leak private key or password
	loadedCfg := mgr.GetConfig()
	if len(loadedCfg.Tunnels) != 1 {
		t.Fatalf("Expected 1 tunnel, got %d", len(loadedCfg.Tunnels))
	}
	loadedTunnel := loadedCfg.Tunnels[0]
	if loadedTunnel.PrivateKeyContent != "" {
		t.Errorf("Security violation: PrivateKeyContent must not be returned in GetConfig()")
	}
	if !loadedTunnel.HasIdentityFile {
		t.Errorf("HasIdentityFile should be true")
	}

	// 4. Verify Raw YAML
	yamlStr, err := mgr.GetRawYAML()
	if err != nil {
		t.Fatalf("GetRawYAML failed: %v", err)
	}
	if strings.Contains(yamlStr, "test-private-key-content-12345") {
		t.Errorf("Raw YAML must not contain inline private key content")
	}
	if !strings.Contains(yamlStr, "identity_file:") {
		t.Errorf("Raw YAML should contain identity_file reference")
	}
}

func TestBuildForwardingSpec(t *testing.T) {
	tests := []struct {
		name      string
		tunnel    TunnelConfig
		wantFlags []string
		wantErr   bool
	}{
		{
			name: "Remote to Local simple port",
			tunnel: TunnelConfig{
				Direction:  DirectionRemoteToLocal,
				LocalPort:  "8001",
				RemotePort: "8000",
			},
			wantFlags: []string{"-L", "8001:localhost:8000"},
		},
		{
			name: "Remote to Local with explicit bind and target",
			tunnel: TunnelConfig{
				Direction:  DirectionRemoteToLocal,
				LocalPort:  "0.0.0.0:8001",
				RemotePort: "192.168.1.100:8000",
			},
			wantFlags: []string{"-L", "0.0.0.0:8001:192.168.1.100:8000"},
		},
		{
			name: "Local to Remote simple port",
			tunnel: TunnelConfig{
				Direction:  DirectionLocalToRemote,
				LocalPort:  "3306",
				RemotePort: "13306",
			},
			wantFlags: []string{"-R", "13306:localhost:3306"},
		},
		{
			name: "Dynamic SOCKS5 proxy",
			tunnel: TunnelConfig{
				Direction: DirectionDynamicSocks5,
				LocalPort: "1080",
			},
			wantFlags: []string{"-D", "1080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, err := tt.tunnel.BuildForwardingSpec()
			if (err != nil) != tt.wantErr {
				t.Fatalf("BuildForwardingSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(flags) != len(tt.wantFlags) {
				t.Fatalf("BuildForwardingSpec() = %v, want %v", flags, tt.wantFlags)
			}
			for i := range flags {
				if flags[i] != tt.wantFlags[i] {
					t.Errorf("flags[%d] = %v, want %v", i, flags[i], tt.wantFlags[i])
				}
			}
		})
	}
}
