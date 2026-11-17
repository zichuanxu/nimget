# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The desktop app is a Wails v3 window rendering a React UI in the system WebView
(WebView2 on Windows, WKWebView on macOS). The browser extension popup and pages
are plain HTML/CSS/JS in Chrome and Edge.

## Users

Power users who have outgrown the browser's download bar: people moving to NimGet
from IDM, aria2, or Free Download Manager. They queue large files (ISOs, installers,
videos, HLS streams) and leave the window in the background or the tray. They come
back to glance at total speed, progress, and anything that failed, then leave again.
Occasional users exist but are secondary.

## Product Purpose

A free, open-source download manager that is fast (up to 32 connections, rebalanced
as they finish), resumes at byte granularity even after a crash, and never loses a
queued download. Success: a user hands off a link and stops thinking about it until
the file is on disk, or until NimGet tells them, in plain words, what it needs.

## Positioning

Work-stealing segmentation with byte-level checkpoints, verified against aria2c in a
benchmark gate; a loopback-only service with token, Host, and Origin checks; and a
paired browser extension that cancels the browser's download only after NimGet
returns a durable task receipt. No accounts, no hosted services, no telemetry.

## Operating Context

- The window is opened from the tray, by the extension handoff, by drag-and-drop, or
  by the clipboard monitor offering a copied URL or cURL command.
- Task lifecycle: queued, running, paused, completed, failed, plus recovery states
  that ask the user to re-authenticate by pasting a cURL command again.
- Keyboard-first operation exists: Ctrl/Cmd+N add, Ctrl/Cmd+, settings, Ctrl/Cmd+F
  search, arrows move, Space pauses or resumes, Enter opens, Delete removes.
- Queues can reach thousands of tasks; the list is paged at 50 rows.
- Completion actions (open, sleep, shut down) run after a 30-second countdown.
- The extension popup reports pairing status and capture settings; pairing needs
  the user to confirm a code shown in the browser on the desktop.

## Capabilities and Constraints

- English and Simplified Chinese ship for both the desktop app and the extension;
  every layout must hold for both, including longer English labels and CJK text.
- Light and dark themes (system or explicit), already token-driven.
- macOS draws a translucent window behind the sidebar; Windows does not.
- Zero spend: no paid fonts, services, or assets. Fonts must be system or bundled
  open-licence files.
- The desktop app does not use loopback HTTP; it talks to the service through Wails
  bindings. Generated bindings must not be hand-edited.
- Playwright tests drive the real frontend with a stubbed Wails transport and query
  by visible names and roles; behavior, labels, and roles must survive a restyle.

## Brand Commitments

- Name: NimGet (product), `nimget` (CLI and module).
- Existing app icon: `app/build/appicon.png`, `app/branding/nimget.svg`.
- Voice: plain, factual, short. Errors say what happened and what to do next.
- The visual design system lives in `app/frontend/DESIGN.md`; the root `DESIGN.md`
  is the engineering contract and is not a visual document.

## Evidence on Hand

- Benchmark method and recorded comparison: `app/frontend/tests/README.md`.
- No testimonials, user counts, or press. Do not invent any.

## Product Principles

1. Glanceable first: speed, progress, and failures readable in one look.
2. Never lose a download, and never hide why one stopped.
3. Keyboard and screen reader parity with the mouse.
4. Quiet by default; the app earns attention only when the user must act.

## Accessibility & Inclusion

Existing behavior to keep: visible focus, roles and labels on every control, focus
restoration after dialogs and removed rows, reduced-motion support, 44px hit areas on
row checkboxes. Target WCAG 2.2 AA contrast in both themes.
