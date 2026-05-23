/* =========================================================================
   SECURE SERVER MANAGEMENT - FRONTEND SPA CORE
   ========================================================================= */

// Global Application State
const state = {
    authenticated: false,
    servers: [],
    tags: [],
    selectedTagFilter: new Set(),
    currentView: 'page-dashboard',
    currentDetailServerId: null,
    activeTerminalServerId: null,
    pollingInterval: null,
    connectionCheckInterval: null,
    newServerToken: null,
    newServerId: null,
    processedCmds: new Set(),
    printedCmdLines: new Set(),
    terminalInterval: null,
    editingServerTagsId: null,
    pendingServerTags: new Set(),
};

// SVG Dash Array Value for Metric Circles
const RING_CIRCUMFERENCE = 534; // 2 * Math.PI * r (where r = 85)

// Document Ready Initialize
document.addEventListener('DOMContentLoaded', () => {
    initElements();
    checkAuth();
    setupEventListeners();
});

// Cache DOM elements
let el = {};
function initElements() {
    el = {
        toast: document.getElementById('toast'),
        toastMsg: document.getElementById('toast-message'),
        
        authContainer: document.getElementById('auth-container'),
        mainContainer: document.getElementById('main-container'),
        loginForm: document.getElementById('login-form'),
        loginError: document.getElementById('login-error'),
        loginErrorText: document.getElementById('error-text'),
        username: document.getElementById('username'),
        password: document.getElementById('password'),
        
        pageTitle: document.getElementById('page-title'),
        pageSubtitle: document.getElementById('page-subtitle'),
        menuItems: document.querySelectorAll('.menu-item'),
        btnLogout: document.getElementById('btn-logout'),
        
        // Views
        pageDashboard: document.getElementById('page-dashboard'),
        pageServers: document.getElementById('page-servers'),
        pageServerDetail: document.getElementById('page-server-detail'),
        
        // Overview Stats
        statTotal: document.getElementById('stat-total-servers'),
        statOnline: document.getElementById('stat-online-servers'),
        statOffline: document.getElementById('stat-offline-servers'),
        statAvgCpu: document.getElementById('stat-avg-cpu'),
        statAvgRam: document.getElementById('stat-avg-ram'),
        
        // Containers
        dashboardGrid: document.getElementById('servers-status-grid'),
        dashboardEmpty: document.getElementById('dashboard-empty-state'),
        serversTableBody: document.getElementById('servers-table-body'),
        tableEmpty: document.getElementById('table-empty-state'),
        
        // Server Detail page elements
        btnBack: document.getElementById('btn-back-to-dashboard'),
        detailName: document.getElementById('detail-server-name'),
        detailStatus: document.getElementById('detail-status-badge'),
        detailIp: document.getElementById('detail-ip'),
        detailUptime: document.getElementById('detail-uptime'),
        detailOs: document.getElementById('detail-os'),
        detailCpuModel: document.getElementById('detail-cpu-model'),
        detailCpuCores: document.getElementById('detail-cpu-cores'),
        detailRamUsed: document.getElementById('detail-ram-used'),
        detailRamFree: document.getElementById('detail-ram-free'),
        detailRamTotal: document.getElementById('detail-ram-total'),
        detailDiskUsed: document.getElementById('detail-disk-used'),
        detailDiskFree: document.getElementById('detail-disk-free'),
        detailDiskTotal: document.getElementById('detail-disk-total'),
        
        // Ring progress bars
        ringCpu: document.getElementById('ring-cpu'),
        ringRam: document.getElementById('ring-ram'),
        ringDisk: document.getElementById('ring-disk'),
        
        // Gauge text highlights
        gaugeCpuVal: document.getElementById('gauge-cpu-val'),
        gaugeRamVal: document.getElementById('gauge-ram-val'),
        gaugeDiskVal: document.getElementById('gauge-disk-val'),
        gaugeCpuPercent: document.getElementById('gauge-cpu-percent'),
        gaugeRamPercent: document.getElementById('gauge-ram-percent'),
        gaugeDiskPercent: document.getElementById('gauge-disk-percent'),
        
        // Add Server Modal Wizard
        btnAddServerTrigger: document.getElementById('btn-add-server-trigger'),
        addServerModal: document.getElementById('add-server-modal'),
        btnCloseModal: document.getElementById('btn-close-modal'),
        newServerName: document.getElementById('new-server-name'),
        btnGenerateAgent: document.getElementById('btn-generate-agent-command'),
        btnCopyCommand: document.getElementById('btn-copy-command'),
        agentCommandText: document.getElementById('agent-command-text'),
        btnFinishWizard: document.getElementById('btn-finish-wizard'),
        
        // Wizard step panels
        wizardStep1: document.getElementById('wizard-step-1'),
        wizardStep2: document.getElementById('wizard-step-2'),
        connectionLoader: document.getElementById('connection-loader'),
        connectionSuccess: document.getElementById('connection-success'),
        connectedFeedback: document.getElementById('connected-server-feedback'),
        
        // Tag filter bar
        tagFilterPills: document.getElementById('tag-filter-pills'),
        btnManageTags: document.getElementById('btn-manage-tags'),

        // Manage Tags Modal
        tagsModal: document.getElementById('tags-modal'),
        btnCloseTagsModal: document.getElementById('btn-close-tags-modal'),
        newTagName: document.getElementById('new-tag-name'),
        tagColorPicker: document.getElementById('tag-color-picker'),
        btnCreateTag: document.getElementById('btn-create-tag'),
        tagsListContainer: document.getElementById('tags-list-container'),

        // Server Tags Modal
        serverTagsModal: document.getElementById('server-tags-modal'),
        btnCloseServerTagsModal: document.getElementById('btn-close-server-tags-modal'),
        serverTagsSubtitle: document.getElementById('server-tags-modal-subtitle'),
        serverTagsChecklist: document.getElementById('server-tags-checklist'),
        btnSaveServerTags: document.getElementById('btn-save-server-tags'),

        // Terminal Modal Elements
        terminalModal: document.getElementById('terminal-modal'),
        terminalSessionStatus: document.getElementById('terminal-session-status'),
        terminalOutputLogs: document.getElementById('terminal-output-logs'),
        terminalInput: document.getElementById('terminal-input'),
        terminalPromptLabel: document.getElementById('terminal-prompt-label'),
        terminalTitleLabel: document.getElementById('terminal-title-label'),
        terminalSuggestions: document.getElementById('terminal-suggestions')
    };
}

