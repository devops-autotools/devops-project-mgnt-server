# Multi Shell Overlay + Paste Focus Fix — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a tiled fullscreen Multi Shell overlay (2/4/8 panes, each with a server picker) to the Server List page, and fix the paste-then-edit focus bug in all shell windows.

**Architecture:** Frontend-only change across three files (`index.html`, `style.css`, `app.js`). The overlay is a `position:fixed` fullscreen div with a CSS grid of panes. Each pane creates its own xterm.js terminal and WebSocket session by reusing the existing `genSessionId()` and shell WebSocket path. The paste fix adds two event listeners to the shell body element inside the existing `openShell()` function. Local dev uses `go run main.go`; deploy uses rsync to `ubuntu@172.25.155.195` (never sync `data/`).

**Tech Stack:** Vanilla JS, plain CSS, xterm.js v5.3.0 + xterm-addon-fit v0.8.0 (already loaded from CDN), Go `net/http` backend (no changes needed).

---

## File Map

| File | Change |
|------|--------|
| `public/index.html` | Add Multi Shell button + picker dropdown in `header-actions`; add `#multi-shell-overlay` div before `#shell-windows` |
| `public/css/style.css` | Add all Multi Shell styles (overlay, header bar, grid, pane titlebar, server dropdown, picker dropdown) |
| `public/js/app.js` | Paste fix in `openShell()`; add `el.btnMultiShell` to `initElements`; add Multi Shell state + all lifecycle functions; update `switchView()` |

---

## Task 1: Paste Focus Fix

**Files:**
- Modify: `public/js/app.js` — inside `openShell()`, after `term.open(...)`

- [ ] **Step 1: Locate the insertion point**

Open `public/js/app.js`. Find the block starting at line ~922 that reads:
```js
term.open(document.getElementById('shell-body-' + sessionId));
fitAddon.fit();
```

- [ ] **Step 2: Add the two focus-restoration listeners**

Immediately after `fitAddon.fit();`, add:
```js
    // Restore PTY focus after paste/click so arrow keys send escape sequences
    // instead of triggering browser text selection (bôi trắng bug)
    const shellBodyEl = document.getElementById('shell-body-' + sessionId);
    shellBodyEl.addEventListener('paste', () => requestAnimationFrame(() => term.focus()));
    shellBodyEl.addEventListener('mouseup', () => term.focus());
```

- [ ] **Step 3: Manual verify — paste + edit works**

```bash
go run main.go
```
Open `http://localhost:8080`, log in, open a Shell on any server. Paste a long command (e.g. `echo hello_world_test`), then press ← arrow key. Cursor should move left one character — no white highlighting of text.

- [ ] **Step 4: Commit**

```bash
git add public/js/app.js
git commit -m "fix: restore xterm focus after paste so arrow keys move cursor not select text"
```

---

## Task 2: HTML Skeleton

**Files:**
- Modify: `public/index.html`

- [ ] **Step 1: Add Multi Shell button + picker to header-actions**

Find the `<div class="header-actions">` block (line ~117). It currently contains only the "Add New Server" button. Add the Multi Shell wrapper **after** that button:

```html
<div class="header-actions">
    <button class="btn btn-primary" id="btn-add-server-trigger">
        <i class="fa-solid fa-plus"></i>
        <span>Add New Server</span>
    </button>
    <div class="ms-picker-wrapper hidden" id="ms-picker-wrapper">
        <button class="btn btn-secondary" id="btn-multi-shell" onclick="toggleMultiShellPicker()">
            <i class="fa-solid fa-table-cells"></i>
            <span id="ms-btn-label">Multi Shell</span>
            <i class="fa-solid fa-chevron-down" id="ms-chevron" style="font-size:0.7rem;margin-left:2px"></i>
        </button>
        <div class="ms-picker-dropdown hidden" id="ms-picker-dropdown">
            <button onclick="openMultiShell(2)"><i class="fa-solid fa-table-columns"></i> 2 Shells</button>
            <button onclick="openMultiShell(4)"><i class="fa-solid fa-table-cells-large"></i> 4 Shells</button>
            <button onclick="openMultiShell(8)"><i class="fa-solid fa-border-all"></i> 8 Shells</button>
        </div>
    </div>
</div>
```

