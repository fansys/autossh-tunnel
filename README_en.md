# SSH Tunnel Manager

[![GitHub Container Registry](https://img.shields.io/badge/docker-ghcr.io%2Ffansys%2Fautossh--tunnel-blue?logo=docker)](https://github.com/fansys/autossh-tunnel/pkgs/container/autossh-tunnel)
[![Go Report Card](https://img.shields.io/badge/go-1.25-blue?logo=go)](https://golang.org)
[![Tailwind CSS](https://img.shields.io/badge/Tailwind_CSS-3.x-38B2AC?logo=tailwind-css)](https://tailwindcss.com)

[中文版](README_zh.md) | [English](README_en.md)

A modern, all-in-one SSH tunnel management system built with pure Go native SSH libraries and Tailwind CSS. Easily configure **Remote-to-Local (`-L`)**, **Local-to-Remote (`-R`)**, and **Dynamic SOCKS5 Proxy (`-D`)** tunnels via a declarative YAML file or a visual Web Console.

---

## 🌟 Key Features

- **All-in-One Single Container Architecture**: Web Console, REST API, WebSocket Terminal, and SSH tunnel engine consolidated into a single lightweight image.
- **Pure Go Native SSH Engine**: Powered by `golang.org/x/crypto/ssh` — **zero dependencies on autossh, sshpass, or openssh binaries**, with minimal memory overhead per tunnel.
- **Comprehensive Web Configuration Panel**: A sleek Slate/Light dashboard built with Tailwind CSS, supporting full configuration options (ports, keepalive, timeout, host key policies, ProxyJump, etc.).
- **Versatile Authentication & Credential Security**:
  - **Password Auth**: Native password login and environment variable referencing;
  - **Write-Only Private Key Handling**: Paste private key text on the page to automatically store it securely (`0600` permissions), with **zero plaintext leakage in APIs or UI**;
  - **2FA / Keyboard-Interactive Auth**: Built-in WebSocket terminal for manual one-time passcodes.
- **Pre-flight Connection Testing & Smart Diagnostics**:
  - Test connectivity in real-time before saving;
  - Automatically identifies mismatched keys, password requirements, untrusted host keys, DNS failures, or port conflicts with actionable remediation suggestions.
- **Multi-dimensional Monitoring & Real-time Metrics**:
  - Live tracking of **RTT latency**, **transmitted/received data throughput (Tx/Rx)**, and **active connections**.
- **Exponential Backoff Auto-Recovery**:
  - Gracefully reconnects using exponential backoff on network interruptions;
  - Accurately remembers running state: **only auto-starts tunnels that were active before container restart**.
- **Stateless Persistent JWT Authentication**:
  - Admin login with password modification support;
  - Persistent JWT secret ensures **no re-login required across container restarts**.
- **Instant Language Toggle**: Seamless one-click toggle between English and Simplified Chinese with offline static assets.

---

## 🚀 Quick Start

### 1. Using Docker Compose (Recommended)

Create a `docker-compose.yml` file:

```yaml
services:
  autossh:
    image: ghcr.io/fansys/autossh-tunnel:latest
    container_name: autossh-tunnel
    volumes:
      - ./config:/etc/autossh:rw
    environment:
      - TZ=Asia/Shanghai
      - PORT=8080
      # Initial Admin credentials (Default: admin / admin888)
      - USERNAME=admin
      - PASSWORD=admin888
      # Optional: API Key for programmatic REST API access
      # - API_KEY=your-secret-api-key
    network_mode: "host"
    restart: always
```

### 2. Start the Service

```bash
mkdir -p config
docker compose up -d
```

Open your browser and visit `http://<YOUR_IP>:8080`, then log in with the admin credentials!

---

## 📖 Configuration Example (`config/config.yaml`)

Configure tunnels visually in the Web Console or edit `config/config.yaml` directly:

```yaml
tunnels:
  # Example 1: Basic SSH Key tunnel (Remote mapped to Local -L)
  - name: "web-service"
    remote_host: "user@remote-host1.com"
    remote_port: "8000"
    local_port: "8001"
    direction: remote_to_local
    enabled: true

  # Example 2: Password-authenticated tunnel (Native Go SSH auto-reconnect)
  - name: "database-tunnel"
    remote_host: "root@remote-db.example.com"
    remote_port: "3306"
    local_port: "13306"
    auth_type: password
    password: "YourSecurePassword" # or use password_env: "REMOTE_DB_PASS"

  # Example 3: Dynamic SOCKS5 proxy tunnel (-D local dynamic proxy)
  - name: "dynamic-socks5-proxy"
    remote_host: "user@gateway.example.com"
    local_port: "1080"
    direction: dynamic_socks5

  # Example 4: Interactive 2FA tunnel (Enter one-time passcode via Web Terminal)
  - name: "jumphost-2fa-tunnel"
    remote_host: "user@jumphost.example.com"
    remote_port: "22"
    local_port: "2222"
    direction: remote_to_local
    interactive: true

  # Example 5: Advanced options (Custom SSH options, keepalive, jump host, and backoff)
  # - name: "advanced-production-tunnel"
  #   remote_host: "deploy@prod.server.internal"
  #   remote_port: "443"
  #   local_port: "8443"
  #   ssh_port: 2222
  #   server_alive_interval: 15
  #   connect_timeout: 8
  #   strict_host_key_checking: "accept-new"
  #   proxy_jump: "jumpuser@jumphost:22"
  #   auto_restart: true
  #   max_retries: 10
  #   retry_interval: 5
```

---

## 🛠️ Local Development & Build

Standard Go toolchain with zero Node.js / npm dependencies:

```bash
# Run unit tests
make test

# Build local binary
make build-local

# Run locally
make run
```

---

## 📄 License

This project is licensed under the [MIT License](LICENSE).
