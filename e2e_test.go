package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oaklight/autossh-tunnel/internal/api"
	"github.com/oaklight/autossh-tunnel/internal/auth"
	"github.com/oaklight/autossh-tunnel/internal/config"
	"github.com/oaklight/autossh-tunnel/internal/monitor"
	"github.com/oaklight/autossh-tunnel/internal/sshtunnel"
	"github.com/oaklight/autossh-tunnel/internal/terminal"
	"golang.org/x/crypto/ssh"
)

// MockSSHServer represents an in-process real SSH server for end-to-end testing
type MockSSHServer struct {
	listener net.Listener
	config   *ssh.ServerConfig
	addr     string
	stopChan chan struct{}
}

func startMockSSHServer(t *testing.T, expectedUser, expectedPass string, authorizedPublicKey ssh.PublicKey) *MockSSHServer {
	// Generate host key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("Failed to create host signer: %v", err)
	}

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == expectedUser && string(password) == expectedPass {
				return nil, nil
			}
			return nil, fmt.Errorf("invalid password for %s", conn.User())
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if authorizedPublicKey != nil && conn.User() == expectedUser {
				if string(key.Marshal()) == string(authorizedPublicKey.Marshal()) {
					return nil, nil
				}
			}
			return nil, fmt.Errorf("unknown public key for %s", conn.User())
		},
		KeyboardInteractiveCallback: func(conn ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := client("", "", []string{"Password: "}, []bool{false})
			if err == nil && len(answers) == 1 && answers[0] == expectedPass && conn.User() == expectedUser {
				return nil, nil
			}
			return nil, fmt.Errorf("interactive auth failed")
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen for mock SSH server: %v", err)
	}

	mock := &MockSSHServer{
		listener: listener,
		config:   serverConfig,
		addr:     listener.Addr().String(),
		stopChan: make(chan struct{}),
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go mock.handleConn(conn)
		}
	}()

	return mock
}

func (s *MockSSHServer) handleConn(netConn net.Conn) {
	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, s.config)
	if err != nil {
		_ = netConn.Close()
		return
	}
	defer sshConn.Close()

	// Handle global requests (such as tcpip-forward for -R)
	var remoteListenersMu sync.Mutex
	remoteListeners := make(map[string]net.Listener)
	defer func() {
		remoteListenersMu.Lock()
		for _, l := range remoteListeners {
			_ = l.Close()
		}
		remoteListenersMu.Unlock()
	}()

	go func() {
		for req := range reqs {
			switch req.Type {
			case "tcpip-forward":
				var fwd struct {
					BindAddr string
					BindPort uint32
				}
				if err := ssh.Unmarshal(req.Payload, &fwd); err != nil {
					_ = req.Reply(false, nil)
					continue
				}

				bindAddr := fmt.Sprintf("%s:%d", fwd.BindAddr, fwd.BindPort)
				if fwd.BindAddr == "" || fwd.BindAddr == "0.0.0.0" {
					bindAddr = fmt.Sprintf("127.0.0.1:%d", fwd.BindPort)
				}

				listener, err := net.Listen("tcp", bindAddr)
				if err != nil {
					_ = req.Reply(false, nil)
					continue
				}

				_, actualPortStr, _ := net.SplitHostPort(listener.Addr().String())
				var actualPort uint32
				_, _ = fmt.Sscanf(actualPortStr, "%d", &actualPort)

				remoteListenersMu.Lock()
				remoteListeners[bindAddr] = listener
				remoteListenersMu.Unlock()

				_ = req.Reply(true, ssh.Marshal(struct{ Port uint32 }{actualPort}))

				// Accept forwarded connections and push to SSH client
				go func(l net.Listener, port uint32) {
					defer l.Close()
					for {
						rConn, err := l.Accept()
						if err != nil {
							return
						}
						go func(c net.Conn) {
							defer c.Close()
							origAddr, origPortStr, _ := net.SplitHostPort(c.RemoteAddr().String())
							var origPort uint32
							_, _ = fmt.Sscanf(origPortStr, "%d", &origPort)

							channel, reqs, err := sshConn.OpenChannel("forwarded-tcpip", ssh.Marshal(struct {
								Addr       string
								Port       uint32
								OriginAddr string
								OriginPort uint32
							}{
								Addr:       "0.0.0.0",
								Port:       port,
								OriginAddr: origAddr,
								OriginPort: origPort,
							}))
							if err != nil {
								return
							}
							defer channel.Close()
							go ssh.DiscardRequests(reqs)

							done := make(chan struct{}, 2)
							go func() {
								_, _ = io.Copy(channel, c)
								done <- struct{}{}
							}()
							go func() {
								_, _ = io.Copy(c, channel)
								done <- struct{}{}
							}()
							<-done
						}(rConn)
					}
				}(listener, actualPort)

			case "cancel-tcpip-forward":
				var fwd struct {
					BindAddr string
					BindPort uint32
				}
				_ = ssh.Unmarshal(req.Payload, &fwd)
				bindAddr := fmt.Sprintf("%s:%d", fwd.BindAddr, fwd.BindPort)
				remoteListenersMu.Lock()
				if l, ok := remoteListeners[bindAddr]; ok {
					_ = l.Close()
					delete(remoteListeners, bindAddr)
				}
				remoteListenersMu.Unlock()
				_ = req.Reply(true, nil)

			case "keepalive@openssh.com":
				_ = req.Reply(true, nil)
			default:
				_ = req.Reply(false, nil)
			}
		}
	}()

	// Handle channels (such as direct-tcpip for -L or SOCKS5)
	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "direct-tcpip":
			var d struct {
				DestAddr   string
				DestPort   uint32
				OriginAddr string
				OriginPort uint32
			}
			if err := ssh.Unmarshal(newChannel.ExtraData(), &d); err != nil {
				_ = newChannel.Reject(ssh.ConnectionFailed, "invalid payload")
				continue
			}

			destTarget := fmt.Sprintf("%s:%d", d.DestAddr, d.DestPort)
			destConn, err := net.DialTimeout("tcp", destTarget, 5*time.Second)
			if err != nil {
				_ = newChannel.Reject(ssh.ConnectionFailed, fmt.Sprintf("failed to dial target: %v", err))
				continue
			}

			ch, reqs, err := newChannel.Accept()
			if err != nil {
				_ = destConn.Close()
				continue
			}
			go ssh.DiscardRequests(reqs)

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				_, _ = io.Copy(ch, destConn)
				_ = ch.Close()
				wg.Done()
			}()
			go func() {
				_, _ = io.Copy(destConn, ch)
				_ = destConn.Close()
				wg.Done()
			}()
			wg.Wait()

		default:
			_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
		}
	}
}