Note: `ms-picker-wrapper` starts with `hidden` — `switchView()` will reveal it on the Servers page (Task 6).

- [ ] **Step 2: Add overlay div before #shell-windows**

Find line ~603: `<div id="shell-windows"></div>`. Insert the overlay **before** that line:

```html
<!-- Multi Shell fullscreen overlay -->
<div id="multi-shell-overlay" class="hidden">
    <div class="ms-header">
        <div class="ms-header-info">
            <i class="fa-solid fa-table-cells"></i>
            <span style="font-weight:600;color:var(--text-primary)">Multi Shell</span>
            <span class="ms-meta" id="ms-pane-count"></span>
            <span class="ms-meta" id="ms-connected-count"></span>
        </div>
        <div class="ms-header-controls">
            <button class="ms-ctrl-btn" onclick="minimizeMultiShell()">− Minimize</button>
            <button class="ms-ctrl-btn ms-ctrl-close" onclick="closeMultiShell()">✕ Close</button>
        </div>
    </div>
    <div class="ms-grid" id="ms-grid"></div>
</div>
<div id="shell-windows"></div>
```

- [ ] **Step 3: Commit**

```bash
git add public/index.html
git commit -m "feat(html): add Multi Shell button, picker dropdown, and overlay skeleton"
```

---

## Task 3: CSS — All Multi Shell Styles

**Files:**
- Modify: `public/css/style.css` — append at end of file

- [ ] **Step 1: Append all Multi Shell CSS**

At the very end of `public/css/style.css`, add:

