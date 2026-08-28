package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oaklight/autossh-tunnel/internal/config"
)

func TestJWTGenerationAndValidation(t *testing.T) {
	secret := []byte("test-super-secret-key-1234567890")

	// 1. Valid Token
	token, err := GenerateJWT("admin", secret, 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	claims, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("ValidateJWT failed: %v", err)
	}
	if claims.Subject != "admin" {
		t.Errorf("Expected claims subject 'admin', got '%s'", claims.Subject)
	}

	// 2. Tampered Token
	tampered := token + "bad"
	if _, err := ValidateJWT(tampered, secret); err == nil {
		t.Errorf("ValidateJWT should fail for tampered token")
	}

	// 3. Expired Token
	expiredToken, err := GenerateJWT("admin", secret, -1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}
	if _, err := ValidateJWT(expiredToken, secret); err != ErrTokenExpired {
		t.Errorf("Expected ErrTokenExpired, got %v", err)
	}
}

func TestAuthManagerPersistedInConfigYAML(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "auth_yaml_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.yaml")
	cfgMgr1, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config.Manager: %v", err)
	}

	// Instance 1: User logs in using default credentials initialized in config.yaml
	mgr1 := NewManager(cfgMgr1, "testkey1")
	token, ok := mgr1.Authenticate("admin", "admin888")
	if !ok || token == "" {
		t.Fatalf("Login failed on instance 1")
	}

	if !mgr1.ValidateToken(token) {
		t.Errorf("ValidateToken failed on instance 1")
	}

	// Instance 2: Simulates Server Restart (re-reading everything purely from config.yaml)
	cfgMgr2, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config.Manager on instance 2: %v", err)
	}
	mgr2 := NewManager(cfgMgr2, "testkey1")

	// The JWT issued by Instance 1 MUST remain valid on Instance 2
	if !mgr2.ValidateToken(token) {
		t.Errorf("JWT issued before restart should remain valid after server restart!")
	}

	// Verify API key still works
	if !mgr2.ValidateToken("testkey1") {
		t.Errorf("API key should be valid")
	}

	// Change Password rotates secret and updates admin_password directly in config.yaml
	if !mgr2.ChangePassword("admin888", "newpassword123") {
		t.Errorf("ChangePassword failed")
	}

	// Old JWT must now be invalid
	if mgr2.ValidateToken(token) {
		t.Errorf("Old JWT should be invalidated after password change!")
	}

	// New login generates valid token with new password
	newToken, ok := mgr2.Authenticate("admin", "newpassword123")
	if !ok || !mgr2.ValidateToken(newToken) {
		t.Errorf("New JWT validation failed after password change")
	}

	// Verify users.json is NEVER created (everything in config.yaml)
	if _, err := os.Stat(filepath.Join(tempDir, "users.json")); !os.IsNotExist(err) {
		t.Errorf("users.json should NOT exist, everything must be in config.yaml")
	}
}
