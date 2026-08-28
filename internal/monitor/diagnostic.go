package monitor

import (
	"net"
	"strings"
	"time"
)

// DiagnosticResult contains parsed error diagnosis and remediation advice
type DiagnosticResult struct {
	ErrorCode    string `json:"error_code"`
	Category     string `json:"category"` // "auth", "network", "host_key", "port", "config"
	Title        string `json:"title"`
	Description  string `json:"description"`
	Suggestion   string `json:"suggestion"`
	RawLog       string `json:"raw_log,omitempty"`
	Success      bool   `json:"success"`
	LatencyMs    int64  `json:"latency_ms,omitempty"`
}

// DiagnoseSSHError analyzes SSH error output and returns structured diagnostics
func DiagnoseSSHError(rawError string) DiagnosticResult {
	lower := strings.ToLower(rawError)

	// 1. Specific: Password Auth Attempted and Rejected by Server
	if strings.Contains(lower, "attempted methods [none password") ||
		strings.Contains(lower, "attempted methods [none keyboard-interactive") {
		return DiagnosticResult{
			ErrorCode:   "AUTH_PASSWORD_FAILED",
			Category:    "auth",
			Title:       "SSH 密码认证失败 (Password Authentication Failed)",
			Description: "目标服务器收到了提交的密码，但拒绝了登录请求。可能原因：密码错误、用户名不存在，或服务器禁止该用户密码登录。",
			Suggestion:  "1. 请确认密码是否填写正确；2. 检查远程主机格式是否包含用户名 (例如: username@host)；3. 若使用 root 登录，很多 Linux 服务器默认禁止 root 密码登录 (PermitRootLogin)，建议使用普通用户或改用 SSH 密钥认证。",
			RawLog:      rawError,
		}
	}

	// 2. Specific: Publickey Permission Denied
	if strings.Contains(lower, "attempted methods [none publickey") ||
		strings.Contains(lower, "permission denied (publickey") {
		return DiagnosticResult{
			ErrorCode:   "AUTH_KEY_FAILED",
			Category:    "auth",
			Title:       "SSH 密钥认证失败 (Publickey Authentication Failed)",
			Description: "远程服务器拒绝了提供的 SSH 密钥，私钥不匹配或公钥未添加至目标服务器的 authorized_keys 中。",
			Suggestion:  "请确认在配置中粘贴了正确的私钥内容，并确保目标主机的 ~/.ssh/authorized_keys 中已添加对应公钥。",
			RawLog:      rawError,
		}
	}

	// 3. Generic: Auth Failed / No valid method
	if strings.Contains(lower, "unable to authenticate") ||
		strings.Contains(lower, "no valid authentication method") {
		return DiagnosticResult{
			ErrorCode:   "AUTH_FAILED",
			Category:    "auth",
			Title:       "SSH 凭证认证失败 (Authentication Failed)",
			Description: "远程服务器拒绝了认证请求，未找到匹配的认证方法或提供的密码/密钥无效。",
			Suggestion:  "请检查远程主机用户名是否填写正确 (格式: user@host)，并确认密码或私钥有效。",
			RawLog:      rawError,
		}
	}

	// 4. Password / Interactive Required
	if strings.Contains(lower, "password required") ||
		strings.Contains(lower, "password:") {
		return DiagnosticResult{
			ErrorCode:   "AUTH_PASSWORD_REQUIRED",
			Category:    "auth",
			Title:       "需要密码认证 (Password Authentication Required)",
			Description: "远程服务器要求输入密码或 2FA 一次性验证码，但当前未提供有效密码或未开启交互式认证。",
			Suggestion:  "请在页面配置中填写 SSH 密码，或将认证模式切换为交互式并在 Web 终端中完成验证。",
			RawLog:      rawError,
		}
	}

	// 5. Host Key Verification Failed
	if strings.Contains(lower, "host key") && (strings.Contains(lower, "mismatch") || strings.Contains(lower, "verification failed")) {
		return DiagnosticResult{
			ErrorCode:   "HOST_KEY_UNTRUSTED",
			Category:    "host_key",
			Title:       "主机公钥指纹未受信 (Host Key Verification Failed)",
			Description: "目标服务器的主机公钥未在 known_hosts 文件中，或目标服务器重装系统后公钥发生变化。",
			Suggestion:  "可在高级选项中将 StrictHostKeyChecking 设置为 accept-new 或 no，或者更新 known_hosts 文件。",
			RawLog:      rawError,
		}
	}

	// 6. Host resolution failed
	if strings.Contains(lower, "no such host") || strings.Contains(lower, "could not resolve hostname") || strings.Contains(lower, "name or service not known") {
		return DiagnosticResult{
			ErrorCode:   "DNS_RESOLUTION_FAILED",
			Category:    "network",
			Title:       "远程主机名无法解析 (DNS Lookup Failed)",
			Description: "系统无法将远程主机名/域名解析为 IP 地址。",
			Suggestion:  "请检查远程服务器的主机名拼写是否正确，或者直接使用 IP 地址连接，并确保网络 DNS 正常。",
			RawLog:      rawError,
		}
	}

	// 7. Connection Refused
	if strings.Contains(lower, "connection refused") {
		return DiagnosticResult{
			ErrorCode:   "CONNECTION_REFUSED",
			Category:    "network",
			Title:       "连接被拒绝 (Connection Refused)",
			Description: "目标服务器拒绝了 SSH TCP 连接请求。通常是 SSH 服务未运行或端口填写错误。",
			Suggestion:  "请检查远程主机的 SSH 端口（默认 22）是否正确，并确认目标主机 SSH 服务正在监听该端口。",
			RawLog:      rawError,
		}
	}

	// 8. Connection Timed Out / Network Unreachable
	if strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "connection timed out") || strings.Contains(lower, "network is unreachable") {
		return DiagnosticResult{
			ErrorCode:   "CONNECTION_TIMEOUT",
			Category:    "network",
			Title:       "连接超时 / 网络不可达 (Connection Timed Out)",
			Description: "未能与目标服务器建立 TCP 连接。可能是由于防火墙阻断、安全组未放行或网络路由不可达。",
			Suggestion:  "请检查目标服务器的网络防火墙、云厂商安全组端口规则，或在高级选项中配置 ProxyJump 跳板机。",
			RawLog:      rawError,
		}
	}

	// 9. Port Binding Conflict
	if strings.Contains(lower, "address already in use") || strings.Contains(lower, "cannot listen to port") {
		return DiagnosticResult{
			ErrorCode:   "PORT_IN_USE",
			Category:    "port",
			Title:       "端口已被占用 (Port Binding Conflict)",
			Description: "本地或远程绑定的转发端口已被宿主机或其他服务占用。",
			Suggestion:  "请更换本地或远程端口，或停止占用该端口的其它进程。",
			RawLog:      rawError,
		}
	}

	// General / Unknown error
	return DiagnosticResult{
		ErrorCode:   "UNKNOWN_ERROR",
		Category:    "general",
		Title:       "SSH 连接异常",
		Description: "SSH 连接建立失败，具体错误信息请参考原始日志。",
		Suggestion:  "请查看下方原始日志，检查 SSH 配置、网络连通性及权限。",
		RawLog:      rawError,
	}
}

// ProbeTCPPort attempts a TCP dial to a local or remote address with a short timeout
func ProbeTCPPort(address string, timeout time.Duration) (bool, int64) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false, 0
	}
	_ = conn.Close()
	return true, time.Since(start).Milliseconds()
}

// NormalizePortAddress converts "8080" or "127.0.0.1:8080" to a valid "host:port" dial target
func NormalizePortAddress(portStr string, defaultHost string) string {
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		return ""
	}
	if strings.Contains(portStr, ":") {
		return portStr
	}
	return net.JoinHostPort(defaultHost, portStr)
}