```css
/* ==========================================================================
   MULTI SHELL OVERLAY
   ========================================================================== */

/* Picker button wrapper (shown only on Servers page via switchView) */
.ms-picker-wrapper {
    position: relative;
}

.ms-picker-dropdown {
    position: absolute;
    top: calc(100% + 6px);
    right: 0;
    min-width: 160px;
    background: rgba(17, 24, 39, 0.98);
    border: 1px solid rgba(59, 130, 246, 0.3);
    border-radius: 10px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
    z-index: 3000;
    overflow: hidden;
}

.ms-picker-dropdown button {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 10px 16px;
    background: none;
    border: none;
    color: var(--text-primary);
    font-size: 0.85rem;
    cursor: pointer;
    text-align: left;
    transition: background var(--transition-fast);
}

.ms-picker-dropdown button:hover {
    background: rgba(59, 130, 246, 0.15);
}

/* Fullscreen overlay */
#multi-shell-overlay {
    position: fixed;
    inset: 0;
    z-index: 2000;
    background: rgba(8, 12, 20, 0.99);
    display: flex;
    flex-direction: column;
}

#multi-shell-overlay.hidden {
    display: none;
}

/* Overlay header bar */
.ms-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 16px;
    height: 44px;
    min-height: 44px;
    background: rgba(17, 24, 39, 0.95);
    border-bottom: 1px solid rgba(59, 130, 246, 0.2);
    flex-shrink: 0;
    gap: 12px;
}

.ms-header-info {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 0.9rem;
    color: var(--text-muted);
}

.ms-meta {
    font-size: 0.78rem;
    color: var(--text-muted);
}

.ms-meta:not(:empty)::before {
    content: '•';
    margin-right: 8px;
}

.ms-header-controls {
    display: flex;
    align-items: center;
    gap: 8px;
}

.ms-ctrl-btn {
    background: rgba(255, 255, 255, 0.06);
    border: 1px solid rgba(255, 255, 255, 0.1);
    color: var(--text-primary);
    border-radius: 6px;
    padding: 5px 14px;
    font-size: 0.82rem;
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
}

.ms-ctrl-btn:hover {
    background: rgba(255, 255, 255, 0.12);
}

.ms-ctrl-close:hover {
    background: rgba(239, 68, 68, 0.2);
    border-color: rgba(239, 68, 68, 0.4);
}

/* Pane grid */
.ms-grid {
    flex: 1;
    display: grid;
    gap: 2px;
    background: #000;
    overflow: hidden;
}

.ms-grid[data-panes="2"] {
    grid-template-columns: 1fr 1fr;
    grid-template-rows: 1fr;
}

.ms-grid[data-panes="4"] {
    grid-template-columns: 1fr 1fr;
    grid-template-rows: 1fr 1fr;
}

.ms-grid[data-panes="8"] {
    grid-template-columns: 1fr 1fr 1fr 1fr;
    grid-template-rows: 1fr 1fr;
}

/* Individual pane */
.ms-pane {
    display: flex;
    flex-direction: column;
    background: rgba(11, 15, 25, 0.98);
    overflow: hidden;
    min-width: 0;
    min-height: 0;
}

.ms-pane-titlebar {
    display: flex;
    align-items: center;
    padding: 0 8px;
    height: 32px;
    min-height: 32px;
    background: rgba(17, 24, 39, 0.92);
    border-bottom: 1px solid rgba(255, 255, 255, 0.06);
    flex-shrink: 0;
    gap: 6px;
}

.ms-pane-label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-muted);
    font-family: var(--font-heading);
    white-space: nowrap;
    flex-shrink: 0;
}

/* Server picker inside pane titlebar */
.ms-server-dropdown-wrap {
    position: relative;
    flex: 1;
    min-width: 0;
}

.ms-pane-server-btn {
    width: 100%;
    background: rgba(59, 130, 246, 0.08);
    border: 1px solid rgba(59, 130, 246, 0.2);
    color: var(--text-muted);
    border-radius: 5px;
    padding: 3px 10px;
    font-size: 0.75rem;
    cursor: pointer;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    text-align: left;
    transition: background var(--transition-fast), color var(--transition-fast);
}

.ms-pane-server-btn:hover {
    background: rgba(59, 130, 246, 0.18);
    color: var(--text-primary);
}

.ms-pane-server-btn.connected {
    color: #3fb950;
    border-color: rgba(63, 185, 80, 0.3);
    background: rgba(63, 185, 80, 0.07);
}

/* Server picker dropdown */
.ms-server-dropdown {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    min-width: 200px;
    max-height: 260px;
    overflow-y: auto;
    background: rgba(17, 24, 39, 0.98);
    border: 1px solid rgba(59, 130, 246, 0.25);
    border-radius: 8px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
    z-index: 2100;
}

.ms-server-dropdown-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 14px;
    font-size: 0.8rem;
    color: var(--text-primary);
    cursor: pointer;
    transition: background var(--transition-fast);
    white-space: nowrap;
}

.ms-server-dropdown-item:hover {
    background: rgba(59, 130, 246, 0.15);
}

.ms-server-dropdown-item.offline {
    color: var(--text-muted);
    cursor: not-allowed;
    opacity: 0.5;
}

.ms-server-dropdown-item.offline:hover {
    background: none;
}

/* Pane close button */
.ms-pane-close-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 2px 5px;
    border-radius: 3px;
    font-size: 0.8rem;
    line-height: 1;
    flex-shrink: 0;
    transition: color var(--transition-fast), background var(--transition-fast);
}

.ms-pane-close-btn:hover {
    color: #ef4444;
    background: rgba(239, 68, 68, 0.12);
}

/* Pane terminal body */
.ms-pane-body {
    flex: 1;
    overflow: hidden;
    min-height: 0;
}

.ms-pane-body .xterm {
    height: 100%;
}

.ms-pane-body .xterm-viewport {
    overflow-y: auto !important;
}

/* Multi Shell taskbar item variant */
.taskbar-item.ms-taskbar-item {
    background: rgba(59, 130, 246, 0.1);
    border-color: rgba(59, 130, 246, 0.3);
}
```

- [ ] **Step 2: Verify styles load**

```bash
go run main.go
```
Open `http://localhost:8080`, navigate to Servers page. Open DevTools → Network tab → confirm `style.css` loads without parse errors. The Multi Shell button is not visible yet (added in Task 6).

- [ ] **Step 3: Commit**

```bash
git add public/css/style.css
git commit -m "feat(css): add Multi Shell overlay, grid, pane, and picker styles"
```

---

## Task 4: JS — State Object + Overlay Lifecycle

**Files:**
- Modify: `public/js/app.js`
  - `initElements()` — add `el.btnMultiShell`
  - After the PTY SHELL WINDOWS section — add `ms` state + all lifecycle functions

- [ ] **Step 1: Add el.btnMultiShell to initElements**

In `initElements()`, find the last entry of the `el = { ... }` object (line ~145, `terminalSuggestions`). Add before the closing `};`:

```js
        // Multi Shell
        btnMultiShell: document.getElementById('ms-picker-wrapper'),
```