// Setup Interaction Handlers
function setupEventListeners() {
    // Login Submit
    el.loginForm.addEventListener('submit', handleLoginSubmit);
    
    // Sidebar Navigation Tabs
    el.menuItems.forEach(item => {
        item.addEventListener('click', (e) => {
            const target = e.currentTarget.getAttribute('data-target');
            switchView(target);
        });
    });
    
    // Logout
    el.btnLogout.addEventListener('click', handleLogout);
    
    // Back from detailed view
    el.btnBack.addEventListener('click', () => {
        switchView('page-dashboard');
    });
    
    // Modal controls
    el.btnAddServerTrigger.addEventListener('click', openAddServerModal);

    // Tag management modal
    el.btnManageTags.addEventListener('click', openTagsModal);
    el.btnCloseTagsModal.addEventListener('click', closeTagsModal);
    el.btnCreateTag.addEventListener('click', handleCreateTag);
    el.newTagName.addEventListener('keydown', (e) => { if (e.key === 'Enter') handleCreateTag(); });
    el.tagColorPicker.addEventListener('click', (e) => {
        const dot = e.target.closest('.color-dot');
        if (!dot) return;
        el.tagColorPicker.querySelectorAll('.color-dot').forEach(d => d.classList.remove('selected'));
        dot.classList.add('selected');
    });

    // Server tags modal
    el.btnCloseServerTagsModal.addEventListener('click', closeServerTagsModal);
    el.btnSaveServerTags.addEventListener('click', handleSaveServerTags);
    
    // Handle empty state button clicks
    document.addEventListener('click', (e) => {
        if (e.target && e.target.classList.contains('btn-add-first-server')) {
            openAddServerModal();
        }
    });
    
    el.btnCloseModal.addEventListener('click', closeAddServerModal);

    // Wizard step 1 generate
    el.btnGenerateAgent.addEventListener('click', handleGenerateAgent);
    
    // Copy installer command
    el.btnCopyCommand.addEventListener('click', copyInstallerCommand);
    
    // Finish wizard
    el.btnFinishWizard.addEventListener('click', () => {
        closeAddServerModal();
        switchView('page-dashboard');
        fetchTelemetry();
    });

    // Terminal keyboard handler
    el.terminalInput.addEventListener('keydown', handleTerminalKeydown);
}

// Show custom toast notification
function showToast(message, isError = false) {
    el.toastMsg.innerText = message;
    el.toast.className = `toast show${isError ? ' error' : ''}`;
    
    const icon = el.toast.querySelector('.toast-icon');
    if (isError) {
        icon.className = 'fa-solid fa-circle-exclamation toast-icon';
    } else {
        icon.className = 'fa-solid fa-circle-check toast-icon';
    }
    
    setTimeout(() => {
        el.toast.classList.remove('show');
        el.toast.classList.add('hidden');
    }, 4000);
}

// Check session on start
async function checkAuth() {
    try {
        const response = await fetch('/api/servers');
        if (response.status !== 401) {
            loginSuccess();
        } else {
            logoutSuccess();
        }
    } catch (e) {
        logoutSuccess();
    }
}

// Handle Admin Sign In Form
async function handleLoginSubmit(e) {
    e.preventDefault();
    const username = el.username.value.trim();
    const password = el.password.value;
    
    try {
        const response = await fetch('/api/auth/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ username, password })
        });
        
        const data = await response.json();
        
        if (response.ok) {
            showToast('Login successful!');
            loginSuccess();
        } else {
            el.loginErrorText.innerText = data.error || 'Username or password incorrect';
            el.loginError.classList.remove('hidden');
        }
    } catch (err) {
        el.loginErrorText.innerText = 'Cannot connect to management server.';
        el.loginError.classList.remove('hidden');
    }
}

async function handleLogout() {
    try {
        await fetch('/api/auth/logout', { method: 'POST' });
        showToast('Successfully signed out.');
    } catch (e) {}
    logoutSuccess();
}

function loginSuccess() {
    state.authenticated = true;
    el.authContainer.classList.add('hidden');
    el.mainContainer.classList.remove('hidden');

    fetchTags();
    fetchTelemetry();
    state.pollingInterval = setInterval(fetchTelemetry, 5000);
}

async function fetchTags() {
    try {
        const res = await fetch('/api/tags');
        if (!res.ok) return;
        state.tags = await res.json() || [];
        renderTagFilterBar();
    } catch (_) {}
}

function logoutSuccess() {
    state.authenticated = false;
    el.authContainer.classList.remove('hidden');
    el.mainContainer.classList.add('hidden');
    
    if (state.pollingInterval) {
        clearInterval(state.pollingInterval);
        state.pollingInterval = null;
    }
    if (state.connectionCheckInterval) {
        clearInterval(state.connectionCheckInterval);
        state.connectionCheckInterval = null;
    }
    stopTerminalPolling();
}

// --- Navigation view switcher ---

function switchView(viewId) {
    // Update menu bar states
    el.menuItems.forEach(item => {
        if (item.getAttribute('data-target') === viewId || 
            (viewId === 'page-server-detail' && item.getAttribute('data-target') === 'page-dashboard')) {
            item.classList.add('active');
        } else {
            item.classList.remove('active');
        }
    });
    
    // Hide active views
    el.pageDashboard.classList.add('hidden');
    el.pageServers.classList.add('hidden');
    el.pageServerDetail.classList.add('hidden');
    
    // Show selected view
    document.getElementById(viewId).classList.remove('hidden');
    state.currentView = viewId;
    
    // Update headers text dynamically
    if (viewId === 'page-dashboard') {
        el.pageTitle.innerText = 'Infrastructure Dashboard';
        el.pageSubtitle.innerText = 'Real-time telemetry, active host statuses, and metric aggregations.';
        el.btnAddServerTrigger.classList.remove('hidden');
    } else if (viewId === 'page-servers') {
        el.pageTitle.innerText = 'Servers Inventory';
        el.pageSubtitle.innerText = 'Comprehensive server registry, specs, and status actions.';
        el.btnAddServerTrigger.classList.remove('hidden');
    } else if (viewId === 'page-server-detail') {
        el.pageTitle.innerText = 'Telemetry Deep-Dive';
        el.pageSubtitle.innerText = 'Granular performance stats, memory analytics, and uptime records.';
        el.btnAddServerTrigger.classList.add('hidden');
    }
    
    // Refresh display
    renderViews();
}

