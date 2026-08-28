// SSH Tunnel Manager - Modernized Frontend Script with Full i18n & Global Dialog Controls

let tunnelsData = [];
let terminalInstance = null;
let terminalSocket = null;
let refreshTimer = null;
let i18nInstance = null;

// Helpers
function escapeHtml(str) {
    if (!str) return '';
    return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}

function t(key, fallback = '') {
    if (window.i18n && typeof window.i18n.t === 'function') {
        const res = window.i18n.t(key);
        if (res && res !== key) return res;
    }
    return fallback || key;
}

// Modern Toast Notification
function showToast(message, type = 'info') {
    let container = document.getElementById('toastContainer');
    if (!container) {
        container = document.createElement('div');
        container.id = 'toastContainer';
        container.className = 'fixed bottom-5 right-5 z-50 flex flex-col gap-2.5 max-w-sm pointer-events-none';
        document.body.appendChild(container);
    }

    const toast = document.createElement('div');
    const colors = {
        success: 'bg-emerald-600 text-white shadow-emerald-500/20',
        error: 'bg-rose-600 text-white shadow-rose-500/20',
        warning: 'bg-amber-600 text-white shadow-amber-500/20',
        info: 'bg-indigo-600 text-white shadow-indigo-500/20'
    };
    const icons = {
        success: 'check_circle',
        error: 'error',
        warning: 'warning',
        info: 'info'
    };

    const colorClass = colors[type] || colors.info;
    const iconName = icons[type] || icons.info;

    toast.className = `${colorClass} px-4 py-3 rounded-xl shadow-lg flex items-center gap-2.5 text-xs font-medium pointer-events-auto transform transition-all duration-300 opacity-0 translate-y-3`;
    toast.innerHTML = `<i class="material-icons text-base flex-shrink-0">${iconName}</i> <span class="flex-1">${escapeHtml(message)}</span>`;

    container.appendChild(toast);

    // Trigger animate-in
    setTimeout(() => {
        toast.classList.remove('opacity-0', 'translate-y-3');
    }, 20);

    // Auto dismiss
    setTimeout(() => {
        toast.classList.add('opacity-0', 'translate-y-3');
        setTimeout(() => {
            if (toast.parentNode) toast.parentNode.removeChild(toast);
        }, 300);
    }, 3500);
}

async function apiRequest(url, options = {}) {
    const defaultHeaders = { 'Content-Type': 'application/json' };
    options.headers = { ...defaultHeaders, ...(options.headers || {}) };
    const res = await fetch(url, options);
    if (res.status === 401) {
        window.location.href = '/login?redirect=' + encodeURIComponent(window.location.pathname);
        throw new Error('Unauthorized');
    }
    return res;
}

// ----------------- Initial Loading & Polling -----------------

document.addEventListener('DOMContentLoaded', async () => {
    // 1. Initialize i18n
    if (window.i18n) {
        i18nInstance = window.i18n;
        await i18nInstance.init();
    }

    // 2. Listen for language changes and re-render
    window.addEventListener('languageChanged', (e) => {
        renderTunnelTable(tunnelsData);
        showToast(e.detail.language === 'zh' ? '已切换为简体中文' : 'Switched to English', 'success');
    });

    // 3. Load Tunnels
    loadTunnels();
    initEventListeners();
    initGlobalDialogClosers();

    // 4. Periodic Background Refresh
    refreshTimer = setInterval(loadTunnelsQuietly, 4000);
});

async function loadTunnels() {
    try {
        const res = await apiRequest('/api/tunnels');
        if (!res.ok) return;
        const data = await res.json();
        tunnelsData = data.tunnels || [];
        renderTunnelTable(tunnelsData);
        updateStats(tunnelsData);
    } catch (err) {
        console.error('Failed to load tunnels:', err);
    }
}

async function loadTunnelsQuietly() {
    try {
        const res = await apiRequest('/api/tunnels');
        if (!res.ok) return;
        const data = await res.json();
        tunnelsData = data.tunnels || [];
        renderTunnelTable(tunnelsData);
        updateStats(tunnelsData);
    } catch (err) {}
}

function updateStats(tunnels) {
    let active = 0, degraded = 0, failed = 0, stopped = 0;
    tunnels.forEach(rt => {
        if (rt.status === 'active') active++;
        else if (rt.status === 'degraded') degraded++;
        else if (rt.status === 'failed' || rt.status === 'retrying') failed++;
        else stopped++;
    });

    const elTotal = document.getElementById('statTotal');
    const elActive = document.getElementById('statActive');
    const elDegraded = document.getElementById('statDegraded');
    const elFailed = document.getElementById('statFailed');
    const elStopped = document.getElementById('statStopped');

    if (elTotal) elTotal.innerText = tunnels.length;
    if (elActive) elActive.innerText = active;
    if (elDegraded) elDegraded.innerText = degraded;
    if (elFailed) elFailed.innerText = failed;
    if (elStopped) elStopped.innerText = stopped;
}