- [ ] **Step 2: Add Multi Shell state and lifecycle functions**

Find the end of the PTY shell section in `app.js` — after the `makeDraggable` function (around line ~1050). Insert the following block:

```js
// =========================================================================
// MULTI SHELL OVERLAY
// =========================================================================

// ms: singleton state for the Multi Shell overlay
const ms = {
    active: false,
    minimized: false,
    paneCount: 0,
    // Each entry: { sessionId, serverId, serverName, term, ws, fitAddon, connected, observer }
    panes: [],
    taskbarItemEl: null,
};

window.toggleMultiShellPicker = function() {
    if (ms.active) {
        if (ms.minimized) restoreMultiShell();
        return;
    }
    const dd = document.getElementById('ms-picker-dropdown');
    const isHidden = dd.classList.contains('hidden');
    dd.classList.toggle('hidden');
    if (isHidden) {
        const close = (e) => {
            if (!e.target.closest('#ms-picker-wrapper')) {
                dd.classList.add('hidden');
                document.removeEventListener('mousedown', close);
            }
        };
        setTimeout(() => document.addEventListener('mousedown', close), 0);
    }
};

window.openMultiShell = function(count) {
    document.getElementById('ms-picker-dropdown').classList.add('hidden');
    if (ms.active) return;

    ms.paneCount = count;
    ms.panes = Array.from({ length: count }, () => ({
        sessionId: null, serverId: null, serverName: null,
        term: null, ws: null, fitAddon: null, connected: false, observer: null,
    }));
    ms.active = true;
    ms.minimized = false;

    const overlay = document.getElementById('multi-shell-overlay');
    overlay.classList.remove('hidden');

    const grid = document.getElementById('ms-grid');
    grid.setAttribute('data-panes', count);
    grid.innerHTML = Array.from({ length: count }, (_, i) => buildMsPaneHTML(i)).join('');

    updateMsHeader();
    updateMsButton();
};

function buildMsPaneHTML(i) {
    return `
    <div class="ms-pane" id="ms-pane-${i}">
        <div class="ms-pane-titlebar">
            <span class="ms-pane-label">Pane ${i + 1}</span>
            <div class="ms-server-dropdown-wrap" id="ms-pane-select-wrap-${i}">
                <button class="ms-pane-server-btn" onclick="togglePaneDropdown(${i})">
                    Select a server… ▾
                </button>
                <div class="ms-server-dropdown hidden" id="ms-pane-dropdown-${i}"></div>
            </div>
            <button class="ms-pane-close-btn" onclick="closePaneShell(${i})" title="Close pane session">✕</button>
        </div>
        <div class="ms-pane-body" id="ms-pane-body-${i}"></div>
    </div>`;
}

window.minimizeMultiShell = function() {
    document.getElementById('multi-shell-overlay').classList.add('hidden');
    ms.minimized = true;

    const connCount = ms.panes.filter(p => p.connected).length;
    const item = document.createElement('div');
    item.className = 'taskbar-item ms-taskbar-item';
    item.id = 'ms-taskbar-item';
    item.innerHTML = `
        <i class="fa-solid fa-table-cells"></i>
        <span>Multi Shell (${connCount}/${ms.paneCount})</span>
        <button class="taskbar-restore" onclick="restoreMultiShell()">▲</button>
        <button class="taskbar-close" onclick="closeMultiShell()">✕</button>`;
    document.getElementById('shell-taskbar').appendChild(item);
    ms.taskbarItemEl = item;
    updateMsButton();
};

window.restoreMultiShell = function() {
    document.getElementById('multi-shell-overlay').classList.remove('hidden');
    ms.minimized = false;
    ms.taskbarItemEl?.remove();
    ms.taskbarItemEl = null;
    setTimeout(() => ms.panes.forEach(p => { if (p.fitAddon) p.fitAddon.fit(); }), 50);
    updateMsButton();
};

window.closeMultiShell = function() {
    ms.panes.forEach((_, i) => closePaneShell(i));
    document.getElementById('multi-shell-overlay').classList.add('hidden');
    document.getElementById('ms-grid').innerHTML = '';
    ms.taskbarItemEl?.remove();
    ms.taskbarItemEl = null;
    ms.active = false;
    ms.minimized = false;
    ms.panes = [];
    updateMsButton();
};

function updateMsButton() {
    const wrapper = el.btnMultiShell;
    if (!wrapper) return;
    const label = document.getElementById('ms-btn-label');
    const chevron = document.getElementById('ms-chevron');
    if (ms.active) {
        if (label) label.textContent = 'Restore Multi Shell';
        if (chevron) chevron.style.display = 'none';
    } else {
        if (label) label.textContent = 'Multi Shell';
        if (chevron) chevron.style.display = '';
    }
}

function updateMsHeader() {
    const connCount = ms.panes.filter(p => p.connected).length;
    const paneCountEl = document.getElementById('ms-pane-count');
    const connCountEl = document.getElementById('ms-connected-count');
    if (paneCountEl) paneCountEl.textContent = `${ms.paneCount} panes`;
    if (connCountEl) connCountEl.textContent = `${connCount} connected`;
    if (ms.taskbarItemEl) {
        const span = ms.taskbarItemEl.querySelector('span');
        if (span) span.textContent = `Multi Shell (${connCount}/${ms.paneCount})`;
    }
}
```