// --- Telemetry Gathers & Rendering ---

async function fetchTelemetry() {
    if (!state.authenticated) return;
    
    try {
        const response = await fetch('/api/servers');
        if (response.status === 401) {
            logoutSuccess();
            return;
        }
        
        const data = await response.json();
        state.servers = data.sort((a, b) => a.name.localeCompare(b.name));
        
        calculateOverviewStats();
        renderViews();
    } catch (e) {
        console.error("Error loading server metrics", e);
    }
}

// Compute aggregate metrics
function calculateOverviewStats() {
    const total = state.servers.length;
    let online = 0;
    let totalCpu = 0;
    let totalRam = 0;
    let activeMetricServers = 0;
    
    const now = new Date();
    
    state.servers.forEach(s => {
        // dynamic online status calculation based on 15s reporting window
        const isOnline = s.connected && (now - new Date(s.last_report)) < 15000;
        if (isOnline) {
            online++;
            totalCpu += s.cpu_usage;
            totalRam += s.ram_usage;
            activeMetricServers++;
        }
    });
    
    const offline = total - online;
    const avgCpu = activeMetricServers > 0 ? Math.round(totalCpu / activeMetricServers) : 0;
    const avgRam = activeMetricServers > 0 ? Math.round(totalRam / activeMetricServers) : 0;
    
    // Update UI headers overview
    el.statTotal.innerText = total;
    el.statOnline.innerText = online;
    el.statOffline.innerText = offline;
    el.statAvgCpu.innerText = `${avgCpu}%`;
    el.statAvgRam.innerText = `${avgRam}%`;
}

function renderViews() {
    const now = new Date();
    
    if (state.currentView === 'page-dashboard') {
        renderDashboardView(now);
    } else if (state.currentView === 'page-servers') {
        renderServersTableView(now);
    } else if (state.currentView === 'page-server-detail') {
        renderServerDetailsView(now);
    }
}

// Render Dashboard (Cards Grid View)
function renderDashboardView(now) {
    const cards = state.servers.map(s => {
        const isOnline = s.connected && (now - new Date(s.last_report)) < 15000;
        const statusClass = isOnline ? 'online' : 'offline';
        const statusText = isOnline ? 'Online' : 'Offline';
        
        // Gathers formatting
        const lastActive = s.connected ? formatRelativeTime(new Date(s.last_report)) : 'Never';
        const cpuVal = s.connected && isOnline ? s.cpu_usage.toFixed(1) : '0';
        const ramVal = s.connected && isOnline ? s.ram_usage.toFixed(1) : '0';
        const diskVal = s.connected ? s.disk_usage.toFixed(1) : '0';
        
        return `
            <div class="card server-live-card ${statusClass}" onclick="viewServerDetails('${s.id}')">
                <div class="card-header-main">
                    <div class="server-info-title">
                        <h3>${s.name}</h3>
                        <span><i class="fa-solid fa-network-wired"></i> ${s.ip || '0.0.0.0'}</span>
                    </div>
                    <span class="status-badge ${statusClass}">${statusText}</span>
                </div>
                <div class="card-body-metrics">
                    <div class="metric-bar-group">
                        <div class="metric-bar-header">
                            <span class="label"><i class="fa-solid fa-microchip"></i> CPU Usage</span>
                            <span>${cpuVal}%</span>
                        </div>
                        <div class="progress-track">
                            <div class="progress-fill fill-cpu" style="width: ${cpuVal}%"></div>
                        </div>
                    </div>
                    
                    <div class="metric-bar-group">
                        <div class="metric-bar-header">
                            <span class="label"><i class="fa-solid fa-memory"></i> RAM Usage</span>
                            <span>${ramVal}%</span>
                        </div>
                        <div class="progress-track">
                            <div class="progress-fill fill-ram" style="width: ${ramVal}%"></div>
                        </div>
                    </div>

                    <div class="metric-bar-group">
                        <div class="metric-bar-header">
                            <span class="label"><i class="fa-solid fa-hard-drive"></i> Disk Usage</span>
                            <span>${diskVal}%</span>
                        </div>
                        <div class="progress-track">
                            <div class="progress-fill fill-disk" style="width: ${diskVal}%"></div>
                        </div>
                    </div>
                </div>
                <div class="card-footer-meta">
                    <span><i class="fa-solid fa-clock"></i> ${s.connected ? s.uptime : 'Offline'}</span>
                    <span>Last Seen: ${lastActive}</span>
                </div>
            </div>
        `;
    });
    
    const dynamicCards = cards.join('');
    
    if (state.servers.length === 0) {
        el.dashboardEmpty.classList.remove('hidden');
        const cardsGroup = el.dashboardGrid.querySelectorAll('.server-live-card');
        cardsGroup.forEach(c => c.remove());
    } else {
        el.dashboardEmpty.classList.add('hidden');
        el.dashboardGrid.innerHTML = dynamicCards;
    }
}