// ----------------- Table Rendering -----------------

function renderTunnelTable(tunnels) {
    const tbody = document.getElementById('tunnelTableBody');
    if (!tbody) return;

    if (!tunnels || tunnels.length === 0) {
        tbody.innerHTML = `<tr><td colspan="9" class="text-center py-12 text-slate-400 dark:text-slate-500">${t('table.status.no_tunnels', '暂无配置的隧道，点击上方“添加隧道”开始创建')}</td></tr>`;
        return;
    }

    let html = '';
    tunnels.forEach(rt => {
        const tObj = rt.config || {};
        // Any active, degraded, or retrying state is treated as ENABLED / RUNNING
        const isServiceEnabled = (rt.status === 'active' || rt.status === 'degraded' || rt.status === 'retrying');
        const hash = rt.hash || '';

        // Status Badge
        let statusBadge = '';
        if (rt.status === 'active') {
            statusBadge = `<span class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-semibold bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border border-emerald-500/30"><i class="material-icons text-xs">check_circle</i> ${t('table.status.running', '运行中')}</span>`;
        } else if (rt.status === 'degraded') {
            statusBadge = `<span class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-semibold bg-amber-500/15 text-amber-600 dark:text-amber-400 border border-amber-500/30"><i class="material-icons text-xs">warning</i> ${t('table.status.degraded', '端口未通')}</span>`;
        } else if (rt.status === 'retrying') {
            const retryCnt = rt.retry_state ? rt.retry_state.retry_count : 1;
            statusBadge = `<span class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-semibold bg-blue-500/15 text-blue-600 dark:text-blue-400 border border-blue-500/30"><i class="material-icons text-xs animate-spin">sync</i> ${t('table.status.retrying', '重试中')} (${retryCnt})</span>`;
        } else if (rt.status === 'interactive_required') {
            statusBadge = `<span class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-semibold bg-purple-500/15 text-purple-600 dark:text-purple-400 border border-purple-500/30"><i class="material-icons text-xs">vpn_key</i> ${t('table.status.interactive', '需交互认证')}</span>`;
        } else if (rt.status === 'failed') {
            statusBadge = `<span class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-semibold bg-rose-500/15 text-rose-600 dark:text-rose-400 border border-rose-500/30 cursor-pointer hover:bg-rose-500/25 transition-all" onclick="showDiagnostics('${hash}')"><i class="material-icons text-xs">error</i> ${t('table.status.failed', '失败 (查看原因)')}</span>`;
        } else {
            statusBadge = `<span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-semibold bg-slate-500/15 text-slate-600 dark:text-slate-400 border border-slate-500/30">${t('table.status.stopped', '已停止')}</span>`;
        }

        // Direction text
        let dirLabel = t('table.direction.remote_to_local', '远程->本地 (-L)');
        if (tObj.direction === 'local_to_remote') dirLabel = t('table.direction.local_to_remote', '本地->远程 (-R)');
        else if (tObj.direction === 'dynamic_socks5') dirLabel = t('table.direction.dynamic_socks5', 'SOCKS5 (-D)');

        // Auth Method
        let authLabel = t('table.auth.key', 'SSH 密钥');
        if (tObj.auth_type === 'password' || tObj.has_password) authLabel = t('table.auth.password', '密码认证');
        else if (tObj.interactive || tObj.auth_type === 'interactive') authLabel = t('table.auth.interactive', '2FA / 交互式');

        // Latency / Uptime / Metrics
        let perfInfo = '-';
        if (isServiceEnabled) {
            const lat = rt.latency_ms > 0 ? `${rt.latency_ms}ms` : '<1ms';
            const uptime = formatUptime(rt.uptime_seconds);
            let trafficStr = '';
            if (rt.metrics && (rt.metrics.bytes_rx > 0 || rt.metrics.bytes_tx > 0)) {
                trafficStr = `<div class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5">↑ ${formatBytes(rt.metrics.bytes_tx)}  ↓ ${formatBytes(rt.metrics.bytes_rx)}</div>`;
            }
            perfInfo = `<div><span class="text-emerald-600 dark:text-emerald-400 font-bold">${lat}</span> <span class="text-[11px] text-slate-400">(${uptime})</span></div>${trafficStr}`;
        }

        html += `
            <tr class="hover:bg-slate-50/80 dark:hover:bg-slate-750/50 transition-colors border-b border-slate-100 dark:border-slate-800">
                <td class="py-3 px-4 text-center">
                    <button onclick="toggleTunnel('${hash}', ${isServiceEnabled})" 
                            class="relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none ${isServiceEnabled ? 'bg-emerald-500 shadow-sm shadow-emerald-500/40' : 'bg-slate-300 dark:bg-slate-700'}"
                            title="${isServiceEnabled ? t('buttons.stop_tunnel', '点击停用/停止') : t('buttons.start_tunnel', '点击启用/启动')}">
                        <span class="pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out ${isServiceEnabled ? 'translate-x-5' : 'translate-x-0'}"></span>
                    </button>
                </td>
                <td class="py-3 px-4">
                    <div class="font-semibold text-slate-900 dark:text-white">${escapeHtml(tObj.name || t('table.placeholders.unnamed', '未命名隧道'))}</div>
                    <div class="text-[11px] font-mono text-slate-400">${hash.substring(0, 8)}</div>
                </td>
                <td class="py-3 px-4">${statusBadge}</td>
                <td class="py-3 px-4 text-xs font-medium text-slate-600 dark:text-slate-300">${dirLabel}</td>
                <td class="py-3 px-4 font-mono text-xs text-slate-700 dark:text-slate-300">
                    <div>${escapeHtml(tObj.remote_host || '-')}</div>
                    <div class="text-slate-400 text-[11px]">${t('table.headers.remote_port', '端口')}: ${escapeHtml(tObj.remote_port || '-')}</div>
                </td>
                <td class="py-3 px-4 font-mono text-xs font-bold text-indigo-600 dark:text-indigo-400">${escapeHtml(tObj.local_port || '-')}</td>
                <td class="py-3 px-4 text-xs text-slate-600 dark:text-slate-300">${authLabel}</td>
                <td class="py-3 px-4 text-xs">${perfInfo}</td>
                <td class="py-3 px-4 text-right">
                    <div class="inline-flex items-center gap-1">
                        ${tObj.interactive ? `
                            <button class="w-7 h-7 rounded-lg flex items-center justify-center text-purple-600 dark:text-purple-400 hover:bg-purple-50 dark:hover:bg-purple-500/10 transition-all" onclick="openTerminal('${hash}', '${escapeHtml(tObj.name || hash)}')" title="${t('buttons.open_terminal', '打开交互终端')}">
                                <i class="material-icons text-base">terminal</i>
                            </button>
                        ` : `
                            <button class="w-7 h-7 rounded-lg flex items-center justify-center text-slate-500 hover:text-indigo-600 dark:text-slate-400 dark:hover:text-indigo-400 hover:bg-slate-100 dark:hover:bg-slate-750 transition-all" onclick="restartTunnel('${hash}')" title="${t('buttons.restart_tunnel', '重启')}">
                                <i class="material-icons text-base">refresh</i>
                            </button>
                        `}
                        <button class="w-7 h-7 rounded-lg flex items-center justify-center text-slate-500 hover:text-indigo-600 dark:text-slate-400 dark:hover:text-indigo-400 hover:bg-slate-100 dark:hover:bg-slate-750 transition-all" onclick="showDiagnostics('${hash}')" title="${t('buttons.diagnostics', '运行日志与诊断')}">
                            <i class="material-icons text-base">analytics</i>
                        </button>
                        <button class="w-7 h-7 rounded-lg flex items-center justify-center text-slate-500 hover:text-indigo-600 dark:text-slate-400 dark:hover:text-indigo-400 hover:bg-slate-100 dark:hover:bg-slate-750 transition-all" onclick="editTunnel('${hash}')" title="${t('buttons.edit', '编辑配置')}">
                            <i class="material-icons text-base">edit</i>
                        </button>
                        <button class="w-7 h-7 rounded-lg flex items-center justify-center text-slate-500 hover:text-rose-600 dark:text-slate-400 dark:hover:text-rose-400 hover:bg-rose-50 dark:hover:bg-rose-500/10 transition-all" onclick="deleteTunnel('${hash}')" title="${t('buttons.delete', '删除')}">
                            <i class="material-icons text-base">delete</i>
                        </button>
                    </div>
                </td>
            </tr>
        `;
    });

    tbody.innerHTML = html;
}