- [ ] **Step 3: Verify no JS errors**

```bash
go run main.go
```
Open `http://localhost:8080`, open browser DevTools Console. Confirm no errors on page load. `ms` object exists (`console.log(ms)` in console should work since it's module-scoped — if not accessible from console, that's fine, just check for no errors).

- [ ] **Step 4: Commit**

```bash
git add public/js/app.js
git commit -m "feat(js): add Multi Shell state object and overlay lifecycle (open/minimize/restore/close)"
```

---

## Task 5: JS — Pane Server Picker + Shell Connection

**Files:**
- Modify: `public/js/app.js` — append after the `updateMsHeader` function added in Task 4

- [ ] **Step 1: Add pane shell connection functions**

Immediately after `updateMsHeader()` in app.js, add:

```js
window.togglePaneDropdown = function(paneIndex) {
    const dd = document.getElementById(`ms-pane-dropdown-${paneIndex}`);
    const isHidden = dd.classList.contains('hidden');
    if (!isHidden) { dd.classList.add('hidden'); return; }

    const now = Date.now();
    dd.innerHTML = state.servers.length === 0
        ? `<div class="ms-server-dropdown-item offline">No servers registered</div>`
        : state.servers.map(s => {
            const online = s.connected && (now - new Date(s.last_report)) < 15000;
            const onclick = online ? `connectPaneShell(${paneIndex},'${s.id}','${escHTML(s.name)}')` : '';
            return `<div class="ms-server-dropdown-item ${online ? '' : 'offline'}"
                ${online ? `onclick="${onclick}"` : ''}
                title="${online ? s.name : 'Server offline'}">
                <span style="font-size:0.65rem">${online ? '●' : '○'}</span>
                ${escHTML(s.name)}
            </div>`;
        }).join('');

    dd.classList.remove('hidden');
    const close = (e) => {
        if (!e.target.closest(`#ms-pane-select-wrap-${paneIndex}`)) {
            dd.classList.add('hidden');
            document.removeEventListener('mousedown', close);
        }
    };
    setTimeout(() => document.addEventListener('mousedown', close), 0);
};