// Render Servers Table List View
function renderServersTableView(now) {
    const filtered = state.selectedTagFilter.size === 0
        ? state.servers
        : state.servers.filter(s => (s.tags || []).some(t => state.selectedTagFilter.has(t)));

    const tagMap = Object.fromEntries(state.tags.map(t => [t.name, t.color]));

    const rows = filtered.map(s => {
        const isOnline = s.connected && (now - new Date(s.last_report)) < 15000;
        const statusBadge = `<span class="badge ${isOnline ? 'online' : 'offline'}">${isOnline ? 'Online' : 'Offline'}</span>`;
        const lastActive = s.connected ? formatRelativeTime(new Date(s.last_report)) : 'Never';
        const cpuVal = s.connected && isOnline ? s.cpu_usage.toFixed(1) : '0';
        const ramVal = s.connected && isOnline ? s.ram_usage.toFixed(1) : '0';
        const diskVal = s.connected ? s.disk_usage.toFixed(1) : '0';

        const tagPills = (s.tags || []).map(t => {
            const color = tagMap[t] || '#6366f1';
            return `<span class="tag-pill" style="background:${color}22;color:${color};border-color:${color}55">${escHTML(t)}</span>`;
        }).join('');

        return `
            <tr>
                <td style="font-family:var(--font-heading);font-weight:700;color:#fff">${escHTML(s.name)}</td>
                <td><i class="fa-solid fa-network-wired text-muted"></i> ${s.ip || '---'}</td>
                <td><i class="fa-brands fa-linux text-muted"></i> ${s.os || '---'}</td>
                <td>${statusBadge}</td>
                <td>
                    <div class="table-progress">
                        <div class="bar-track"><div class="bar-fill fill-cpu" style="width:${cpuVal}%"></div></div>
                        <span>${cpuVal}%</span>
                    </div>
                </td>
                <td>
                    <div class="table-progress">
                        <div class="bar-track"><div class="bar-fill fill-ram" style="width:${ramVal}%"></div></div>
                        <span>${ramVal}%</span>
                    </div>
                </td>
                <td>
                    <div class="table-progress">
                        <div class="bar-track"><div class="bar-fill fill-disk" style="width:${diskVal}%"></div></div>
                        <span>${diskVal}%</span>
                    </div>
                </td>
                <td>${s.connected ? s.uptime : '---'}</td>
                <td>${lastActive}</td>
                <td>
                    <div class="tag-cell">
                        ${tagPills}
                        <button class="btn-tag-edit" title="Edit tags" onclick="openServerTagsModal('${s.id}')">
                            <i class="fa-solid fa-pen-to-square"></i>
                        </button>
                    </div>
                </td>
                <td>
                    <div style="display:flex;gap:0.4rem;align-items:center">
                        <button class="btn btn-primary btn-sm" onclick="openTerminalShell('${s.id}','${escHTML(s.name)}')" title="Open Shell">
                            <i class="fa-solid fa-terminal"></i> Shell
                        </button>
                        <button class="btn btn-danger btn-sm" onclick="deleteServer('${s.id}','${escHTML(s.name)}')" title="Delete Server">
                            <i class="fa-solid fa-trash-can"></i>
                        </button>
                    </div>
                </td>
            </tr>
        `;
    });

    if (state.servers.length === 0) {
        el.tableEmpty.classList.remove('hidden');
        el.serversTableBody.innerHTML = '';
    } else {
        el.tableEmpty.classList.add('hidden');
        el.serversTableBody.innerHTML = rows.join('') || `<tr><td colspan="11" style="text-align:center;padding:2rem;color:var(--text-muted)">No servers match the selected tags.</td></tr>`;
    }
}

function renderTagFilterBar() {
    const allBtn = `<button class="tag-filter-pill ${state.selectedTagFilter.size === 0 ? 'active' : ''}" onclick="clearTagFilter()">All</button>`;
    const pills = state.tags.map(t => {
        const active = state.selectedTagFilter.has(t.name);
        return `<button class="tag-filter-pill ${active ? 'active' : ''}" style="--tag-color:${t.color}" onclick="toggleTagFilter('${escHTML(t.name)}')">${escHTML(t.name)}</button>`;
    }).join('');
    el.tagFilterPills.innerHTML = allBtn + pills;
}

window.toggleTagFilter = function(name) {
    if (state.selectedTagFilter.has(name)) {
        state.selectedTagFilter.delete(name);
    } else {
        state.selectedTagFilter.add(name);
    }
    renderTagFilterBar();
    renderServersTableView(Date.now());
};

window.clearTagFilter = function() {
    state.selectedTagFilter.clear();
    renderTagFilterBar();
    renderServersTableView(Date.now());
};

// Render Server Details page
function renderServerDetailsView(now) {
    if (!state.currentDetailServerId) return;
    
    const server = state.servers.find(s => s.id === state.currentDetailServerId);
    if (!server) {
        switchView('page-dashboard');
        return;
    }
    
    const isOnline = server.connected && (now - new Date(server.last_report)) < 15000;
    const statusText = isOnline ? 'Online' : 'Offline';
    const statusClass = isOnline ? 'online' : 'offline';
    
    // Fill text header info
    el.detailName.innerText = server.name;
    el.detailStatus.innerText = statusText;
    el.detailStatus.className = `badge ${statusClass}`;
    el.detailIp.innerText = server.ip || '---';
    el.detailUptime.innerText = server.connected ? server.uptime : '---';
    el.detailOs.innerText = server.os || '---';
    
    // CPU details
    el.detailCpuModel.innerText = server.cpu_model || '---';
    el.detailCpuCores.innerText = server.cpu_cores ? `${server.cpu_cores} Cores` : '---';
    
    // RAM details
    el.detailRamUsed.innerText = server.connected ? `${server.ram_used.toFixed(2)} GB` : '0.00 GB';
    el.detailRamFree.innerText = server.connected ? `${server.ram_free.toFixed(2)} GB` : '0.00 GB';
    el.detailRamTotal.innerText = server.connected ? `${server.ram_total.toFixed(2)} GB` : '0.00 GB';
    
    // Disk details
    el.detailDiskUsed.innerText = server.connected ? `${server.disk_used.toFixed(2)} GB` : '0.00 GB';
    el.detailDiskFree.innerText = server.connected ? `${server.disk_free.toFixed(2)} GB` : '0.00 GB';
    el.detailDiskTotal.innerText = server.connected ? `${server.disk_total.toFixed(2)} GB` : '0.00 GB';
    
    // Values percentages
    const cpuPct = server.connected && isOnline ? server.cpu_usage : 0;
    const ramPct = server.connected && isOnline ? server.ram_usage : 0;
    const diskPct = server.connected ? server.disk_usage : 0;
    
    el.gaugeCpuVal.innerText = `${cpuPct.toFixed(1)}%`;
    el.gaugeRamVal.innerText = `${ramPct.toFixed(1)}%`;
    el.gaugeDiskVal.innerText = `${diskPct.toFixed(1)}%`;
    
    el.gaugeCpuPercent.innerText = `${cpuPct.toFixed(0)}%`;
    el.gaugeRamPercent.innerText = `${ramPct.toFixed(0)}%`;
    el.gaugeDiskPercent.innerText = `${diskPct.toFixed(0)}%`;
    
    // Update SVG circles
    updateProgressRing(el.ringCpu, cpuPct);
    updateProgressRing(el.ringRam, ramPct);
    updateProgressRing(el.ringDisk, diskPct);
}