function formatBytes(bytes) {
    if (!bytes || bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

function formatUptime(secs) {
    if (!secs || secs < 0) return '0s';
    if (secs < 60) return `${secs}s`;
    if (secs < 3600) return `${Math.floor(secs / 60)}m`;
    return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
}

// ----------------- Global Dialog Closer (Backdrop & ESC) -----------------

function closeAllModals() {
    ['tunnelModal', 'yamlModal', 'terminalModal', 'passwordModal', 'diagModal'].forEach(id => {
        const el = document.getElementById(id);
        if (el) el.classList.add('hidden');
    });
    if (terminalSocket) {
        terminalSocket.close();
        terminalSocket = null;
    }
}

function initGlobalDialogClosers() {
    ['tunnelModal', 'yamlModal', 'terminalModal', 'passwordModal', 'diagModal'].forEach(id => {
        const overlay = document.getElementById(id);
        if (overlay) {
            overlay.addEventListener('click', (e) => {
                if (e.target === overlay) {
                    overlay.classList.add('hidden');
                    if (terminalSocket) {
                        terminalSocket.close();
                        terminalSocket = null;
                    }
                }
            });
        }
    });

    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' || e.keyCode === 27) {
            closeAllModals();
        }
    });
}

// ----------------- Actions & Controls -----------------