window.connectPaneShell = function(paneIndex, serverId, serverName) {
    document.getElementById(`ms-pane-dropdown-${paneIndex}`)?.classList.add('hidden');

    const pane = ms.panes[paneIndex];
    if (!pane) return;

    // Tear down any existing session in this pane
    if (pane.ws) pane.ws.close();
    if (pane.term) pane.term.dispose();
    if (pane.observer) pane.observer.disconnect();
    pane.connected = false;
    pane.serverId = serverId;
    pane.serverName = serverName;

    const sessionId = genSessionId();
    pane.sessionId = sessionId;

    // Update the picker button text optimistically
    const btn = document.querySelector(`#ms-pane-select-wrap-${paneIndex} .ms-pane-server-btn`);
    if (btn) { btn.textContent = `${serverName} ▾`; btn.classList.remove('connected'); }

    // Mount xterm.js into the pane body
    const bodyEl = document.getElementById(`ms-pane-body-${paneIndex}`);
    bodyEl.innerHTML = '';

    const term = new Terminal({
        theme: {
            background: '#0d1117', foreground: '#e6edf3',
            cursor: '#58a6ff', cursorAccent: '#0d1117',
            selectionBackground: '#264f7855',
            black: '#484f58', brightBlack: '#6e7681',
            red: '#ff7b72', brightRed: '#ffa198',
            green: '#3fb950', brightGreen: '#56d364',
            yellow: '#d29922', brightYellow: '#e3b341',
            blue: '#58a6ff', brightBlue: '#79c0ff',
            magenta: '#bc8cff', brightMagenta: '#d2a8ff',
            cyan: '#39c5cf', brightCyan: '#56d4dd',
            white: '#b1bac4', brightWhite: '#f0f6fc',
        },
        fontFamily: '"Cascadia Code","Fira Code","JetBrains Mono","Consolas",monospace',
        fontSize: 13,
        lineHeight: 1.4,
        cursorBlink: true,
        scrollback: 5000,
        allowTransparency: false,
    });

    const fitAddon = new FitAddon.FitAddon();
    term.loadAddon(fitAddon);
    term.open(bodyEl);
    fitAddon.fit();
    pane.term = term;
    pane.fitAddon = fitAddon;

    // Paste + mouseup focus fix (same as Task 1, applied to pane bodies too)
    bodyEl.addEventListener('paste', () => requestAnimationFrame(() => term.focus()));
    bodyEl.addEventListener('mouseup', () => term.focus());

    // Open WebSocket — host bash shell (no container)
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/api/servers/shell/${serverId}/ws/${sessionId}`);
    ws.binaryType = 'arraybuffer';
    pane.ws = ws;

    ws.onmessage = (ev) => {
        if (typeof ev.data === 'string') {
            try {
                const msg = JSON.parse(ev.data);
                if (msg.type === 'ready') {
                    pane.connected = true;
                    const b = document.querySelector(`#ms-pane-select-wrap-${paneIndex} .ms-pane-server-btn`);
                    if (b) b.classList.add('connected');
                    term.focus();
                    updateMsHeader();
                } else if (msg.type === 'error') {
                    term.write('\r\n\x1b[31m✖ ' + (msg.message || 'Connection error') + '\x1b[0m\r\n');
                }
            } catch (_) {}
        } else {
            term.write(new Uint8Array(ev.data));
        }
    };

    ws.onclose = () => {
        pane.connected = false;
        term.write('\r\n\x1b[33m[session closed]\x1b[0m\r\n');
        const b = document.querySelector(`#ms-pane-select-wrap-${paneIndex} .ms-pane-server-btn`);
        if (b) b.classList.remove('connected');
        updateMsHeader();
    };

    ws.onerror = () => {
        pane.connected = false;
        updateMsHeader();
    };

    const enc = new TextEncoder();
    term.onData(data => { if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(data).buffer); });
    term.onResize(({ rows, cols }) => {
        if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', rows, cols }));
    });

    const obs = new ResizeObserver(() => { if (pane.fitAddon) pane.fitAddon.fit(); });
    obs.observe(bodyEl);
    pane.observer = obs;

    updateMsHeader();
};

