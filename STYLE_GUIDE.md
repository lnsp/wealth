# Style Guide

The visual language for the wealth app. Aimed at a "heritage private bank" feel — warm, paper-toned, deliberately restrained. Less consumer SaaS, more editorial finance.

Source of truth: `frontend/tailwind.config.js` for tokens and `frontend/src/index.css` for CSS variables, base layer, and component primitives. This document describes the system; the code is authoritative.

## Aesthetic

The light theme is "the club at noon," the dark theme is "the club at midnight." Surfaces feel like parchment. A near-invisible paper-grain SVG noise overlay (0.025 opacity) sits on `body::before` to soften flat regions.

Refinement levers we've committed to:
1. **No iOS borrowings.** The codebase used to mix Apple HIG tokens (`#007AFF`, `apple-title1`, etc.) with the heritage palette. Those have been removed — they read "consumer app," not "private bank."
2. **Serif at display sizes.** EB Garamond carries `display` and `title` by default. Sans is reserved for UI chrome, dense data, and body copy.
3. **Hairlines over shadows.** Surfaces are separated by 1px borders in `divider`, not soft drop shadows. Shadows are reserved for overlays that need to detach from the page (popovers, modals).
4. **Tight, architectural radii.** 6–8px on inputs and cards, 4px on buttons. No 16–20px "soft" rounding.
5. **Slower, more confident motion.** 350ms default with `cubic-bezier(0.16, 1, 0.3, 1)` (out-expo) — things glide to a stop with weight.

## Color tokens

All colors are CSS custom properties that swap between light and dark mode at `:root` / `.dark` (see `frontend/src/index.css:9-43`). Every accent is tuned to clear WCAG AA (≥4.5:1) against every surface — light-mode `slate` is darkened from default to maintain contrast on `parchment-deep`.

### Surfaces & text

| Token             | Light    | Dark     | Use                                                    |
| ----------------- | -------- | -------- | ------------------------------------------------------ |
| `parchment`       | `#FAF9F6`| `#141210`| Page background, primary card surface                  |
| `parchment-deep` | `#F5F3EE`| `#1E1B18`| Subtle elevated surface (inputs, secondary cards)       |
| `inset`           | `#F0EDE8`| `#1A1816`| Recessed regions (alert bands, table headers)          |
| `divider`         | `#E5E0D8`| `#332E28`| Hairline borders, separators                            |
| `ink`             | `#1C1917`| `#EDE9E3`| Primary text                                            |
| `ink-body`        | `#44403C`| `#C8C3BB`| Body copy                                               |
| `ink-muted`       | `#5E5853`| `#9C968E`| Captions, secondary metadata                            |

### Accents

| Token            | Light    | Dark     | Use                                       |
| ---------------- | -------- | -------- | ----------------------------------------- |
| `forest`         | `#1B3D2F`| `#5AAD85`| Primary action, info, focus outline       |
| `forest-light`   | `#2A5A45`| `#78C4A0`| Primary hover                              |
| `gold`           | `#7A6330`| `#C8A960`| Highlights, secondary accent              |
| `sage`           | `#4A6A4A`| `#7EAE7E`| Success, positive deltas                  |
| `claret`         | `#7A3040`| `#D07080`| Danger, negative deltas                   |
| `amber`          | `#7A5520`| `#D4AA50`| Warning                                    |
| `walnut`         | `#7A5C3A`| `#B89870`| Neutral chart accent                       |
| `slate`          | `#5E626C`| `#9AA0AD`| Neutral chart accent                       |

### Semantic aliases & chart palette

```
semantic.success / danger / warning / info / muted
chart.1 (forest) / chart.2 (gold) / chart.3 (sage) /
chart.4 (walnut) / chart.5 (slate) / chart.6 (claret)
```

## Typography

### Families

| Family       | Stack                                                                  | Use                                       |
| ------------ | ---------------------------------------------------------------------- | ----------------------------------------- |
| `font-serif` | EB Garamond → Georgia → Times New Roman                                | Display copy, KPI values, editorial heads |
| `font-sans`  | Inter → -apple-system → BlinkMacSystemFont → SF Pro Display            | UI chrome, body, tables, dense data       |
| `font-mono`  | SF Mono → ui-monospace → Menlo                                         | ISINs, account refs, raw identifiers      |