async function toggleTunnel(hash, isRunning) {
    const action = isRunning ? 'stop' : 'start';
    try {
        await apiRequest(`/api/tunnels/${hash}/${action}`, { method: 'POST' });
        showToast(isRunning ? t('messages.tunnel_stopped', '隧道已停止') : t('messages.tunnel_started', '隧道已启动'), 'success');
        loadTunnels();
    } catch (err) {
        showToast(err.message || '操作失败', 'error');
    }
}

async function restartTunnel(hash) {
    try {
        await apiRequest(`/api/tunnels/${hash}/restart`, { method: 'POST' });
        showToast(t('messages.tunnel_restarted', '隧道已重启'), 'success');
        loadTunnels();
    } catch (err) {
        showToast(err.message || '重启失败', 'error');
    }
}

async function deleteTunnel(hash) {
    if (!confirm(t('messages.confirm_delete', '确定要删除这条 SSH 隧道配置吗？'))) return;
    try {
        await apiRequest(`/api/tunnels/${hash}`, { method: 'DELETE' });
        showToast(t('messages.tunnel_deleted', '隧道已删除'), 'success');
        loadTunnels();
    } catch (err) {
        showToast(err.message || '删除失败', 'error');
    }
}

// ----------------- Modal & Form Handlers -----------------

function initEventListeners() {
    const btnOpenAdd = document.getElementById('btnOpenAddModal');
    if (btnOpenAdd) btnOpenAdd.addEventListener('click', () => openTunnelModal());

    const btnCloseTunnel = document.getElementById('btnCloseTunnelModal');
    if (btnCloseTunnel) btnCloseTunnel.addEventListener('click', closeTunnelModal);

    const btnCancelTunnel = document.getElementById('btnCancelTunnelModal');
    if (btnCancelTunnel) btnCancelTunnel.addEventListener('click', closeTunnelModal);

    const btnStartAll = document.getElementById('btnStartAll');
    if (btnStartAll) btnStartAll.addEventListener('click', async () => {
        await apiRequest('/api/tunnels/start-all', { method: 'POST' });
        showToast(t('messages.all_started', '已启动全部隧道'), 'success');
        loadTunnels();
    });

    const btnStopAll = document.getElementById('btnStopAll');
    if (btnStopAll) btnStopAll.addEventListener('click', async () => {
        await apiRequest('/api/tunnels/stop-all', { method: 'POST' });
        showToast(t('messages.all_stopped', '已停止全部隧道'), 'info');
        loadTunnels();
    });

    const btnRefresh = document.getElementById('btnRefresh');
    if (btnRefresh) btnRefresh.addEventListener('click', () => {
        loadTunnels();
        showToast(t('messages.status_refreshed', '状态已刷新'), 'info');
    });

    const formAuthType = document.getElementById('formAuthType');
    if (formAuthType) formAuthType.addEventListener('change', handleAuthTypeChange);

    const tunnelForm = document.getElementById('tunnelForm');
    if (tunnelForm) tunnelForm.addEventListener('submit', handleTunnelFormSubmit);

    const btnTestConn = document.getElementById('btnTestConnection');
    if (btnTestConn) btnTestConn.addEventListener('click', handleTestConnection);

    const btnEditYAML = document.getElementById('btnEditYAML');
    if (btnEditYAML) btnEditYAML.addEventListener('click', openYamlModal);

    const btnCloseYaml = document.getElementById('btnCloseYamlModal');
    if (btnCloseYaml) btnCloseYaml.addEventListener('click', closeYamlModal);

    const btnCancelYaml = document.getElementById('btnCancelYamlModal');
    if (btnCancelYaml) btnCancelYaml.addEventListener('click', closeYamlModal);

    const btnSaveYaml = document.getElementById('btnSaveYaml');
    if (btnSaveYaml) btnSaveYaml.addEventListener('click', handleSaveYaml);

    const btnChangePass = document.getElementById('btnChangePassword');
    if (btnChangePass) btnChangePass.addEventListener('click', openPasswordModal);

    const btnClosePass = document.getElementById('btnClosePasswordModal');
    if (btnClosePass) btnClosePass.addEventListener('click', closePasswordModal);

    const btnCancelPass = document.getElementById('btnCancelPasswordModal');
    if (btnCancelPass) btnCancelPass.addEventListener('click', closePasswordModal);

    const passwordForm = document.getElementById('passwordForm');
    if (passwordForm) passwordForm.addEventListener('submit', handleChangePassword);

    const btnCloseDiag = document.getElementById('btnCloseDiagModal');
    if (btnCloseDiag) btnCloseDiag.addEventListener('click', closeDiagModal);

    const btnCloseDiagBtn = document.getElementById('btnCloseDiagBtn');
    if (btnCloseDiagBtn) btnCloseDiagBtn.addEventListener('click', closeDiagModal);

    const btnCloseTerminal = document.getElementById('btnCloseTerminalModal');
    if (btnCloseTerminal) btnCloseTerminal.addEventListener('click', closeTerminalModal);

    const btnLogout = document.getElementById('btnLogout');
    if (btnLogout) btnLogout.addEventListener('click', async () => {
        if (!confirm(t('messages.confirm_logout', '确定要退出登录吗？'))) return;
        await apiRequest('/api/auth/logout', { method: 'POST' });
        window.location.href = '/login';
    });
}

