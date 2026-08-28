package sshtunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"
)

// HandleSOCKS5 processes standard SOCKS5 negotiation and forwards traffic via sshClient
func HandleSOCKS5(clientConn net.Conn, sshClient *ssh.Client, metrics *TunnelMetrics) error {
	defer clientConn.Close()

	// 1. Negotiation version and auth methods
	buf := make([]byte, 256)
	if _, err := io.ReadFull(clientConn, buf[:2]); err != nil {
		return err
	}

	version := buf[0]
	if version != 5 {
		return fmt.Errorf("unsupported SOCKS version: %d", version)
	}

	nMethods := int(buf[1])
	if _, err := io.ReadFull(clientConn, buf[:nMethods]); err != nil {
		return err
	}

	// Reply NO_AUTH (0x05, 0x00)
	if _, err := clientConn.Write([]byte{0x05, 0x00}); err != nil {
		return err
	}

	// 2. Read SOCKS5 Request
	if _, err := io.ReadFull(clientConn, buf[:4]); err != nil {
		return err
	}

	cmd := buf[1]
	if cmd != 0x01 { // Only support CONNECT (0x01)
		_, _ = clientConn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // Command not supported
		return errors.New("only CONNECT command is supported")
	}

	addrType := buf[3]
	var targetHost string

	switch addrType {
	case 0x01: // IPv4
		if _, err := io.ReadFull(clientConn, buf[:4]); err != nil {
			return err
		}
		targetHost = net.IP(buf[:4]).String()
	case 0x03: // Domain name
		if _, err := io.ReadFull(clientConn, buf[:1]); err != nil {
			return err
		}
		domainLen := int(buf[0])
		if _, err := io.ReadFull(clientConn, buf[:domainLen]); err != nil {
			return err
		}
		targetHost = string(buf[:domainLen])
	case 0x04: // IPv6
		if _, err := io.ReadFull(clientConn, buf[:16]); err != nil {
			return err
		}
		targetHost = net.IP(buf[:16]).String()
	default:
		_, _ = clientConn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return fmt.Errorf("unsupported address type: %d", addrType)
	}

	// Read Port
	if _, err := io.ReadFull(clientConn, buf[:2]); err != nil {
		return err
	}
	targetPort := binary.BigEndian.Uint16(buf[:2])
	targetAddr := net.JoinHostPort(targetHost, strconv.Itoa(int(targetPort)))

	// Connect to target via SSH client
	remoteConn, err := sshClient.Dial("tcp", targetAddr)
	if err != nil {
		_, _ = clientConn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // Host unreachable
		return fmt.Errorf("failed to dial target via SSH: %w", err)
	}

	// Reply Success
	if _, err := clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = remoteConn.Close()
		return err
	}

	// Pipe bidirectionally
	Pipe(clientConn, remoteConn, metrics)
	return nil
}
