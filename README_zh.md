# SSH 隧道管理器 (SSH Tunnel Manager)

[![GitHub Container Registry](https://img.shields.io/badge/docker-ghcr.io%2Ffansys%2Fautossh--tunnel-blue?logo=docker)](https://github.com/fansys/autossh-tunnel/pkgs/container/autossh-tunnel)
[![Go Report Card](https://img.shields.io/badge/go-1.25-blue?logo=go)](https://golang.org)
[![Tailwind CSS](https://img.shields.io/badge/Tailwind_CSS-3.x-38B2AC?logo=tailwind-css)](https://tailwindcss.com)

[中文版](README_zh.md) | [English](README_en.md)

基于 Go 原生 SSH 库与 Tailwind CSS 构建的现代化、一体化（All-in-One）SSH 隧道管理系统。通过声明式 YAML 配置或可视化 Web 控制台，轻松实现 **远程端口映射到本地 (`-L`)**、**本地服务暴露到远程 (`-R`)** 以及 **动态 SOCKS5 代理 (`-D`)**。

---

## 🌟 核心特性

- **单容器极简架构**：Web 控制台、REST API、WebSocket 终端与 SSH 隧道守护整合为单一镜像，无需维护复杂的双容器网络代理。
- **纯 Go 原生 SSH 引擎**：基于 `golang.org/x/crypto/ssh` 实现，**彻底摆脱外部 autossh / sshpass / openssh 依赖**，单隧道内存开销仅几 KB。
- **全功能 Web 配置面板**：基于 Tailwind CSS 打造的现代化深/浅色控制台，支持配置 YAML 中的全部参数（端口、心跳、超时、指纹校验策略、ProxyJump 跳板机等）。
- **多样化认证与私钥安全**：
  - **密码认证**：直接支持 SSH 密码登录及环境变量引用；
  - **私钥安全落盘 (Write-Only)**：页面粘贴私钥文本后自动安全写入容器受保护文件（`0600` 权限）并在配置中引用，**页面与接口绝不回显私钥明文**；
  - **2FA / 交互式认证**：内置 WebSocket Web 终端，支持键盘交互式验证码输入。
- **一键测试连接 (Pre-flight Check) 与故障智能诊断**：
  - 保存前可一键即时校验 SSH 连通性；
  - 智能捕获并识别公钥不匹配、需要密码、指纹未受信、DNS 无法解析、端口被占用等故障，并提供修复建议。
- **多维健康监控与实时流量统计**：
  - 实时采集各隧道的 **RTT 往返时延**、**已发送/已接收流量 (Tx/Rx)** 及 **当前活跃连接数**。
- **智能退避重试 (Exponential Backoff)**：
  - 异常断开时按指数阶梯自动重连（防网络风暴与对端拉黑）；
  - 精准记忆运行状态：容器重启时**仅自动拉起处于运行中的隧道**，手动停止的隧道保持停止。
- **无状态持久化 JWT 鉴权**：
  - 管理员用户登录拦截，支持修改密码；
  - JWT Secret 持久化存储，**容器重启无需重新登录**。
- **极速多语言切换**：支持简体中文与 English 一键单键切换，内置离线静态资源，内网断网环境亦可秒开。

---

## 🚀 快速入门

### 1. 使用 Docker Compose (推荐)

创建 `docker-compose.yml` 文件：

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
      # 初始管理员账号密码 (默认: admin / admin888)
      - USERNAME=admin
      - PASSWORD=admin888
      # 可选: API 自动化调用的 Bearer Key
      # - API_KEY=your-secret-api-key
    network_mode: "host"
    restart: always
```

### 2. 启动服务

```bash
mkdir -p config
docker compose up -d
```

启动完成后，打开浏览器访问：`http://<您的IP>:8080`，输入管理员账号密码即可进入现代化控制台！

---

## 📖 配置文件示例 (`config/config.yaml`)

您既可以在 Web 页面上可视化添加/修改隧道，也可以直接编辑 `config/config.yaml`：

```yaml
tunnels:
  # 示例 1: 基础 SSH 密钥认证隧道 (远程映射到本地端口 -L)
  - name: "web-service"
    remote_host: "user@remote-host1.com"
    remote_port: "8000"
    local_port: "8001"
    direction: remote_to_local
    enabled: true

  # 示例 2: 直接使用密码认证隧道 (支持通过原生 Go SSH 自动连接保活)
  - name: "database-tunnel"
    remote_host: "root@remote-db.example.com"
    remote_port: "3306"
    local_port: "13306"
    auth_type: password
    password: "YourSecurePassword" # 或使用 password_env: "REMOTE_DB_PASS"

  # 示例 3: 动态 SOCKS5 代理隧道 (-D 本地动态代理)
  - name: "dynamic-socks5-proxy"
    remote_host: "user@gateway.example.com"
    local_port: "1080"
    direction: dynamic_socks5

  # 示例 4: 交互式 2FA/密码隧道 (在 Web 控制台终端手动输入一次性验证码)
  - name: "jumphost-2fa-tunnel"
    remote_host: "user@jumphost.example.com"
    remote_port: "22"
    local_port: "2222"
    direction: remote_to_local
    interactive: true

  # 示例 5: 高级调优配置 (自定义 SSH 选项、心跳保活、跳板机与退避重试)
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

## 🛠️ 本地开发与编译

本项目采用标准 Go 模块构建，无需任何 Node.js / npm 依赖：

```bash
# 运行单元测试
make test

# 编译本地二进制
make build-local

# 本地启动运行
make run
```

---

## 📄 开源许可证

本项目采用 [MIT 许可证](LICENSE) 开源。