function setElValue(id, val) {
    const el = document.getElementById(id);
    if (el) el.value = (val !== undefined && val !== null) ? val : '';
}

function setElChecked(id, checked) {
    const el = document.getElementById(id);
    if (el) el.checked = !!checked;
}

function getElValue(id) {
    const el = document.getElementById(id);
    return el ? el.value.trim() : '';
}

function getElChecked(id, defaultVal = false) {
    const el = document.getElementById(id);
    return el ? el.checked : defaultVal;
}

function handleAuthTypeChange() {
    const val = getElValue('formAuthType') || 'key';
    const keyBox = document.getElementById('authKeyFields');
    const passBox = document.getElementById('authPasswordFields');
    const noticeBox = document.getElementById('authInteractiveNotice');

    if (keyBox) keyBox.classList.toggle('hidden', val !== 'key');
    if (passBox) passBox.classList.toggle('hidden', val !== 'password');
    if (noticeBox) noticeBox.classList.toggle('hidden', val !== 'interactive');
}

function openTunnelModal(tObj = null) {
    const testResult = document.getElementById('testConnectionResult');
    if (testResult) testResult.classList.add('hidden');

    setElValue('formPrivateKeyContent', '');
    setElValue('formPassword', '');

    if (tObj) {
        const titleEl = document.getElementById('tunnelModalTitle');
        if (titleEl) titleEl.innerText = t('tunnel_detail.edit_title', '编辑 SSH 隧道');

        setElValue('formHash', tObj.hash);
        setElValue('formName', tObj.name);
        setElValue('formDirection', tObj.direction || 'remote_to_local');
        setElValue('formRemoteHost', tObj.remote_host);
        setElValue('formRemotePort', tObj.remote_port);
        setElValue('formLocalPort', tObj.local_port);

        let authType = 'key';
        if (tObj.interactive) authType = 'interactive';
        else if (tObj.auth_type === 'password' || tObj.has_password) authType = 'password';
        setElValue('formAuthType', authType);

        setElValue('formPasswordEnv', tObj.password_env);

        const keyNotice = document.getElementById('keyStatusNotice');
        if (keyNotice) keyNotice.classList.toggle('hidden', !tObj.has_identity_file);

        const passNotice = document.getElementById('passStatusNotice');
        if (passNotice) passNotice.classList.toggle('hidden', !tObj.has_password);

        setElValue('formSSHPort', tObj.ssh_port);
        setElValue('formConnectTimeout', tObj.connect_timeout);
        setElValue('formServerAliveInterval', tObj.server_alive_interval);
        setElValue('formStrictHostKey', tObj.strict_host_key_checking || 'accept-new');
        setElValue('formProxyJump', tObj.proxy_jump);

        setElChecked('formHealthCheckEnabled', tObj.health_check_enabled !== false);
        setElChecked('formAutoRestart', tObj.auto_restart !== false);
        setElValue('formMaxRetries', tObj.max_retries);
        setElValue('formRetryInterval', tObj.retry_interval);
    } else {
        const titleEl = document.getElementById('tunnelModalTitle');
        if (titleEl) titleEl.innerText = t('tunnel_detail.add_title', '添加 SSH 隧道');

        setElValue('formHash', '');
        setElValue('formName', '');
        setElValue('formDirection', 'remote_to_local');
        setElValue('formRemoteHost', '');
        setElValue('formRemotePort', '');
        setElValue('formLocalPort', '');
        setElValue('formAuthType', 'key');
        setElValue('formPasswordEnv', '');

        const keyNotice = document.getElementById('keyStatusNotice');
        if (keyNotice) keyNotice.classList.add('hidden');

        const passNotice = document.getElementById('passStatusNotice');
        if (passNotice) passNotice.classList.add('hidden');

        setElValue('formSSHPort', '');
        setElValue('formConnectTimeout', '');
        setElValue('formServerAliveInterval', '');
        setElValue('formStrictHostKey', 'accept-new');
        setElValue('formProxyJump', '');

        setElChecked('formHealthCheckEnabled', true);
        setElChecked('formAutoRestart', true);
        setElValue('formMaxRetries', '');
        setElValue('formRetryInterval', '');
    }

    handleAuthTypeChange();
    const modal = document.getElementById('tunnelModal');
    if (modal) modal.classList.remove('hidden');
}