// Transition into dynamic detailed monitoring page
window.viewServerDetails = function(serverId) {
    state.currentDetailServerId = serverId;
    switchView('page-server-detail');
};

// Update circular stroke gauges
function updateProgressRing(ringElement, percent) {
    const offset = RING_CIRCUMFERENCE - (percent / 100) * RING_CIRCUMFERENCE;
    ringElement.style.strokeDashoffset = offset;
}

// Relative times generator
function formatRelativeTime(date) {
    const diffMs = new Date() - date;
    const diffSec = Math.round(diffMs / 1000);
    
    if (diffSec < 6) return 'Just now';
    if (diffSec < 60) return `${diffSec}s ago`;
    
    const diffMin = Math.round(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m ago`;
    
    const diffHr = Math.round(diffMin / 60);
    return `${diffHr}h ago`;
}

// --- Delete Server Registry Action ---

async function deleteServer(id, name) {
    if (!confirm(`Are you sure you want to remove server "${name}" from the monitoring dashboard?`)) {
        return;
    }
    
    try {
        const response = await fetch(`/api/servers/${id}`, {
            method: 'DELETE'
        });
        
        if (response.ok) {
            showToast(`Successfully deleted server "${name}".`);
            fetchTelemetry();
        } else {
            showToast('Failed to delete server. Please try again.', true);
        }
    } catch (e) {
        showToast('An error occurred while sending delete request.', true);
    }
}

// --- Add Server Wizard UI Controls ---

function openAddServerModal() {
    el.newServerName.value = '';
    el.wizardStep1.classList.remove('hidden');
    el.wizardStep2.classList.add('hidden');
    el.addServerModal.classList.remove('hidden');
}

function closeAddServerModal() {
    el.addServerModal.classList.add('hidden');
    if (state.connectionCheckInterval) {
        clearInterval(state.connectionCheckInterval);
        state.connectionCheckInterval = null;
    }
}

// API Generate install command
async function handleGenerateAgent() {
    const name = el.newServerName.value.trim();
    if (!name) {
        alert('Please enter a name for your server instance!');
        return;
    }
    
    try {
        const response = await fetch('/api/servers/add', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name })
        });
        
        const server = await response.json();
        
        if (response.ok) {
            state.newServerToken = server.token;
            state.newServerId = server.id;
            
            const serverHost = window.location.host;
            const command = `curl -fsSL http://${serverHost}/agent/install/${server.token} | bash`;
            
            el.agentCommandText.innerText = command;
            
            el.wizardStep1.classList.add('hidden');
            el.wizardStep2.classList.remove('hidden');
            
            el.connectionLoader.classList.remove('hidden');
            el.connectionSuccess.classList.add('hidden');
            
            waitForAgentConnection();
        } else {
            alert('An error occurred while generating agent script.');
        }
    } catch (e) {
        alert('Cannot connect to backend API.');
    }
}

function copyInstallerCommand() {
    const text = el.agentCommandText.innerText;
    const markCopied = () => {
        el.btnCopyCommand.innerHTML = '<i class="fa-solid fa-check"></i> Copied!';
        setTimeout(() => { el.btnCopyCommand.innerHTML = '<i class="fa-solid fa-copy"></i> Copy'; }, 3000);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(markCopied).catch(() => fallbackCopy(text, markCopied));
    } else {
        fallbackCopy(text, markCopied);
    }
}

function fallbackCopy(text, onSuccess) {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    try {
        document.execCommand('copy');
        onSuccess();
    } catch (_) {}
    document.body.removeChild(ta);
}

// Active listener: waiting for first contact
function waitForAgentConnection() {
    if (state.connectionCheckInterval) {
        clearInterval(state.connectionCheckInterval);
    }
    
    state.connectionCheckInterval = setInterval(async () => {
        try {
            const response = await fetch('/api/servers');
            if (response.ok) {
                const list = await response.json();
                const freshServer = list.find(s => s.id === state.newServerId);
                
                if (freshServer && freshServer.connected) {
                    clearInterval(state.connectionCheckInterval);
                    state.connectionCheckInterval = null;
                    
                    showToast('Server agent identified successfully!');
                    
                    el.connectedFeedback.innerText = `Server "${freshServer.name}" (${freshServer.ip}) has successfully synchronized telemetry parameters.`;
                    
                    el.connectionLoader.classList.add('hidden');
                    el.connectionSuccess.classList.remove('hidden');
                }
            }
        } catch (e) {
            console.error("Connection waiting error", e);
        }
    }, 1500);
}

// --- SECURE FLOATING WEB CONSOLE TERMINAL HANDLERS ---

// ============================================================
// REAL TERMINAL: State
// ============================================================
const termState = {
    history: [],
    historyIdx: -1,
    currentPrompt: '',
    pendingEntries: new Map(),
    printedCmdLines: new Set(),
    processedCmds: new Set(),
    eventSource: null,
    tabMatches: [],
    tabIdx: -1,
    tabPrefix: '',
};

