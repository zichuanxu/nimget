# NimGet visual design: Daylight departure board

The visual system for the desktop app (`app/frontend`) and the browser extension
(`extension/popup.css`). The root `DESIGN.md` is the engineering contract; this file
covers only how NimGet looks and moves. Product facts live in the root `PRODUCT.md`.

## Idea

The queue reads like a departures board: what is leaving now, what is waiting, and
when each file arrives. Users glance at the window and leave, so every row answers
the same questions in fixed columns: status, file, progress (with time left under
the bar), and speed. Warm white and graphite carry everything; one teal accent
marks what is moving. Status colours appear only on status.

## Colour

Tokens live in `src/styles.css` on `:root`. Dark values are defined twice, under
`prefers-color-scheme: dark` (system) and `:root[data-theme="dark"]` (explicit
choice). The extension uses the same values through `light-dark()`.

| Token | Light | Dark | Job |
|---|---|---|---|
| `--bg` | `#fcfcfb` | `#161719` | content pane |
| `--sidebar` | `#f4f4f2` | `#111214` | navigation rail |
| `--surface` / `--raised` | `#ffffff` | `#1c1d20` / `#232428` | cards, dialogs, menus |
| `--text` / `--text-2` / `--text-3` | `#17181a` / `#53565c` / `#6e7178` | `#ecedee` / `#a9acb2` / `#8b8e95` | primary, secondary, quiet text (all ≥ 4.5:1 on `--bg`) |
| `--border` / `--border-strong` | `#e5e5e2` / `#d3d3cf` | `#2a2c30` / `#3a3c41` | hairlines, control outlines |
| `--selected` / `--row-hover` | `#ecece9` / `#f4f4f2` | `#24262a` / `#1c1d20` | solid row states (actions sit on them) |
| `--accent` | `#0f7b6c` | `#4cbfaa` | primary buttons, progress, focus, running |
| `--accent-text` / `--accent-soft` | `#0b6a5d` / `#e2f1ed` | `#6fd3c1` / teal 14% | running tag, speed figures |
| `--green` / `--red` / `--amber` | `#2f6f37` / `#c42d2d` / `#965a00` | `#86d095` / `#ff8077` / `#f0b350` | completed, failed, high priority |

Chrome is neutral; the accent never decorates. Native controls pick up the palette
through `accent-color`, `scrollbar-color`, `caret-color`, and `::selection`.

## Type

Geist Variable (SIL OFL, bundled from `@fontsource-variable/geist`), falling back
to Segoe UI Variable / SF and then PingFang SC or Microsoft YaHei for Chinese.
The extension uses the system stack to stay small.

| Role | Size / weight |
|---|---|
| Body, rows | 13.5px / 400–550 |
| Secondary, notes, tags | 12–12.5px |
| Page title | 19px / 650, -0.02em |
| Dialog title | 16px / 650 |
| Board figure (total speed) | 28px / 650, -0.03em |

Every figure (bytes, speed, time, counts) uses `font-variant-numeric: tabular-nums`.
Monospace only for URLs, cURL text, and the pairing code.

## The board

- One CSS grid serves the column labels and every row (`.task-columns`, `.row`).
- `.list` is a size container. At 720px and wider, rows lay out as
  `checkbox · status · file · progress/arrival · speed · size`; narrower, each row
  stacks name, bar, and a facts line. DOM order never changes, so each text exists
  once (tests rely on it) and `.row-facts` switches to `display: contents`.
- Status is a small tag: filled dot for running, completed, and failed; a ring for
  queued and paused.
- Progress is a native `<progress>` labelled by the file name, styled through
  `::-webkit-progress-value` / `::-moz-progress-bar`; unknown sizes use the
  indeterminate state, slowed under reduced motion.
- Hover, focus, or selection swaps the size figure for quick actions, which sit on
  a solid background and may extend left over the speed cell.

## Components

- Buttons 32px, radius 7px; primary is teal, danger red; small 28px.
- Fields 34px with a teal focus ring (`0 0 0 3px --accent-ring`).
- `<select>` opts into `appearance: base-select` where supported, with a themed
  picker; elsewhere a native select with a drawn chevron.
- Dialogs are native `<dialog>` opened with `showModal()`. Entry fades with
  `@starting-style`; the small rise runs only without reduced motion.
- Menus keep their ARIA menu roles; hover and focus use `--selected`, and
  destructive items tint red instead of filling.

## Motion

150–200ms state transitions on `cubic-bezier(0.16, 1, 0.3, 1)`. Progress fills
ease to their new width. No decorative pulses. `prefers-reduced-motion` cuts
durations to near zero.