function closeTunnelModal() {
    const modal = document.getElementById('tunnelModal');
    if (modal) modal.classList.add('hidden');
}

function editTunnel(hash) {
    const item = tunnelsData.find(rt => rt.hash === hash);
    if (item && item.config) {
        openTunnelModal(item.config);
    }
}

function collectFormData() {
    const authType = getElValue('formAuthType') || 'key';
    const healthEnabled = getElChecked('formHealthCheckEnabled', true);
    const autoRestart = getElChecked('formAutoRestart', true);

    const hash = getElValue('formHash');
    let enabled = true;
    if (hash) {
        const existing = tunnelsData.find(rt => rt.hash === hash);
        if (existing && existing.config && existing.config.enabled !== undefined) {
            enabled = existing.config.enabled;
        }
    }

    const data = {
        hash: hash,
        name: getElValue('formName'),
        direction: getElValue('formDirection') || 'remote_to_local',
        remote_host: getElValue('formRemoteHost'),
        remote_port: getElValue('formRemotePort'),
        local_port: getElValue('formLocalPort'),
        enabled: enabled,
        auth_type: authType,
        interactive: (authType === 'interactive'),
        strict_host_key_checking: getElValue('formStrictHostKey') || 'accept-new',
        health_check_enabled: healthEnabled,
        auto_restart: autoRestart,
    };

    const privateKey = getElValue('formPrivateKeyContent');
    if (privateKey) {
        data.private_key_content = privateKey;
    }

    const pass = getElValue('formPassword');
    if (pass) data.password = pass;
    const passEnv = getElValue('formPasswordEnv');
    if (passEnv) data.password_env = passEnv;

    const sshPort = parseInt(getElValue('formSSHPort'));
    if (!isNaN(sshPort) && sshPort > 0) data.ssh_port = sshPort;

    const timeout = parseInt(getElValue('formConnectTimeout'));
    if (!isNaN(timeout) && timeout > 0) data.connect_timeout = timeout;

    const aliveInterval = parseInt(getElValue('formServerAliveInterval'));
    if (!isNaN(aliveInterval) && aliveInterval > 0) data.server_alive_interval = aliveInterval;

    const maxRetries = parseInt(getElValue('formMaxRetries'));
    if (!isNaN(maxRetries)) data.max_retries = maxRetries;

    const retryInterval = parseInt(getElValue('formRetryInterval'));
    if (!isNaN(retryInterval) && retryInterval > 0) data.retry_interval = retryInterval;

    const proxyJump = getElValue('formProxyJump');
    if (proxyJump) data.proxy_jump = proxyJump;

    return data;
}

async function handleTunnelFormSubmit(e) {
    e.preventDefault();
    const btn = document.getElementById('btnSaveTunnel');
    btn.disabled = true;
    btn.innerText = t('buttons.saving', '保存中...');

    const payload = collectFormData();
    try {
        const res = await apiRequest('/api/tunnels', {
            method: 'POST',
            body: JSON.stringify(payload)
        });
        const data = await res.json();
        if (res.ok && data.success) {
            closeTunnelModal();
            showToast(t('messages.tunnel_saved', '配置已保存并生效'), 'success');
            loadTunnels();
        } else {
            showToast(data.error || '保存失败', 'error');
        }
    } catch (err) {
        showToast(err.message || '网络请求失败', 'error');
    } finally {
        btn.disabled = false;
        btn.innerText = t('buttons.save_apply', '保存并应用');
    }
}

