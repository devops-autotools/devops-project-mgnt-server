# Design: Multi Shell Overlay + Paste Focus Fix

**Date:** 2026-05-25
**Status:** Approved
**Scope:** Frontend only — `public/js/app.js`, `public/css/style.css`, `public/index.html`

---

## Overview

Two independent improvements to the shell experience in mgnt-server:

1. **Multi Shell Overlay** — open 2, 4, or 8 tiled terminal panes simultaneously in a fullscreen overlay, each independently connected to a chosen server.
2. **Paste Focus Fix** — prevent arrow keys from triggering browser text selection instead of moving the PTY cursor after a paste operation.

---

## Feature 1: Multi Shell Overlay

### Entry Point

A `⊞ Multi Shell ▾` button is added to the workspace header, visible when the Server List page is active (alongside the existing "Add New Server" button). Clicking it opens a small dropdown:

```
[ ⊞ Multi Shell ▾ ]
  ┌─────────────────┐
  │  ⊞  2 Shells    │
  │  ⊞  4 Shells    │
  │  ⊞  8 Shells    │
  └─────────────────┘
```

Selecting a count immediately opens the fullscreen overlay with N empty panes. Only one Multi Shell session may exist at a time. If one already exists (even minimized), the button instead shows "Restore Multi Shell" and clicking it restores the overlay.

### Layout Grid

The overlay uses CSS Grid with layouts determined by pane count:

| Count | Grid          |
|-------|---------------|
| 2     | 2 cols × 1 row |
| 4     | 2 cols × 2 rows |
| 8     | 4 cols × 2 rows |

All panes are equal-sized. Per-pane resizing is not supported (keeps implementation simple).

### Overlay Header Bar

A fixed bar at the top of the overlay:

```
⊞ Multi Shell  •  4 panes  •  3 connected        [−]  [✕ Close]
```

- **[−] Minimize**: Hides the overlay, adds a taskbar item `⊞ Multi Shell (3/4) ▲ [✕]`. All WebSocket connections remain alive.
- **[✕ Close]**: Closes all pane WebSocket sessions, destroys the overlay and its taskbar item.
- The connected count (e.g. `3/4`) is updated live as panes connect or disconnect.

### Pane UI

Each pane has a titlebar and a terminal body.

**Empty state** (no server selected):
```
┌──────────────────────────────────────────────────────┐
│ ⊞ Pane N                                        [✕] │
├──────────────────────────────────────────────────────┤
│                                                      │
│           ┌─────────────────────────┐               │
│           │  Select a server...  ▾  │               │
│           └─────────────────────────┘               │
│                                                      │
└──────────────────────────────────────────────────────┘
```

**Connected state**:
```
┌──────────────────────────────────────────────────────┐
│ ● vcr-db-hni  [Change ▾]  [Connected]          [✕] │
├──────────────────────────────────────────────────────┤
│                                                      │
│   xterm.js terminal running here                     │
│                                                      │
└──────────────────────────────────────────────────────┘
```

**Server dropdown** lists only online servers (sourced from `state.servers`). Offline servers are shown dimmed and are not selectable. Choosing a server on an already-connected pane closes the existing WebSocket session and opens a new one in the same pane.

**Pane [✕]** closes only that pane's session and resets it to empty state — it does not close the Multi Shell overlay.

### Shell Session Reuse

Each pane reuses the core `openShell()` logic:
- Generates a `sessionId` via `genSessionId()`
- Creates an xterm.js `Terminal` instance with the existing theme/font config
- Connects to `wss://<host>/api/servers/shell/<serverId>/ws/<sessionId>` (host shell, no container)
- Registers the session in `shellSessions` Map

The key difference from the existing `openShell()`: instead of creating a new floating `div.shell-window` appended to `#shell-windows`, the xterm.js terminal is opened directly inside the pane's body div (`#ms-pane-body-<paneIndex>`). No draggable window is created.

### Restore Behavior

When the overlay is restored from the taskbar:
- The overlay div is made visible again
- `fitAddon.fit()` is called on all connected pane terminals after a short delay (50ms) to re-sync PTY dimensions with the now-visible container

### Z-Index

The Multi Shell overlay sits at `z-index: 2000` — above regular floating shell windows (`z-index: 1000+`). Regular shells remain accessible when the overlay is minimized.

---

## Feature 2: Paste Focus Fix

### Root Cause

After a paste event (Ctrl+V or right-click → Paste), the browser's native clipboard handler temporarily captures keyboard focus away from the xterm.js canvas. When the user then presses ← or → to edit the pasted command, the browser interprets these as text selection keys, highlighting the terminal content (white selection overlay) instead of sending `\x1b[D` / `\x1b[C` escape sequences to the PTY.

### Fix

Two event listeners added to the `shell-body` element inside `openShell()`, after `term.open()`:

```js
const body = document.getElementById('shell-body-' + sessionId);

// Restore focus after paste so arrow keys go to PTY, not browser selection
body.addEventListener('paste', () => requestAnimationFrame(() => term.focus()));

// Restore focus after any click inside the terminal area
body.addEventListener('mouseup', () => term.focus());
```

`requestAnimationFrame` defers the `focus()` call until after the browser finishes processing the clipboard event, ensuring the terminal reliably recaptures keyboard input.

### Scope

This fix applies to:
- All regular floating shell windows (added inside `openShell()`)
- All Multi Shell panes (same `openShell()` core path is reused)

No backend changes required.

---

## Files Changed

| File | Change |
|------|--------|
| `public/index.html` | Add `⊞ Multi Shell ▾` button + dropdown to workspace header; add `#multi-shell-overlay` div |
| `public/css/style.css` | Add styles for overlay, overlay header bar, pane grid, pane titlebar, server dropdown, taskbar item variant |
| `public/js/app.js` | Add `openMultiShell()`, `minimizeMultiShell()`, `closeMultiShell()`, `restoreMultiShell()`, per-pane server picker logic; add paste/mouseup focus fix in `openShell()` |

---

## Constraints

- **One external Go package rule**: This feature is frontend-only — no new Go dependencies.
- **No frontend build step**: All JS/CSS is plain, no npm or bundler.
- **No backend changes**: Multi Shell uses the same WebSocket endpoints as regular shell sessions. The server already supports concurrent sessions.
- **Max 8 panes**: Enforced at the picker UI level — no option beyond 8.
