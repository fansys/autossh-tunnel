package sshtunnel

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/oaklight/autossh-tunnel/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// BuildSSHClientConfig creates an ssh.ClientConfig from TunnelConfig
func BuildSSHClientConfig(t *config.TunnelConfig, defaultSSHDir string) (*ssh.ClientConfig, string, error) {
	// Parse user and host
	remoteTarget := strings.TrimSpace(t.RemoteHost)
	var user, host string
	if strings.Contains(remoteTarget, "@") {
		parts := strings.SplitN(remoteTarget, "@", 2)
		user = parts[0]
		host = parts[1]
	} else {
		user = os.Getenv("USER")
		if user == "" {
			user = "root"
		}
		host = remoteTarget
	}

	if t.SSHUser != "" {
		user = t.SSHUser
	}

	// SSH Port
	port := 22
	if t.SSHPort > 0 {
		port = t.SSHPort
	}
	targetAddr := net.JoinHostPort(host, strconv.Itoa(port))

	// Auth methods
	var authMethods []ssh.AuthMethod

	// 1. Password Auth (Highest Priority if password is provided or configured)
	pass := t.Password
	if pass == "" && t.PasswordEnv != "" {
		pass = os.Getenv(t.PasswordEnv)
	}

	if pass != "" {
		authMethods = append(authMethods, ssh.Password(pass))
		authMethods = append(authMethods, ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range questions {
				answers[i] = pass
			}
			return answers, nil
		}))
	} else {
		// 2. Identity File Auth
		if t.IdentityFile != "" {
			keyData, err := os.ReadFile(t.IdentityFile)
			if err == nil {
				signer, err := ssh.ParsePrivateKey(keyData)
				if err == nil {
					authMethods = append(authMethods, ssh.PublicKeys(signer))
				}
			}
		}

		// 3. Fallback: try default ~/.ssh keys if no explicit keys
		if len(authMethods) == 0 && defaultSSHDir != "" {
			defaultKeyNames := []string{"id_ed25519", "id_rsa", "id_ecdsa"}
			for _, keyName := range defaultKeyNames {
				keyPath := filepath.Join(defaultSSHDir, keyName)
				if keyData, err := os.ReadFile(keyPath); err == nil {
					if signer, err := ssh.ParsePrivateKey(keyData); err == nil {
						authMethods = append(authMethods, ssh.PublicKeys(signer))
					}
				}
			}
		}
	}

	if len(authMethods) == 0 {
		return nil, "", fmt.Errorf("no valid authentication method available (provide a password or SSH key)")
	}

	// Host key callback
	var hostKeyCallback ssh.HostKeyCallback
	switch t.StrictHostKeyChecking {
	case "yes":
		knownHostsFile := filepath.Join(defaultSSHDir, "known_hosts")
		if kh, err := knownhosts.New(knownHostsFile); err == nil {
			hostKeyCallback = kh
		} else {
			hostKeyCallback = ssh.InsecureIgnoreHostKey()
		}
	case "no", "accept-new", "":
		fallthrough
	default:
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	timeout := 10 * time.Second
	if t.ConnectTimeout > 0 {
		timeout = time.Duration(t.ConnectTimeout) * time.Second
	}

	clientConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}

	return clientConfig, targetAddr, nil
}

// DialSSH connects to the target SSH server, supporting ProxyJump if configured
func DialSSH(ctx context.Context, t *config.TunnelConfig, defaultSSHDir string) (*ssh.Client, error) {
	clientConfig, targetAddr, err := BuildSSHClientConfig(t, defaultSSHDir)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: clientConfig.Timeout}

	// Direct connection
	if t.ProxyJump == "" {
		conn, err := dialer.DialContext(ctx, "tcp", targetAddr)
		if err != nil {
			return nil, err
		}
		c, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, clientConfig)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return ssh.NewClient(c, chans, reqs), nil
	}

	// ProxyJump connection
	jumpTunnel := &config.TunnelConfig{
		RemoteHost:            t.ProxyJump,
		IdentityFile:          t.IdentityFile,
		Password:              t.Password,
		PasswordEnv:           t.PasswordEnv,
		StrictHostKeyChecking: t.StrictHostKeyChecking,
		ConnectTimeout:        t.ConnectTimeout,
	}

	jumpConfig, jumpAddr, err := BuildSSHClientConfig(jumpTunnel, defaultSSHDir)
	if err != nil {
		return nil, fmt.Errorf("failed to build jump host config: %w", err)
	}

	jumpDialer := &net.Dialer{Timeout: jumpConfig.Timeout}
	jumpConn, err := jumpDialer.DialContext(ctx, "tcp", jumpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial jump host (%s): %w", jumpAddr, err)
	}

	jumpClientConn, jumpChans, jumpReqs, err := ssh.NewClientConn(jumpConn, jumpAddr, jumpConfig)
	if err != nil {
		_ = jumpConn.Close()
		return nil, fmt.Errorf("failed to handshake jump host (%s): %w", jumpAddr, err)
	}
	jumpClient := ssh.NewClient(jumpClientConn, jumpChans, jumpReqs)

	// Connect to target host via jump client
	targetConn, err := jumpClient.Dial("tcp", targetAddr)
	if err != nil {
		_ = jumpClient.Close()
		return nil, fmt.Errorf("jump host failed to dial target (%s): %w", targetAddr, err)
	}

	c, chans, reqs, err := ssh.NewClientConn(targetConn, targetAddr, clientConfig)
	if err != nil {
		_ = targetConn.Close()
		_ = jumpClient.Close()
		return nil, err
	}

	client := ssh.NewClient(c, chans, reqs)

	// Automatically close jumpClient when target client disconnects
	go func() {
		_ = client.Wait()
		_ = jumpClient.Close()
	}()

	return client, nil
}

// StartKeepAlive starts a background loop sending application-level SSH keepalive requests
func StartKeepAlive(ctx context.Context, client *ssh.Client, interval time.Duration, onFail func()) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				if err != nil {
					if onFail != nil {
						onFail()
					}
					return
				}
			}
		}
	}()
}