async function handleTestConnection() {
    const btn = document.getElementById('btnTestConnection');
    const resBox = document.getElementById('testConnectionResult');
    btn.disabled = true;
    btn.innerHTML = `<i class="material-icons text-sm animate-spin">sync</i> ${t('messages.testing', '测试中，请稍候...')}`;
    resBox.classList.add('hidden');

    const payload = collectFormData();
    try {
        const res = await apiRequest('/api/test-connection', {
            method: 'POST',
            body: JSON.stringify(payload)
        });
        const diag = await res.json();
        resBox.classList.remove('hidden');

        if (diag.success) {
            resBox.className = 'mt-2 p-3 rounded-xl bg-emerald-500/10 border border-emerald-500/30 text-emerald-600 dark:text-emerald-400 text-xs font-medium';
            resBox.innerHTML = `<strong>✅ ${t('messages.test_success', '测试连接成功！')}</strong> 往返时延: ${diag.latency_ms}ms。SSH 认证已通过。`;
        } else {
            resBox.className = 'mt-2 p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-600 dark:text-rose-400 text-xs space-y-1';
            resBox.innerHTML = `
                <div class="font-bold text-xs">❌ ${escapeHtml(diag.title || t('messages.test_failed', '连接失败'))}</div>
                <div class="text-xs">${escapeHtml(diag.description || '')}</div>
                ${diag.suggestion ? `<div class="bg-black/10 dark:bg-black/25 p-2 rounded-lg text-[11px] mt-1"><strong>💡 排查建议:</strong> ${escapeHtml(diag.suggestion)}</div>` : ''}
            `;
        }
    } catch (err) {
        resBox.classList.remove('hidden');
        resBox.className = 'mt-2 p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-600 dark:text-rose-400 text-xs';
        resBox.innerHTML = `<strong>❌ 测试异常:</strong> ${escapeHtml(err.message)}`;
    } finally {
        btn.disabled = false;
        btn.innerHTML = '<i class="material-icons text-base text-indigo-500">speed</i> 测试连接 (Pre-flight Check)';
    }
}

// ----------------- YAML Editor -----------------

async function openYamlModal() {
    const textarea = document.getElementById('yamlEditorArea');
    const errBox = document.getElementById('yamlErrorNotice');
    if (errBox) errBox.classList.add('hidden');

    try {
        const res = await apiRequest('/api/config/yaml');
        const data = await res.json();
        if (textarea) textarea.value = data.yaml || '';
        const modal = document.getElementById('yamlModal');
        if (modal) modal.classList.remove('hidden');
    } catch (err) {
        showToast(err.message || '读取 YAML 配置失败', 'error');
    }
}

function closeYamlModal() {
    const modal = document.getElementById('yamlModal');
    if (modal) modal.classList.add('hidden');
}

async function handleSaveYaml() {
    const btn = document.getElementById('btnSaveYaml');
    const textarea = document.getElementById('yamlEditorArea');
    const errBox = document.getElementById('yamlErrorNotice');
    btn.disabled = true;
    if (errBox) errBox.classList.add('hidden');

    try {
        const res = await apiRequest('/api/config/yaml', {
            method: 'POST',
            body: JSON.stringify({ yaml: textarea.value })
        });
        const data = await res.json();
        if (res.ok && data.success) {
            closeYamlModal();
            showToast(t('messages.yaml_saved', 'YAML 配置已成功保存并重载'), 'success');
            loadTunnels();
        } else {
            if (errBox) {
                errBox.innerText = data.error || 'YAML 格式错误';
                errBox.classList.remove('hidden');
            }
        }
    } catch (err) {
        if (errBox) {
            errBox.innerText = '保存失败: ' + err.message;
            errBox.classList.remove('hidden');
        }
    } finally {
        btn.disabled = false;
    }
}

// ----------------- Diagnostics Modal -----------------

async function showDiagnostics(hash) {
    const item = tunnelsData.find(rt => rt.hash === hash);
    if (!item) return;

    const title = document.getElementById('diagTitle');
    const content = document.getElementById('diagContent');
    const logsBox = document.getElementById('diagLogs');

    if (title) title.innerText = `隧道诊断分析 - ${item.config?.name || hash.substring(0, 8)}`;

    let diagHtml = '';
    if (item.diagnostic) {
        const d = item.diagnostic;
        diagHtml = `
            <div class="p-4 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-600 dark:text-rose-400 text-xs space-y-1.5">
                <div class="font-bold text-sm flex items-center gap-1.5"><i class="material-icons text-base">error</i> ${escapeHtml(d.title)}</div>
                <div>${escapeHtml(d.description)}</div>
                <div class="bg-black/10 dark:bg-black/30 p-2.5 rounded-lg text-[11px] mt-2 text-slate-700 dark:text-slate-300">
                    <strong>💡 解决建议:</strong> ${escapeHtml(d.suggestion)}
                </div>
            </div>
        `;
    } else if (item.status === 'active') {
        diagHtml = `
            <div class="p-4 rounded-xl bg-emerald-500/10 border border-emerald-500/30 text-emerald-600 dark:text-emerald-400 text-xs font-medium">
                <strong>✅ 隧道运行良好</strong>，本地端口监听正常，往返时延: ${item.latency_ms}ms。
            </div>
        `;
    } else {
        diagHtml = `<div class="text-slate-500 text-xs">当前无异常诊断信息。状态: ${item.status}</div>`;
    }
    if (content) content.innerHTML = diagHtml;

    // Load logs
    if (logsBox) logsBox.innerText = '加载日志中...';
    try {
        const res = await apiRequest(`/api/tunnels/${hash}/logs?lines=100`);
        const logData = await res.json();
        if (logsBox) logsBox.innerText = logData.logs || '(暂无日志输出)';
    } catch (err) {
        if (logsBox) logsBox.innerText = '加载日志失败: ' + err.message;
    }

    const modal = document.getElementById('diagModal');
    if (modal) modal.classList.remove('hidden');
}