function buildPromptHTML(raw) {
    // Parse "user@host:path$" or "user@host:path#" into coloured spans
    const m = raw.match(/^([^@]*)(@)([^:]*)([:])(.+?)([#$])\s*$/);
    if (m) {
        return `<span class="prompt-user">${escHTML(m[1])}</span><span class="prompt-at">${m[2]}</span><span class="prompt-host">${escHTML(m[3])}</span><span class="prompt-colon">${m[4]}</span><span class="prompt-path">${escHTML(m[5])}</span><span class="prompt-dollar">${m[6]}</span>`;
    }
    return `<span class="prompt-dollar">${escHTML(raw)}</span>`;
}

function setLivePrompt(raw) {
    termState.currentPrompt = raw;
    if (el.terminalPromptLabel) {
        el.terminalPromptLabel.innerHTML = buildPromptHTML(raw);
    }
}

function focusTerminalInput() {
    if (el.terminalInput) el.terminalInput.focus();
}
window.focusTerminalInput = focusTerminalInput;

window.openTerminalShell = function(serverId, serverName) {
    state.activeTerminalServerId = serverId;

    // Reset terminal state
    termState.history = [];
    termState.historyIdx = -1;
    termState.pendingEntries.clear();
    termState.printedCmdLines.clear();
    termState.processedCmds.clear();

    el.terminalModal.classList.remove('hidden');
    el.terminalInput.value = '';

    if (el.terminalTitleLabel) {
        el.terminalTitleLabel.textContent = `bash — ${serverName}`;
    }

    // Set initial prompt to server name until first real prompt arrives
    setLivePrompt(`user@${serverName}:~$`);

    // Clear output area, keep welcome
    const body = el.terminalOutputLogs;
    body.innerHTML = `
        <div class="terminal-welcome-line">Welcome to <span class="text-green">Secure Shell Gateway</span> — outbound polling, no inbound SSH required.</div>
        <div class="terminal-welcome-line dim-text">Type any command and press <kbd>Enter</kbd>. Use <kbd>↑</kbd> <kbd>↓</kbd> for history. <kbd>Ctrl+L</kbd> to clear.</div>
        <div class="terminal-spacer"></div>`;

    startTerminalPolling(serverId);
    setTimeout(() => el.terminalInput.focus(), 80);
};

window.closeTerminalModal = function() {
    el.terminalModal.classList.add('hidden');
    stopTerminalPolling();
    state.activeTerminalServerId = null;
};

function startTerminalPolling(serverId) {
    stopTerminalPolling();

    const es = new EventSource(`/api/servers/terminal/stream/${serverId}`);
    termState.eventSource = es;

    es.addEventListener('connected', () => {
        pollTerminalLogs(serverId);
    });

    es.addEventListener('cmd', (e) => {
        try { renderTerminalCommandResult(JSON.parse(e.data)); } catch (_) {}
    });
}

function stopTerminalPolling() {
    if (termState.eventSource) {
        termState.eventSource.close();
        termState.eventSource = null;
    }
    if (state.terminalInterval) {
        clearInterval(state.terminalInterval);
        state.terminalInterval = null;
    }
}

async function pollTerminalLogs(serverId) {
    if (!state.authenticated || !state.activeTerminalServerId) return;
    try {
        const resp = await fetch(`/api/servers/terminal/${serverId}`);
        if (!resp.ok) return;
        const commands = await resp.json();
        if (!Array.isArray(commands)) return;

        let scrollNeeded = false;

        commands.forEach(cmd => {
            // 1. Print prompt + command line when first seen
            if (!termState.printedCmdLines.has(cmd.id)) {
                termState.printedCmdLines.add(cmd.id);

                const promptRaw = cmd.prompt || termState.currentPrompt;
                const entry = document.createElement('div');
                entry.className = 'terminal-entry';
                entry.id = `entry-${cmd.id}`;

                const cmdLine = document.createElement('div');
                cmdLine.className = 'terminal-cmd-line';
                cmdLine.innerHTML = `<span class="terminal-prompt-text">${buildPromptHTML(promptRaw)}&nbsp;</span><span class="terminal-cmd-text">${escHTML(cmd.command)}</span>`;
                entry.appendChild(cmdLine);

                // Add pending spinner
                const pending = document.createElement('div');
                pending.className = 'terminal-pending';
                pending.textContent = 'Executing...';
                entry.appendChild(pending);
                termState.pendingEntries.set(cmd.id, pending);

                el.terminalOutputLogs.appendChild(entry);
                scrollNeeded = true;
            }

            // 2. Replace pending spinner with output once done
            if ((cmd.status === 'success' || cmd.status === 'error') && !termState.processedCmds.has(cmd.id)) {
                termState.processedCmds.add(cmd.id);

                const pending = termState.pendingEntries.get(cmd.id);
                if (pending) {
                    pending.remove();
                    termState.pendingEntries.delete(cmd.id);
                }

                const entry = document.getElementById(`entry-${cmd.id}`);
                if (entry && cmd.output && cmd.output.trim() !== '') {
                    const out = document.createElement('div');
                    out.className = cmd.status === 'error' ? 'terminal-stderr' : 'terminal-stdout';
                    out.textContent = cmd.output;  // textContent = no HTML injection, preserves whitespace
                    entry.appendChild(out);
                } else if (entry && (!cmd.output || cmd.output.trim() === '')) {
                    // No output — add nothing (clean like a real terminal)
                }

                // Update live prompt with result prompt
                if (cmd.prompt) setLivePrompt(cmd.prompt);

                scrollNeeded = true;
            }
        });

        if (scrollNeeded) {
            el.terminalOutputLogs.scrollTop = el.terminalOutputLogs.scrollHeight;
        }
    } catch (e) {
        // Silent fail
    }
}

function renderTerminalCommandResult(cmd) {
    // Print command line if not pre-created (e.g., command from another session)
    if (!termState.printedCmdLines.has(cmd.id)) {
        termState.printedCmdLines.add(cmd.id);
        const entry = document.createElement('div');
        entry.className = 'terminal-entry';
        entry.id = `entry-${cmd.id}`;
        const cmdLine = document.createElement('div');
        cmdLine.className = 'terminal-cmd-line';
        cmdLine.innerHTML = `<span class="terminal-prompt-text">${buildPromptHTML(cmd.prompt || termState.currentPrompt)}&nbsp;</span><span class="terminal-cmd-text">${escHTML(cmd.command)}</span>`;
        entry.appendChild(cmdLine);
        const pending = document.createElement('div');
        pending.className = 'terminal-pending';
        pending.textContent = 'Executing...';
        entry.appendChild(pending);
        termState.pendingEntries.set(cmd.id, pending);
        el.terminalOutputLogs.appendChild(entry);
    }

    if ((cmd.status === 'success' || cmd.status === 'error') && !termState.processedCmds.has(cmd.id)) {
        termState.processedCmds.add(cmd.id);
        const pending = termState.pendingEntries.get(cmd.id);
        if (pending) { pending.remove(); termState.pendingEntries.delete(cmd.id); }
        const entry = document.getElementById(`entry-${cmd.id}`);
        if (entry && cmd.output && cmd.output.trim() !== '') {
            const out = document.createElement('div');
            out.className = cmd.status === 'error' ? 'terminal-stderr' : 'terminal-stdout';
            out.textContent = cmd.output;
            entry.appendChild(out);
        }
        if (cmd.prompt) setLivePrompt(cmd.prompt);
        el.terminalOutputLogs.scrollTop = el.terminalOutputLogs.scrollHeight;
    }
}

async function submitTerminalCommand(cmdText) {
    if (!state.activeTerminalServerId || !cmdText) return;

    if (cmdText === 'clear' || cmdText === 'reset') {
        el.terminalOutputLogs.innerHTML = '<div class="terminal-spacer"></div>';
        return;
    }

    termState.history.unshift(cmdText);
    if (termState.history.length > 200) termState.history.pop();
    termState.historyIdx = -1;

    try {
        const resp = await fetch(`/api/servers/execute/${state.activeTerminalServerId}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ command: cmdText })
        });
        if (!resp.ok) {
            const data = await resp.json().catch(() => ({}));
            appendSystemLine(`Error: ${data.error || 'Failed to send command.'}`, true);
            return;
        }
        const data = await resp.json();
        const cmdId = data.command_id;

        // Pre-render command line immediately; SSE delivers the result
        if (cmdId && !termState.printedCmdLines.has(cmdId)) {
            termState.printedCmdLines.add(cmdId);
            const entry = document.createElement('div');
            entry.className = 'terminal-entry';
            entry.id = `entry-${cmdId}`;
            const cmdLine = document.createElement('div');
            cmdLine.className = 'terminal-cmd-line';
            cmdLine.innerHTML = `<span class="terminal-prompt-text">${buildPromptHTML(termState.currentPrompt)}&nbsp;</span><span class="terminal-cmd-text">${escHTML(cmdText)}</span>`;
            entry.appendChild(cmdLine);
            const pending = document.createElement('div');
            pending.className = 'terminal-pending';
            pending.textContent = 'Executing...';
            entry.appendChild(pending);
            termState.pendingEntries.set(cmdId, pending);
            el.terminalOutputLogs.appendChild(entry);
            el.terminalOutputLogs.scrollTop = el.terminalOutputLogs.scrollHeight;
        }
    } catch (err) {
        appendSystemLine('Network error: could not reach backend.', true);
    }
}

function handleTerminalKeydown(e) {
    if (!el.terminalModal.classList.contains('hidden')) {
        // Tab → autocomplete from history
        if (e.key === 'Tab') {
            e.preventDefault();
            e.stopPropagation();
            handleTabComplete();
            return;
        }

        // Any other key (except Tab) → reset tab state and close suggestions
        if (e.key !== 'Tab') {
            if (termState.tabMatches.length > 0) {
                termState.tabMatches = [];
                termState.tabIdx = -1;
                hideSuggestions();
            }
        }

        // Enter → submit
        if (e.key === 'Enter') {
            e.preventDefault();
            const cmdText = el.terminalInput.value;
            el.terminalInput.value = '';
            hideSuggestions();
            if (cmdText.trim()) submitTerminalCommand(cmdText.trim());
            termState.historyIdx = -1;
        }
        // Arrow Up → history previous
        else if (e.key === 'ArrowUp') {
            e.preventDefault();
            if (termState.history.length === 0) return;
            termState.historyIdx = Math.min(termState.historyIdx + 1, termState.history.length - 1);
            el.terminalInput.value = termState.history[termState.historyIdx];
            setTimeout(() => { el.terminalInput.selectionStart = el.terminalInput.selectionEnd = el.terminalInput.value.length; }, 0);
        }
        // Arrow Down → history next
        else if (e.key === 'ArrowDown') {
            e.preventDefault();
            if (termState.historyIdx <= 0) {
                termState.historyIdx = -1;
                el.terminalInput.value = '';
            } else {
                termState.historyIdx--;
                el.terminalInput.value = termState.history[termState.historyIdx];
            }
        }
        // Escape → close suggestions
        else if (e.key === 'Escape') {
            hideSuggestions();
            termState.tabMatches = [];
            termState.tabIdx = -1;
        }
        // Ctrl+L → clear screen
        else if (e.ctrlKey && e.key === 'l') {
            e.preventDefault();
            el.terminalOutputLogs.innerHTML = '<div class="terminal-spacer"></div>';
        }
        // Ctrl+C → cancel input
        else if (e.ctrlKey && e.key === 'c') {
            e.preventDefault();
            const cancelled = el.terminalInput.value;
            el.terminalInput.value = '';
            termState.historyIdx = -1;
            hideSuggestions();
            const line = document.createElement('div');
            line.className = 'terminal-cmd-line';
            line.innerHTML = `<span class="terminal-prompt-text">${buildPromptHTML(termState.currentPrompt)}&nbsp;</span><span class="terminal-cmd-text">${escHTML(cancelled)}^C</span>`;
            el.terminalOutputLogs.appendChild(line);
            el.terminalOutputLogs.scrollTop = el.terminalOutputLogs.scrollHeight;
        }
    }
}

function handleTabComplete() {
    const input = el.terminalInput.value;

    // First Tab press on a new prefix — build match list
    if (termState.tabMatches.length === 0 || termState.tabPrefix !== input) {
        termState.tabPrefix = input;
        // Deduplicate history, keep order
        const seen = new Set();
        const candidates = termState.history.filter(cmd => {
            if (cmd.startsWith(input) && cmd !== input && !seen.has(cmd)) {
                seen.add(cmd);
                return true;
            }
            return false;
        });
        if (candidates.length === 0) return;
        termState.tabMatches = candidates;
        termState.tabIdx = 0;
    } else {
        // Subsequent Tab presses → cycle forward
        termState.tabIdx = (termState.tabIdx + 1) % termState.tabMatches.length;
    }

    el.terminalInput.value = termState.tabMatches[termState.tabIdx];
    showSuggestions(termState.tabMatches, termState.tabIdx);
}

function showSuggestions(matches, activeIdx) {
    el.terminalSuggestions.innerHTML = matches.map((m, i) =>
        `<div class="term-suggestion-item ${i === activeIdx ? 'active' : ''}" data-cmd="${escHTML(m)}">${escHTML(m)}</div>`
    ).join('');
    el.terminalSuggestions.classList.remove('hidden');
}

function hideSuggestions() {
    el.terminalSuggestions.classList.add('hidden');
    el.terminalSuggestions.innerHTML = '';
}

// Click on suggestion to apply it
document.addEventListener('click', (e) => {
    const item = e.target.closest('.term-suggestion-item');
    if (item) {
        el.terminalInput.value = item.dataset.cmd;
        hideSuggestions();
        termState.tabMatches = [];
        termState.tabIdx = -1;
        el.terminalInput.focus();
    }
});

function appendSystemLine(msg, isError = false) {
    const el2 = document.createElement('div');
    el2.className = isError ? 'terminal-stderr' : 'terminal-system-line';
    el2.textContent = msg;
    el.terminalOutputLogs.appendChild(el2);
    el.terminalOutputLogs.scrollTop = el.terminalOutputLogs.scrollHeight;
}

// =========================================================================
// TAG MANAGEMENT
// =========================================================================

function openTagsModal() {
    renderTagsList();
    el.tagsModal.classList.remove('hidden');
}

function closeTagsModal() {
    el.tagsModal.classList.add('hidden');
    el.newTagName.value = '';
}

function renderTagsList() {
    if (state.tags.length === 0) {
        el.tagsListContainer.innerHTML = `<p class="text-muted" style="text-align:center">No tags yet. Create one above.</p>`;
        return;
    }
    el.tagsListContainer.innerHTML = state.tags.map(t => `
        <div class="tag-manage-row">
            <span class="tag-pill" style="background:${t.color}22;color:${t.color};border-color:${t.color}55">${escHTML(t.name)}</span>
            <button class="btn btn-danger btn-sm" onclick="handleDeleteTag('${escHTML(t.name)}')">
                <i class="fa-solid fa-trash-can"></i>
            </button>
        </div>
    `).join('');
}

async function handleCreateTag() {
    const name = el.newTagName.value.trim();
    if (!name) { el.newTagName.focus(); return; }
    const selectedDot = el.tagColorPicker.querySelector('.color-dot.selected');
    const color = selectedDot ? selectedDot.dataset.color : '#6366f1';

    try {
        const res = await fetch('/api/tags', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name, color }),
        });
        if (res.status === 409) { showToast('Tag already exists.', true); return; }
        if (!res.ok) { showToast('Failed to create tag.', true); return; }
        const tag = await res.json();
        state.tags.push(tag);
        el.newTagName.value = '';
        renderTagsList();
        renderTagFilterBar();
        showToast(`Tag "${tag.name}" created.`);
    } catch (_) { showToast('Network error.', true); }
}

window.handleDeleteTag = async function(name) {
    try {
        const res = await fetch(`/api/tags/${encodeURIComponent(name)}`, { method: 'DELETE' });
        if (!res.ok) { showToast('Failed to delete tag.', true); return; }
        state.tags = state.tags.filter(t => t.name !== name);
        state.selectedTagFilter.delete(name);
        state.servers.forEach(s => { if (s.tags) s.tags = s.tags.filter(t => t !== name); });
        renderTagsList();
        renderTagFilterBar();
        showToast(`Tag "${name}" deleted.`);
    } catch (_) { showToast('Network error.', true); }
};

// =========================================================================
// SERVER TAG ASSIGNMENT
// =========================================================================

window.openServerTagsModal = function(serverId) {
    const server = state.servers.find(s => s.id === serverId);
    if (!server) return;
    state.editingServerTagsId = serverId;
    state.pendingServerTags = new Set(server.tags || []);

    el.serverTagsSubtitle.textContent = server.name;

    if (state.tags.length === 0) {
        el.serverTagsChecklist.innerHTML = `<p class="text-muted">No tags available. Create tags first via "Manage Tags".</p>`;
    } else {
        el.serverTagsChecklist.innerHTML = state.tags.map(t => `
            <label class="tag-check-row">
                <input type="checkbox" value="${escHTML(t.name)}" ${state.pendingServerTags.has(t.name) ? 'checked' : ''}>
                <span class="tag-pill" style="background:${t.color}22;color:${t.color};border-color:${t.color}55">${escHTML(t.name)}</span>
            </label>
        `).join('');
    }

    el.serverTagsModal.classList.remove('hidden');
};

function closeServerTagsModal() {
    el.serverTagsModal.classList.add('hidden');
    state.editingServerTagsId = null;
}

async function handleSaveServerTags() {
    const id = state.editingServerTagsId;
    if (!id) return;

    const checked = [...el.serverTagsChecklist.querySelectorAll('input[type=checkbox]:checked')].map(c => c.value);

    try {
        const res = await fetch(`/api/servers/${id}/tags`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ tags: checked }),
        });
        if (!res.ok) { showToast('Failed to save tags.', true); return; }
        const server = state.servers.find(s => s.id === id);
        if (server) server.tags = checked;
        closeServerTagsModal();
        renderServersTableView(Date.now());
        showToast('Tags updated.');
    } catch (_) { showToast('Network error.', true); }
}

function escHTML(str) {
    if (!str) return '';
    const d = document.createElement('div');
    d.textContent = str;
    return d.innerHTML;
}

function escapeHTML(str) { return escHTML(str); }