window.closePaneShell = function(paneIndex) {
    const pane = ms.panes[paneIndex];
    if (!pane) return;
    if (pane.ws) pane.ws.close();
    if (pane.term) pane.term.dispose();
    if (pane.observer) pane.observer.disconnect();
    pane.sessionId = null; pane.serverId = null; pane.serverName = null;
    pane.term = null; pane.ws = null; pane.fitAddon = null;
    pane.connected = false; pane.observer = null;

    const bodyEl = document.getElementById(`ms-pane-body-${paneIndex}`);
    if (bodyEl) bodyEl.innerHTML = '';
    const btn = document.querySelector(`#ms-pane-select-wrap-${paneIndex} .ms-pane-server-btn`);
    if (btn) { btn.textContent = 'Select a server… ▾'; btn.classList.remove('connected'); }
    updateMsHeader();
};
```

- [ ] **Step 2: Manual verify — pane connects to a server**

With `go run main.go` still running:
1. Navigate to Servers page (Multi Shell button is still hidden — that's expected, Task 6 enables it)
2. In DevTools Console, run:
   ```js
   openMultiShell(2)
   ```
3. The fullscreen overlay should appear with 2 panes.
4. Click "Select a server… ▾" on any pane → dropdown lists servers.
5. Click an online server → terminal appears and shows "Connecting…" → changes to green "Connected" text on the picker button.
6. Type a command in the terminal (e.g. `hostname`) → output appears.
7. Click Minimize → overlay hides, taskbar shows `Multi Shell (1/2)`.
8. Click `▲` in taskbar → overlay restores, terminal still alive.
9. Click `✕ Close` → overlay closes, taskbar item gone.

- [ ] **Step 3: Commit**

```bash
git add public/js/app.js
git commit -m "feat(js): add Multi Shell pane server picker and shell connection logic"
```

---

## Task 6: JS — Wire Multi Shell Button via switchView

**Files:**
- Modify: `public/js/app.js` — `switchView()` function

- [ ] **Step 1: Show/hide Multi Shell button in switchView**

In `switchView()`, locate the existing block that shows/hides `el.btnAddServerTrigger` for each page. The current code looks like:

```js
if (viewId === 'page-dashboard') {
    ...
    el.btnAddServerTrigger.classList.remove('hidden');
} else if (viewId === 'page-servers') {
    ...
    el.btnAddServerTrigger.classList.remove('hidden');
} else if (viewId === 'page-server-detail') {
    ...
    el.btnAddServerTrigger.classList.add('hidden');
} else if (viewId === 'page-containers') {
    ...
    el.btnAddServerTrigger.classList.add('hidden');
}
```

Replace this entire block with:

```js
    if (viewId === 'page-dashboard') {
        el.pageTitle.innerText = 'Infrastructure Dashboard';
        el.pageSubtitle.innerText = 'Real-time telemetry, active host statuses, and metric aggregations.';
        el.btnAddServerTrigger.classList.remove('hidden');
        el.btnMultiShell.classList.add('hidden');
    } else if (viewId === 'page-servers') {
        el.pageTitle.innerText = 'Servers Inventory';
        el.pageSubtitle.innerText = 'Comprehensive server registry, specs, and status actions.';
        el.btnAddServerTrigger.classList.remove('hidden');
        el.btnMultiShell.classList.remove('hidden');
    } else if (viewId === 'page-server-detail') {
        el.pageTitle.innerText = 'Telemetry Deep-Dive';
        el.pageSubtitle.innerText = 'Granular performance stats, memory analytics, and uptime records.';
        el.btnAddServerTrigger.classList.add('hidden');
        el.btnMultiShell.classList.add('hidden');
    } else if (viewId === 'page-containers') {
        el.pageTitle.innerText = 'Containers';
        el.pageSubtitle.innerText = 'Docker and Kubernetes containers across all monitored servers.';
        el.btnAddServerTrigger.classList.add('hidden');
        el.btnMultiShell.classList.add('hidden');
    }
```

- [ ] **Step 2: Bump cache-busting version**

In `public/index.html`, find:
```html
<link rel="stylesheet" href="/css/style.css?v=4">
```
and
```html
<script src="/js/app.js?v=4"></script>
```

Change both to `?v=5`.

- [ ] **Step 3: Full end-to-end manual test**

```bash
go run main.go
```

Open `http://localhost:8080`, log in and verify:

1. **Dashboard page** — Multi Shell button NOT visible, Add New Server button visible.
2. **Navigate to Servers** — Multi Shell button appears next to Add New Server.
3. **Click Multi Shell** → picker dropdown shows "2 Shells / 4 Shells / 8 Shells".
4. **Choose 4 Shells** → fullscreen overlay opens with 4 empty panes in 2×2 grid.
5. **Header shows**: "Multi Shell • 4 panes • 0 connected".
6. **Click "Select a server…" on pane 1** → dropdown lists all servers (online ones clickable, offline ones dimmed).
7. **Select an online server** → terminal appears, status becomes "Connected", header updates to "1 connected".
8. **Type `hostname` + Enter** → output shown correctly.
9. **Paste a long command then press ←** → cursor moves, no white highlight.
10. **Open pane 2 on a different server** → both terminals independent and live.
11. **Click "− Minimize"** → overlay hides, taskbar shows `⊞ Multi Shell (2/4) ▲ ✕`.
12. **Click ▲ restore** → overlay back, both terminals still responsive.
13. **Navigate to Containers page** → Multi Shell button hidden, overlay still accessible via taskbar.
14. **Click ✕ in taskbar** → everything closed, button back to "Multi Shell".
15. **Back on Servers page, open 2 Shells** → button now shows "Restore Multi Shell" while active.
16. **Paste fix** → open any single shell, paste `echo hello`, press ← → cursor moves correctly.