function closeDiagModal() {
    const modal = document.getElementById('diagModal');
    if (modal) modal.classList.add('hidden');
}

// ----------------- Interactive Terminal (Xterm) -----------------

function openTerminal(hash, name) {
    const titleEl = document.getElementById('terminalTitle');
    if (titleEl) titleEl.innerHTML = `<i class="material-icons text-purple-400 text-base">terminal</i> 交互式终端认证 - ${escapeHtml(name)}`;

    const container = document.getElementById('terminalContainer');
    if (!container) return;
    container.innerHTML = '';

    if (terminalInstance) {
        terminalInstance.dispose();
    }

    terminalInstance = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: 'Menlo, Monaco, "Courier New", monospace',
        theme: {
            background: '#020617',
            foreground: '#f8fafc'
        }
    });

    terminalInstance.open(container);

    if (typeof FitAddon !== 'undefined') {
        const FitCtor = (typeof FitAddon === 'function') ? FitAddon : (FitAddon.FitAddon || null);
        if (FitCtor) {
            const fitAddon = new FitCtor();
            terminalInstance.loadAddon(fitAddon);
            setTimeout(() => {
                try { fitAddon.fit(); } catch(e) {}
            }, 60);
        }
    }

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${proto}//${window.location.host}/ws/terminal?hash=${encodeURIComponent(hash)}`;

    terminalSocket = new WebSocket(wsUrl);
    terminalSocket.binaryType = 'arraybuffer';

    terminalSocket.onopen = () => {
        terminalInstance.writeln('\x1b[32m=== SSH 交互式认证会话已连接 ===\x1b[0m\r\n');
    };

    terminalSocket.onmessage = (e) => {
        if (e.data instanceof ArrayBuffer) {
            terminalInstance.write(new Uint8Array(e.data));
        } else {
            terminalInstance.write(e.data);
        }
    };

    terminalSocket.onclose = () => {
        terminalInstance.writeln('\r\n\x1b[33m=== 终端会话已断开 ===\x1b[0m');
        setTimeout(loadTunnels, 1000);
    };

    terminalInstance.onData((data) => {
        if (terminalSocket && terminalSocket.readyState === WebSocket.OPEN) {
            terminalSocket.send(data);
        }
    });

    const modal = document.getElementById('terminalModal');
    if (modal) modal.classList.remove('hidden');
}

function closeTerminalModal() {
    if (terminalSocket) {
        terminalSocket.close();
        terminalSocket = null;
    }
    const modal = document.getElementById('terminalModal');
    if (modal) modal.classList.add('hidden');
}

// ----------------- Change Password -----------------

function openPasswordModal() {
    setElValue('oldPass', '');
    setElValue('newPass', '');
    setElValue('confirmPass', '');
    const errBox = document.getElementById('passwordErrorNotice');
    if (errBox) errBox.classList.add('hidden');
    const modal = document.getElementById('passwordModal');
    if (modal) modal.classList.remove('hidden');
}

function closePasswordModal() {
    const modal = document.getElementById('passwordModal');
    if (modal) modal.classList.add('hidden');
}

async function handleChangePassword(e) {
    e.preventDefault();
    const oldP = getElValue('oldPass');
    const newP = getElValue('newPass');
    const confirmP = getElValue('confirmPass');
    const errBox = document.getElementById('passwordErrorNotice');

    if (newP !== confirmP) {
        if (errBox) {
            errBox.innerText = t('messages.password_mismatch', '两次输入的新密码不一致');
            errBox.classList.remove('hidden');
        }
        return;
    }

    try {
        const res = await apiRequest('/api/auth/change-password', {
            method: 'POST',
            body: JSON.stringify({ old_password: oldP, new_password: newP })
        });
        const data = await res.json();
        if (res.ok && data.success) {
            showToast(t('messages.password_changed', '密码修改成功，请重新登录'), 'success');
            setTimeout(() => {
                window.location.href = '/login';
            }, 1000);
        } else {
            if (errBox) {
                errBox.innerText = data.error || '原密码错误';
                errBox.classList.remove('hidden');
            }
        }
    } catch (err) {
        if (errBox) {
            errBox.innerText = '修改失败: ' + err.message;
            errBox.classList.remove('hidden');
        }
    }
}