Body sets `font-variant-numeric: tabular-nums lining-nums` by default so columns line up. Don't override this in tables.

### Scale

| Class           | Size  | Weight | Tracking  | Family    | Notes                                |
| --------------- | ----- | ------ | --------- | --------- | ------------------------------------ |
| `text-display`  | 44/52 | 400    | -0.025em  | EB Garamond (auto) | Hero numbers, page-defining KPIs |
| `text-title`    | 26/32 | 400    | -0.015em  | EB Garamond (auto) | Page titles                          |
| `text-heading`  | 20/28 | 500    | -0.005em  | sans      | Section headings                     |
| `text-label`    | 11/16 | 500    | 0.1em     | sans      | All-caps eyebrows, table column heads |

`text-display` and `text-title` bind serif via Tailwind's `fontSize` config — `font-serif` is no longer needed alongside them (it's harmless if left from prior usage).

### Polish utilities (opt-in)

```html
<!-- All-caps labels with refined small-caps cadence -->
<span class="font-smallcaps">since inception</span>

<!-- Old-style figures for editorial prose. NOT for tables. -->
<p>Around <span class="font-oldstyle">1873</span>, the firm…</p>
```

`.font-smallcaps` applies `font-variant-caps: all-small-caps` plus 0.04em tracking. `.font-oldstyle` applies `oldstyle-nums proportional-nums` — only use in standalone prose; tables stay lining + tabular for column alignment.

## Surfaces & elevation

**Default elevation = hairline.** `.apple-card` carries a 1px `border-divider`, no shadow. Use the same pattern when building custom surfaces:

```html
<div class="bg-parchment border border-divider rounded-apple p-4">
```

Shadows are reserved for **overlays that need to detach from the page** (popovers, modals, tooltips):

| Token         | Shadow                                  | Use                            |
| ------------- | --------------------------------------- | ------------------------------ |
| `shadow-apple-sm` | `0 1px 2px rgba(0,0,0,0.04)`        | Subtle lift (hover, tooltips)  |
| `shadow-apple`    | `0 2px 4px rgba(0,0,0,0.05)`        | Popovers, dropdowns            |
| `shadow-apple-lg` | `0 8px 24px rgba(0,0,0,0.08)`       | Modals, command palettes       |

Do **not** use shadows as the default lift for cards — that pulls the design back toward iOS.

## Radii

| Token             | Value | Use                              |
| ----------------- | ----- | -------------------------------- |
| `rounded-apple`   | 8px   | Cards, large containers          |
| `rounded-apple-lg`| 12px  | Modals, sheets                   |
| `rounded-apple-xl`| 16px  | Hero panels                      |
| `rounded-md` / `rounded-lg` | 6/8px | Inputs, selects (also via `.apple-input` / `.apple-select` at 6px) |
| Buttons           | 4px   | Set on `.apple-btn-*`            |

The `rounded-xl` (12px) and `rounded-lg` (8px) Tailwind defaults are widely used — they're fine; just don't reach for `rounded-2xl` or larger.

## Motion

Default transition: **350ms `cubic-bezier(0.16, 1, 0.3, 1)`** (out-expo). Things glide to a stop with weight rather than the snappy 250ms Material curve we used to default to.

- Hovers, focus states, dropdown opens: use the default.
- Page transitions / large layout shifts: stick with the default — restraint reads premium.
- Don't pile on entry animations. A fade is enough.

## Focus states

`:focus-visible` outline is **2px forest with 2px offset**. Set globally in the base layer — don't override per-component unless the default genuinely fails (e.g., on a forest-colored background).

## Component primitives

These live in `frontend/src/index.css` under `@layer components`. Treat them as the lowest-level building blocks.