- [ ] **Step 4: Commit**

```bash
git add public/js/app.js public/index.html
git commit -m "feat(js): show Multi Shell button on Servers page; bump CSS/JS cache to v5"
```

---

## Task 7: Deploy to Production + Verify

**Files:** No changes — deploy what's on disk.

- [ ] **Step 1: Build server binary**

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o mgnt-server .
```

Expected: binary `mgnt-server` written, no errors.

- [ ] **Step 2: Build agent binaries**

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o public/downloads/agent-linux-amd64 ./cmd/agent/
CGO_ENABLED=0 GOOS=linux GOARCH=arm64  go build -ldflags="-w -s" -o public/downloads/agent-linux-arm64  ./cmd/agent/
```

Expected: both binaries written to `public/downloads/`, no errors.

- [ ] **Step 3: Deploy to .195 (do NOT sync data/)**

```bash
rsync -az mgnt-server ubuntu@172.25.155.195:~/mgnt-server/
rsync -az public/ ubuntu@172.25.155.195:~/mgnt-server/public/
ssh ubuntu@172.25.155.195 "
  sudo docker stop secure-server-manager
  sudo docker cp ~/mgnt-server/mgnt-server secure-server-manager:/app/mgnt-server
  sudo docker cp ~/mgnt-server/public secure-server-manager:/app/
  sudo docker start secure-server-manager
"
```

Expected: container restarts cleanly (no exit errors).

- [ ] **Step 4: Verify on production**

Open `http://172.25.155.195:8080`, log in. Run through the checklist:
1. Navigate to Servers → Multi Shell button visible.
2. Open 4 Shells → overlay appears.
3. Connect pane 1 to `vcr-db-hni` → terminal connects and shows prompt.
4. Connect pane 2 to `vcr-db-hcm` → second terminal connects independently.
5. Paste a command in pane 1, press ← → cursor moves (no white highlight).
6. Minimize → taskbar item appears.
7. Restore → both terminals still connected.
8. Close → overlay gone.

- [ ] **Step 5: Push to dev and update PR**

```bash
git push origin dev
```

The existing PR #2 (dev → main) will pick up these commits automatically.

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|-----------------|------|
| Multi Shell button on Server List header | Task 2 + Task 6 |
| Dropdown picker: 2 / 4 / 8 panes | Task 2 + Task 4 |
| Tiled fullscreen grid (2×1, 2×2, 4×2) | Task 3 (CSS grid) + Task 4 (JS) |
| Pane: server dropdown, only online servers | Task 5 (`togglePaneDropdown`) |
| Pane: Change server button (green when connected) | Task 5 (`ms-pane-server-btn.connected`) |
| Pane ✕ resets pane, not overlay | Task 5 (`closePaneShell`) |
| Overlay header: pane count + connected count | Task 4 (`updateMsHeader`) |
| Minimize → taskbar, WebSockets alive | Task 4 (`minimizeMultiShell`) |
| Restore from taskbar, fitAddon.fit() on all panes | Task 4 (`restoreMultiShell`) |
| Close → all sessions closed | Task 4 (`closeMultiShell`) |
| Only 1 Multi Shell session at a time | Task 4 (`if (ms.active) return`) |
| Button changes to "Restore Multi Shell" when active | Task 4 (`updateMsButton`) |
| Paste focus fix in regular shells | Task 1 |
| Paste focus fix in Multi Shell panes | Task 5 (same listeners) |
| No backend changes | Confirmed — all changes are frontend |
| Deploy: no data/ sync | Task 7 Step 3 |

**Placeholder scan:** No TBD, no TODO, all code is complete.

**Type consistency:** `ms.panes[i]` object shape is defined once in `openMultiShell` (Task 4) and read consistently in `connectPaneShell`, `closePaneShell`, `minimizeMultiShell`, `closeMultiShell` (all Task 5 or Task 4). `el.btnMultiShell` points to `#ms-picker-wrapper` — added in Task 2 HTML and read in Task 6. `genSessionId()` and `state.servers` exist in the codebase prior to this feature.
