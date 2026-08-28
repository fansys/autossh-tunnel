package sshtunnel

import (
	"testing"

	"github.com/oaklight/autossh-tunnel/internal/config"
)

func TestBuildSSHClientConfigPassword(t *testing.T) {
	tunnel := &config.TunnelConfig{
		Name:       "pwd-test",
		RemoteHost: "testuser@127.0.0.1",
		SSHPort:    22,
		AuthType:   config.AuthTypePassword,
		Password:   "MySecretPassword123",
	}

	clientCfg, targetAddr, err := BuildSSHClientConfig(tunnel, "")
	if err != nil {
		t.Fatalf("BuildSSHClientConfig failed: %v", err)
	}

	if clientCfg.User != "testuser" {
		t.Errorf("Expected user 'testuser', got '%s'", clientCfg.User)
	}

	if targetAddr != "127.0.0.1:22" {
		t.Errorf("Expected targetAddr '127.0.0.1:22', got '%s'", targetAddr)
	}

	if len(clientCfg.Auth) < 2 {
		t.Errorf("Expected at least 2 auth methods (password & keyboard-interactive), got %d", len(clientCfg.Auth))
	}
}