| Class              | Description                                                      |
| ------------------ | ---------------------------------------------------------------- |
| `apple-card`       | 8px radius, parchment, 1px divider hairline                      |
| `apple-separator`  | 1px bottom divider                                               |
| `apple-input`      | 6px radius, parchment-deep, divider hairline, forest focus ring  |
| `apple-select`     | Same as input + custom dropdown chevron                          |
| `apple-btn-primary`| 4px radius, forest fill, parchment text                          |
| `apple-btn-secondary`| 4px radius, transparent + forest border, forest text           |
| `apple-badge`      | Pill, 12px text, no default colors — caller specifies bg/text    |
| `safe-area-bottom` | Adds `env(safe-area-inset-bottom)` padding                       |
| `safe-area-header-offset` | Adds top safe-area padding (mobile only)                  |
| `scrollbar-hide`   | Hides scrollbar while keeping scroll                              |

Note: the `apple-*` prefix is legacy — these are heritage primitives now. Don't rename without sweeping all callers.

## Conventions & gotchas

Hard-won rules from production bugs (see TASKS.md for incident context):

1. **Don't use Tailwind alpha modifiers (`bg-amber/10`, `bg-{color}/20`) on var-backed tokens.** They render as nothing because the CSS variables aren't in the channel-decomposed format Tailwind's `/N` syntax needs. Use:
   - **Outlined chips:** `bg-parchment-deep + border border-{color} + text-{color}`
   - **Severity bands:** `bg-inset + border-l-[3px] border-{color}`

2. **Color is never the only signal.** Status, urgency, and freshness states must always carry a non-color cue too — a dot, label, icon, or position. Accessibility AND color-blind safety.

3. **WCAG AA on every surface.** Every accent must clear 4.5:1 against `parchment`, `parchment-deep`, `inset`, and `divider`. The token values in `index.css` are already tuned — don't introduce new colors without checking with a contrast tool.

4. **Dark/light parity.** No hardcoded hex values in components. Always use semantic tokens. For ECharts, use the shared theme + `useThemeColors` hook so charts swap with the rest of the page.

5. **Tabular numbers in tables.** Body already sets `font-variant-numeric: tabular-nums lining-nums`. Don't override with `font-oldstyle` inside tables — columns will misalign.

6. **Privacy mode CSS depends on `font-serif` selectors.** See the `.privacy` rules in `index.css`. If you display monetary values, use `font-serif` so they get blurred in privacy mode.

## Charts

All visualizations go through `<ReactECharts>` with a shared theme object. The chart palette is `chart.1–6`, intentionally heritage-toned. See `ARCHITECTURE.md:754-767` for the mapping of chart types to ECharts series types.

Quick reference:

| Chart                | Series type          | Notes                                              |
| -------------------- | -------------------- | -------------------------------------------------- |
| Net worth over time  | `line` w/ `areaStyle`| Stacked areas via `stack: 'total'`                 |
| Overlap heatmap      | `heatmap`            | Cartesian grid, `visualMap` for color scale         |
| Drawdown over time   | `line` w/ `areaStyle`| Claret-shaded area below 0%, from daily snapshots  |

## Responsive design

Single breakpoint that matters: Tailwind `md` (768px).

| Form factor          | Layout                                                                                  |
| -------------------- | --------------------------------------------------------------------------------------- |
| Desktop ≥1024px      | Top nav, multi-column dashboards                                                         |
| Mobile <768px        | Fixed bottom 5-tab iOS-style bar with safe-area padding for notched phones              |

Stacked allocation bars appear above holdings on both form factors; mobile collapses tables into cards while preserving the weight bar inline.

## Migration notes

If you find code referencing tokens that no longer exist:

| Removed                         | Replacement                                       |
| ------------------------------- | ------------------------------------------------- |
| `bg-apple-blue`, `text-apple-red`, … | Use heritage semantic tokens (`forest`, `claret`, `sage`, …) |
| `text-apple-title1` through `text-apple-caption2` | Use `text-display`, `text-title`, `text-heading`, `text-label`, or arbitrary `text-[Npx]` |
| `hover:shadow-apple` on cards   | Use `hover:bg-parchment-deep` or `hover:border-forest` for a hairline-friendly hover |

Keep the `apple-*` **component class names** (`apple-card`, `apple-btn-primary`, etc.) — those are stable; just the underlying styling has been refined.