func (s *MockSSHServer) Close() {
	_ = s.listener.Close()
}

// GenerateTestKeyPair creates an in-memory RSA private key and OpenSSH public key
func GenerateTestKeyPair(t *testing.T) (privateKeyPEM string, sshPubKey ssh.PublicKey) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	privDER := x509.MarshalPKCS1PrivateKey(key)
	privBlock := pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privDER,
	}
	privateKeyPEM = string(pem.EncodeToMemory(&privBlock))

	pub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("Failed to create SSH public key: %v", err)
	}

	return privateKeyPEM, pub
}

func TestCompleteEndToEndSuite(t *testing.T) {
	// 1. Start a Mock HTTP Backend Target
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("E2E_HTTP_BACKEND_RESPONSE"))
	}))
	defer backendServer.Close()

	_, backendPortStr, _ := net.SplitHostPort(backendServer.Listener.Addr().String())

	// 2. Generate RSA Key Pair for Public Key Authentication
	privKeyPEM, pubKey := GenerateTestKeyPair(t)

	// 3. Start Mock SSH Server with both password and publickey support
	testSSHUser := "testuser"
	testSSHPass := "SecretPass888!"
	mockSSH := startMockSSHServer(t, testSSHUser, testSSHPass, pubKey)
	defer mockSSH.Close()

	_, mockSSHPortStr, _ := net.SplitHostPort(mockSSH.addr)
	var mockSSHPort int
	_, _ = fmt.Sscanf(mockSSHPortStr, "%d", &mockSSHPort)

	// 4. Setup Isolated Config Environment
	tempDir, err := os.MkdirTemp("", "e2e_suite_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.yaml")
	logDir := filepath.Join(tempDir, "logs")

	// Set Environment Variables for Authentication
	_ = os.Setenv("USERNAME", "superadmin")
	_ = os.Setenv("PASSWORD", "SuperSecurePassword123!")

	// 5. Initialize System Services
	cfgMgr, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("Failed to create config.Manager: %v", err)
	}

	authMgr := auth.NewManager(cfgMgr, "test-api-key-master")
	tunnelMgr := sshtunnel.NewManager(cfgMgr, logDir, tempDir)
	tunnelMgr.StartSupervisor()
	defer tunnelMgr.StopAll()

	termMgr := terminal.NewSessionManager(cfgMgr)
	apiHandler := api.NewHandler(authMgr, cfgMgr, tunnelMgr, termMgr, "web/static", tempDir, "test")

	// Verify Admin Credentials initialized in config.yaml
	u, _ := cfgMgr.GetAdminCredentials()
	if u != "superadmin" {
		t.Errorf("Expected username 'superadmin', got '%s'", u)
	}

	// -------------------------------------------------------------
	// SCENARIO 1: JWT Authentication & Login API
	// -------------------------------------------------------------
	t.Run("Scenario 1: Authentication & JWT", func(t *testing.T) {
		token, ok := authMgr.Authenticate("superadmin", "SuperSecurePassword123!")
		if !ok || token == "" {
			t.Fatalf("Authentication failed")
		}

		if !authMgr.ValidateToken(token) {
			t.Errorf("ValidateToken failed for valid JWT")
		}

		// API Key validation
		if !authMgr.ValidateToken("test-api-key-master") {
			t.Errorf("ValidateToken failed for pre-configured API Key")
		}

		// Invalid credentials
		if _, ok := authMgr.Authenticate("superadmin", "wrongpassword"); ok {
			t.Errorf("Authenticate should fail for wrong password")
		}
	})

	// -------------------------------------------------------------
	// SCENARIO 2: Remote-to-Local (-L) with Password Authentication
	// -------------------------------------------------------------
	t.Run("Scenario 2: Remote-to-Local (-L) with Password", func(t *testing.T) {
		localPort := "18201"
		tunnel := &config.TunnelConfig{
			Name:        "remote-to-local-pwd",
			RemoteHost:  fmt.Sprintf("%s@127.0.0.1", testSSHUser),
			SSHPort:     mockSSHPort,
			RemotePort:  backendPortStr,
			LocalPort:   localPort,
			Direction:   config.DirectionRemoteToLocal,
			AuthType:    config.AuthTypePassword,
			Password:    testSSHPass,
			StrictHostKeyChecking: "no",
		}

		if err := cfgMgr.ProcessAndSaveTunnel(tunnel); err != nil {
			t.Fatalf("ProcessAndSaveTunnel failed: %v", err)
		}
		tunnelMgr.SyncWithConfig()

		hash := tunnel.CalculateHash()

		// Wait up to 3 seconds for tunnel to become active
		var rt *sshtunnel.TunnelRuntime
		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			rt = tunnelMgr.GetRuntime(hash)
			if rt != nil && rt.Status == sshtunnel.StatusActive {
				break
			}
		}

		if rt == nil || rt.Status != sshtunnel.StatusActive {
			t.Fatalf("Tunnel failed to become active, status: %v", rt)
		}

		// Verify HTTP forwarding through local port 18201
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s", localPort))
		if err != nil {
			t.Fatalf("Failed to request local forwarded port: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if string(body) != "E2E_HTTP_BACKEND_RESPONSE" {
			t.Errorf("Expected 'E2E_HTTP_BACKEND_RESPONSE', got '%s'", string(body))
		}

		// Verify Traffic Metrics
		rtAfter := tunnelMgr.GetRuntime(hash)
		if rtAfter.Metrics.BytesRx == 0 && rtAfter.Metrics.BytesTx == 0 {
			t.Errorf("Expected traffic metrics to be counted, got Rx=%d, Tx=%d",
				rtAfter.Metrics.BytesRx, rtAfter.Metrics.BytesTx)
		}
	})

	// -------------------------------------------------------------
	// SCENARIO 3: Remote-to-Local (-L) with Private Key (Write-Only)
	// -------------------------------------------------------------
	t.Run("Scenario 3: Remote-to-Local (-L) with Private Key", func(t *testing.T) {
		localPort := "18202"
		tunnel := &config.TunnelConfig{
			Name:              "remote-to-local-key",
			RemoteHost:        fmt.Sprintf("%s@127.0.0.1", testSSHUser),
			SSHPort:           mockSSHPort,
			RemotePort:        backendPortStr,
			LocalPort:         localPort,
			Direction:         config.DirectionRemoteToLocal,
			AuthType:          config.AuthTypeKey,
			PrivateKeyContent: privKeyPEM,
			StrictHostKeyChecking: "no",
		}

		if err := cfgMgr.ProcessAndSaveTunnel(tunnel); err != nil {
			t.Fatalf("ProcessAndSaveTunnel failed: %v", err)
		}
		tunnelMgr.SyncWithConfig()

		hash := tunnel.CalculateHash()

		// Verify key file落盘
		if tunnel.IdentityFile == "" {
			t.Fatalf("IdentityFile was not set")
		}
		if _, err := os.Stat(tunnel.IdentityFile); os.IsNotExist(err) {
			t.Fatalf("Key file was not created at %s", tunnel.IdentityFile)
		}

		// Wait for active
		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			rt := tunnelMgr.GetRuntime(hash)
			if rt != nil && rt.Status == sshtunnel.StatusActive {
				break
			}
		}

		// Request through local port 18202
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s", localPort))
		if err != nil {
			t.Fatalf("Failed to request local forwarded port: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if string(body) != "E2E_HTTP_BACKEND_RESPONSE" {
			t.Errorf("Expected 'E2E_HTTP_BACKEND_RESPONSE', got '%s'", string(body))
		}

		// Security Check: GetConfig must NEVER leak private key or password
		sanitizedCfg := cfgMgr.GetConfig()
		for _, tun := range sanitizedCfg.Tunnels {
			if tun.PrivateKeyContent != "" {
				t.Errorf("Security Leak: PrivateKeyContent exposed in GetConfig()")
			}
			if tun.Password != "" {
				t.Errorf("Security Leak: Password exposed in GetConfig()")
			}
		}
	})

	// -------------------------------------------------------------
	// SCENARIO 4: Local-to-Remote (-R) Port Forwarding
	// -------------------------------------------------------------
	t.Run("Scenario 4: Local-to-Remote (-R)", func(t *testing.T) {
		remoteBindPort := "19203"
		tunnel := &config.TunnelConfig{
			Name:        "local-to-remote-test",
			RemoteHost:  fmt.Sprintf("%s@127.0.0.1", testSSHUser),
			SSHPort:     mockSSHPort,
			RemotePort:  remoteBindPort,
			LocalPort:   backendPortStr,
			Direction:   config.DirectionLocalToRemote,
			AuthType:    config.AuthTypePassword,
			Password:    testSSHPass,
			StrictHostKeyChecking: "no",
		}

		if err := cfgMgr.ProcessAndSaveTunnel(tunnel); err != nil {
			t.Fatalf("ProcessAndSaveTunnel failed: %v", err)
		}
		tunnelMgr.SyncWithConfig()

		hash := tunnel.CalculateHash()

		// Wait for active
		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			rt := tunnelMgr.GetRuntime(hash)
			if rt != nil && rt.Status == sshtunnel.StatusActive {
				break
			}
		}

		// Test connecting to the remote port on Mock SSH Server
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s", remoteBindPort))
		if err != nil {
			t.Fatalf("Failed to request remote bound port: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if string(body) != "E2E_HTTP_BACKEND_RESPONSE" {
			t.Errorf("Expected 'E2E_HTTP_BACKEND_RESPONSE', got '%s'", string(body))
		}
	})

	// -------------------------------------------------------------
	// SCENARIO 5: Dynamic SOCKS5 Proxy (-D)
	// -------------------------------------------------------------
	t.Run("Scenario 5: Dynamic SOCKS5 Proxy (-D)", func(t *testing.T) {
		socksPort := "18205"
		tunnel := &config.TunnelConfig{
			Name:        "socks5-proxy-test",
			RemoteHost:  fmt.Sprintf("%s@127.0.0.1", testSSHUser),
			SSHPort:     mockSSHPort,
			LocalPort:   socksPort,
			Direction:   config.DirectionDynamicSocks5,
			AuthType:    config.AuthTypePassword,
			Password:    testSSHPass,
			StrictHostKeyChecking: "no",
		}

		if err := cfgMgr.ProcessAndSaveTunnel(tunnel); err != nil {
			t.Fatalf("ProcessAndSaveTunnel failed: %v", err)
		}
		tunnelMgr.SyncWithConfig()

		hash := tunnel.CalculateHash()

		// Wait for active
		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			rt := tunnelMgr.GetRuntime(hash)
			if rt != nil && rt.Status == sshtunnel.StatusActive {
				break
			}
		}

		// Dial via SOCKS5 client
		socksAddr := fmt.Sprintf("127.0.0.1:%s", socksPort)
		clientConn, err := net.DialTimeout("tcp", socksAddr, 2*time.Second)
		if err != nil {
			t.Fatalf("Failed to connect to SOCKS5 server: %v", err)
		}
		defer clientConn.Close()

		// SOCKS5 Handshake: NO_AUTH
		_, _ = clientConn.Write([]byte{0x05, 0x01, 0x00})
		handshakeResp := make([]byte, 2)
		if _, err := io.ReadFull(clientConn, handshakeResp); err != nil || handshakeResp[1] != 0x00 {
			t.Fatalf("SOCKS5 handshake failed: %v, resp: %v", err, handshakeResp)
		}

		// SOCKS5 Connect to target backend
		var bPort uint16
		_, _ = fmt.Sscanf(backendPortStr, "%d", &bPort)
		connectReq := []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, byte(bPort >> 8), byte(bPort & 0xff)}
		_, _ = clientConn.Write(connectReq)

		connectResp := make([]byte, 10)
		if _, err := io.ReadFull(clientConn, connectResp); err != nil || connectResp[1] != 0x00 {
			t.Fatalf("SOCKS5 connect request failed: %v, resp: %v", err, connectResp)
		}

		// Send HTTP GET through SOCKS5 tunnel
		_, _ = clientConn.Write([]byte("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"))
		httpRespData, err := io.ReadAll(clientConn)
		if err != nil || !strings.Contains(string(httpRespData), "E2E_HTTP_BACKEND_RESPONSE") {
			t.Errorf("Failed to receive HTTP response through SOCKS5: %v, output: %s", err, string(httpRespData))
		}
	})

	// -------------------------------------------------------------
	// SCENARIO 6: Test Connection API & Error Diagnostics
	// -------------------------------------------------------------
	t.Run("Scenario 6: Test Connection & Diagnostics API", func(t *testing.T) {
		// 1. Success test
		validTunnel := &config.TunnelConfig{
			RemoteHost: fmt.Sprintf("%s@127.0.0.1", testSSHUser),
			SSHPort:    mockSSHPort,
			Password:   testSSHPass,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client, err := sshtunnel.DialSSH(ctx, validTunnel, "")
		if err != nil {
			t.Fatalf("Expected successful test connection, got: %v", err)
		}
		_ = client.Close()

		// 2. Failure test: Wrong Password
		invalidTunnel := &config.TunnelConfig{
			RemoteHost: fmt.Sprintf("%s@127.0.0.1", testSSHUser),
			SSHPort:    mockSSHPort,
			Password:   "WrongSecret!",
		}
		_, errWrongPass := sshtunnel.DialSSH(ctx, invalidTunnel, "")
		if errWrongPass == nil {
			t.Fatalf("Expected auth error for wrong password, got nil")
		}
		diag := monitor.DiagnoseSSHError(errWrongPass.Error())
		if diag.Category != "auth" {
			t.Errorf("Expected category 'auth', got '%s'", diag.Category)
		}
	})

	// -------------------------------------------------------------
	// SCENARIO 7: Batch Stop All & Start All Controls
	// -------------------------------------------------------------
	t.Run("Scenario 7: Batch Control Stop-All & Start-All", func(t *testing.T) {
		// Stop all tunnels
		tunnelMgr.StopAll()
		_ = cfgMgr.SetAllTunnelsEnabled(false)

		// Verify all runtimes are in stopped status
		runtimes := tunnelMgr.GetAllRuntimes()
		for _, rt := range runtimes {
			if rt.Status != sshtunnel.StatusStopped {
				t.Errorf("Expected tunnel %s to be stopped, got %s", rt.Hash, rt.Status)
			}
		}

		// Start all tunnels
		_ = cfgMgr.SetAllTunnelsEnabled(true)
		tunnelMgr.StartAll()

		// Verify tunnels re-activate without address already in use error
		time.Sleep(800 * time.Millisecond)
		activeCount := 0
		for _, rt := range tunnelMgr.GetAllRuntimes() {
			if rt.Status == sshtunnel.StatusActive {
				activeCount++
			}
		}
		if activeCount == 0 {
			t.Errorf("Expected tunnels to be active after start-all")
		}
	})

	// -------------------------------------------------------------
	// SCENARIO 8: YAML Direct Online Read & Write
	// -------------------------------------------------------------
	t.Run("Scenario 8: Raw YAML Read and Write", func(t *testing.T) {
		yamlData, err := cfgMgr.GetRawYAML()
		if err != nil || !strings.Contains(yamlData, "remote-to-local-pwd") {
			t.Fatalf("GetRawYAML failed or missing content: %v", err)
		}

		// Update YAML
		newYAML := yamlData + "\n# Test Comment Append\n"
		if err := cfgMgr.SaveRawYAML(newYAML); err != nil {
			t.Fatalf("SaveRawYAML failed: %v", err)
		}

		updatedYAML, _ := cfgMgr.GetRawYAML()
		if !strings.Contains(updatedYAML, "Test Comment Append") {
			t.Errorf("Saved YAML comment not found")
		}
	})

	_ = apiHandler
}
