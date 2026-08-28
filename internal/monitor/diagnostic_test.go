package monitor

import (
	"testing"
)

func TestDiagnoseSSHError(t *testing.T) {
	tests := []struct {
		name      string
		rawError  string
		wantCode  string
		wantCat   string
	}{
		{
			name:     "Password Rejected by Server",
			rawError: "ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain",
			wantCode: "AUTH_PASSWORD_FAILED",
			wantCat:  "auth",
		},
		{
			name:     "Permission Denied Publickey",
			rawError: "debug1: Authentications that can continue: publickey,password\nPermission denied (publickey).",
			wantCode: "AUTH_KEY_FAILED",
			wantCat:  "auth",
		},
		{
			name:     "Password Required",
			rawError: "Permission denied (keyboard-interactive), password required",
			wantCode: "AUTH_PASSWORD_REQUIRED",
			wantCat:  "auth",
		},
		{
			name:     "Host Key Verification Failed",
			rawError: "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\nHost key mismatch / verification failed.",
			wantCode: "HOST_KEY_UNTRUSTED",
			wantCat:  "host_key",
		},
		{
			name:     "DNS Resolution Failed",
			rawError: "ssh: Could not resolve hostname non-existent.domain: Name or service not known",
			wantCode: "DNS_RESOLUTION_FAILED",
			wantCat:  "network",
		},
		{
			name:     "Connection Refused",
			rawError: "ssh: connect to host 127.0.0.1 port 2222: Connection refused",
			wantCode: "CONNECTION_REFUSED",
			wantCat:  "network",
		},
		{
			name:     "Port In Use",
			rawError: "bind [127.0.0.1]:8080: Address already in use\nchannel_setup_fwd_listener_tcpip: cannot listen to port: 8080",
			wantCode: "PORT_IN_USE",
			wantCat:  "port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := DiagnoseSSHError(tt.rawError)
			if res.ErrorCode != tt.wantCode {
				t.Errorf("ErrorCode = %v, want %v", res.ErrorCode, tt.wantCode)
			}
			if res.Category != tt.wantCat {
				t.Errorf("Category = %v, want %v", res.Category, tt.wantCat)
			}
			if res.Suggestion == "" {
				t.Errorf("Expected suggestion to be non-empty")
			}
		})
	}
}
