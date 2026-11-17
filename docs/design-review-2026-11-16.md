# Design review — 2026-11-16

Full visual / UX review of the app against `STYLE_GUIDE.md`.

## How this was done

- 204 screenshots of the locally running stack (`localhost:8443`): every route and top-level tab, at 1440px desktop and 390px mobile, light and dark.
- Per-view measurements: page height, horizontal overflow, console errors, controls under 32px, heading counts.
- Source-level greps of `frontend/src` for style-guide conformance.
- Five area reviews (shell + Net Worth, Portfolio + Cashflow, Analysis + Tax, Planning, Transactions + Settings), merged and de-duplicated here.

Caveats:

- The running container image was four days old. Findings marked **[src]** were confirmed in current source; **[live]** were confirmed in the running build; **[shot]** were seen in screenshots only; **[unconfirmed]** are a reviewer's inference.
- Sections that only render after user input (FIRE results, withdrawal simulation, Rentenlücke results, security detail, privacy mode) were reviewed from source, not screenshots.
- Other work was modifying backend files and `Login.tsx` in this working tree during the review, so `Login.tsx` line numbers may have shifted.
- No amounts, names or account details are quoted; the data on screen is real.

Paths are relative to `frontend/src/` unless they start with `internal/`.

## Summary

The visual identity holds up: serif figures, hairline sections and the tuned dark palette read as one product on every page. The problems are underneath it.

1. **Broken things.** One tab crashes, one page overflows on mobile, several charts render empty or unreadable, and a few flows are dead ends.
2. **Numbers that mislead.** Several headline figures are mislabelled, mix currencies, or contradict a figure elsewhere on the same page.
3. **No shared component layer.** The style guide's primitives are almost unused, so each page re-implements inputs, KPI tiles, buttons and section headers slightly differently. Most of the Medium and Low findings are symptoms of this.
4. **Formatting has no single source.** Decimal separators, date formats, axis abbreviations and language vary within single pages.

Suggested order: section 1, then section 2, then the systemic fixes in section 3, which remove a large share of sections 4–9 for free.

---

## 1. Broken — fix first

| # | Finding | Where | Status |
|---|---|---|---|
| 1.1 | **Analysis → Costs crashes.** The handler returns `"holdings": null` when no holding has a TER; the page reads `.length` on it. Takes Overlap, Correlation and ETF Breakdown down with it. | `internal/handler/analysis.go:985`, `pages/Analysis.tsx:1862` | [live] [src] |
| 1.2 | **The crash fallback is a dead end.** The route-level boundary replaces the whole page including the tabs, shows the raw JS error, and "Try again" resets to the Risk tab. | `App.tsx:368`, `components/ErrorBoundary.tsx:30-42` | [live] |
| 1.3 | **Cashflow overflows 83px on mobile**, stretching the fixed header and pushing the active "Cashflow" tab off the bottom bar. `sr-only` spans in the Trend column escape the scroll wrapper. Adding `relative` to the wrapper takes the overflow to 0. | `pages/Cashflow.tsx:181`, `:220` | [live] |
| 1.4 | **Mobile bottom bar has 7 tabs with truncated 10px labels** ("Net Wo…", "Transac…"). The style guide specifies 5, and the mobile header shows no page title, so the current page is never named in full. | `App.tsx:387-400`, `:331-336` | [live] |
| 1.5 | **Mobile share quantities print as raw JS numbers**, so a fractional quantity reads as thousands in German formatting. Desktop formats the same value correctly. | `pages/Transactions.tsx:214` vs `:270` | [src] |
| 1.6 | **Deleting a user is one tap with no confirmation**, offered on the only admin row. Passkey "Remove" and "Disable 2FA" also have none. | `pages/Settings.tsx:690`, `:815`, `:734` | [src] |
| 1.7 | **Adding a notification channel is a dead end.** Three chained `prompt()`s, then a POST with an all-empty config and no UI to fill it in. | `pages/Settings.tsx:994-1011` | [src] |
| 1.8 | **CSV file picker is unreachable by keyboard** (`display:none` input inside a non-focusable label). | `components/CsvUploader.tsx:81-84` | [src] |
| 1.9 | **Add Account form has no usable focus or labels.** No `htmlFor`/`id`, `outline-none` on every control, and selects with `appearance-none` and no chevron read as static text. | `pages/Settings.tsx:392-489` | [src] |
| 1.10 | **Cashflow month rows are mouse-only.** `<tr onClick>` is the only way to change the Spend Breakdown month. | `pages/Cashflow.tsx:200-204` | [src] |
| 1.11 | **Dividends tab is completely blank** when the total is zero — no heading, no empty state. | `pages/Portfolio.tsx:446` | [live] |
| 1.12 | **Mobile FIRE "Annual expenses" input runs past the viewport edge** (`flex-1` without `min-w-0`). | `components/planning/FireCalculator.tsx:49-53` | [live] |
| 1.13 | **Planning can blank on an API error.** The Rentenlücke call stores any JSON body, including an error body, and then maps over `.sources`. | `components/planning/Rentenluecke.tsx:113-118`, `OpportunityCosts.tsx:26-40` | [src], not reproduced |
| 1.14 | **`/api/analysis/benchmark-comparison` returns 400** on every mount of Analysis and Tax when the benchmark has under 30 price rows. The "What If…" section then silently disappears. | `internal/handler/analysis.go:2167`, `pages/Analysis.tsx:116`, `:1832` | [live] [src] |

### Charts that render empty or unreadable

| # | Finding | Where | Status |
|---|---|---|---|
| 1.15 | **Chart theme axis defaults never apply.** The wrapper merges only colour, text and tooltip, so any chart without explicit `splitLine` gets ECharts' default gridlines: near-white in dark mode, blue-grey in light. Affects Analysis, Planning and Cashflow charts. | `components/charts/EChartWrapper.tsx:19-29`, `charts/theme.ts` | [src] |
| 1.16 | **Legend colours don't match the lines** wherever colour is set only in `lineStyle` or as a function: Portfolio Growth Over Time, Net Worth Projection, Analysis Cash Flow. | `pages/Portfolio.tsx:387-434`, `pages/NetWorth.tsx:1229-1237`, `pages/Analysis.tsx:1790` | [shot] |
| 1.17 | **Mobile x-axis labels collide into one string** on the Net Worth waterfall, Growth Over Time, Projection, and Analysis FX / Cash Flow / Purchasing Power. | `pages/NetWorth.tsx:621`, `:690`, `:1205`; `pages/Analysis.tsx:1759`, `:1787`, `:1820` | [shot] |
| 1.18 | **Exchange Rates chart shows no lines** and its axis names overprint in one corner. | `pages/Analysis.tsx:1742-1767` | [shot], cause unconfirmed |
| 1.19 | **Sector Drift is a ~380px empty plot.** The guard checks row count, not whether any sector has data. | `pages/Analysis.tsx:190` | [shot] |
| 1.20 | **Net Worth "Breakdown Over Time" has no legend** for its three stacked bands, and its range follows a period selector that isn't shown on that tab. | `pages/NetWorth.tsx:256-334` | [shot] |
| 1.21 | **Cashflow donut uses two near-identical colours** for all categories, so the legend identifies nothing. | `pages/Cashflow.tsx:257` | [shot] |

---

## 2. Numbers that mislead

These are presentation problems that change what a figure means. Items marked for audit need the calculation checked, not just the label.

| # | Finding | Where | Status |
|---|---|---|---|
| 2.1 | **Holdings table shows native-currency Price and Avg Cost with a € sign**, and P&L % divides EUR profit by a native-currency cost basis. | `components/HoldingsTable.tsx:148-150`, `:173`, `:176`; see comment at `pages/Portfolio.tsx:124-131` | [src] |
| 2.2 | **Portfolio Performance grid contradicts itself**: a TWR of −100%, "+0.00%" under a positive Total Gains, negative Net Invested, alongside a strongly positive P&L. | `pages/Portfolio.tsx:295-316` (renders API values) | [live] — needs a financial audit of the backend |
| 2.3 | **Net Worth "Return" KPI is total net worth minus contributions**, so property costs and loan interest count as negative return. It disagrees with "Market Returns" further down the page. | `pages/NetWorth.tsx:383-385`, `:530-534` | [shot] |
| 2.4 | **Projection tab calls liquid wealth "net worth"**, directly under a hero showing total net worth. | `pages/NetWorth.tsx:1104-1105`, `:1292`, `:1366` | [shot] |
| 2.5 | **Net worth totals differ slightly** between the hero and the Assets tab footer. | `pages/NetWorth.tsx:398`; `components/assets/AssetsOverview.tsx:123` | [shot], cause unconfirmed |
| 2.6 | **Spending counts "Transfer" asymmetrically** — excluded from income but not from expenses — which may be why savings rates show deeply negative. | `internal/handler/analysis.go:3071-3087` | [src], effect unconfirmed |
| 2.7 | **Cash-buffer "months" uses 5% of securities value, not expenses**, while the copy says "months of expenses". | `internal/handler/analysis.go:4193-4197`, `pages/Analysis.tsx:806` | reviewer-reported |
| 2.8 | **Planning results rest on unstated constants**: a fixed statutory pension in Withdrawal Simulation, 1.8% over 25 years for every loan, 7% / 2% growth in Withdrawal Sources. | `components/planning/WithdrawalSimulation.tsx:35`, `LiabilityAmortization.tsx:20-21`, `WithdrawalSources.tsx:30` | [src] |
| 2.9 | **Cashflow shows months with no imported data as "—" rows** under a "Trailing 12 months" label, without saying the medians cover fewer months. | `pages/Cashflow.tsx` | [shot] |
| 2.10 | **Broker withdrawals in the RSU account show a positive amount under a claret "withdrawal" badge.** | likely `internal/parser/` | [shot], cause unconfirmed |
| 2.11 | **Privacy mode misses money values.** The blur selectors match only `.tabular-nums` and certain serif sizes. Unblurred: the hero's Liquid / property line, account card balances, group subtotals, goal amounts, alert thresholds, IBANs, and Planning milestone labels with their progress bars. | `index.css:226-243`; `pages/NetWorth.tsx:400-403`; `components/AccountCard.tsx:70`; `pages/Portfolio.tsx:1249-1251`, `:1309`; `pages/Settings.tsx:545`; `components/planning/FinancialIndependencePlaybook.tsx:81-116` | selectors and hero [src]; rest reviewer-reported |

---

## 3. Systemic issues

### 3.1 The style guide's primitives are not used

Counts across `frontend/src`:

| Primitive / token | Uses | What is used instead |
|---|---|---|
| `apple-card` | 0 | ~50 hand-written `rounded-[8px]` / `rounded-[6px] border border-divider bg-parchment` strings, plus `rounded-xl` filled tiles |
| `apple-input` | 0 | 113 raw `<input>` with at least four class recipes |
| `apple-select` | 1 | 17 raw `<select>` |
| `apple-btn-primary` / `-secondary` | 16 / 18 | 124 raw `<button>`, including ~8 hand-rolled `rounded-[8px] bg-forest text-white` primaries |
| `text-display`, `text-label` | 0 | 23 distinct `text-[Npx]` sizes (12px ×365, 13px ×297, 11px ×257) |
| `ui/MetricCard` | — | at least five KPI tile patterns |
| `LoadingState` / `ErrorState` / `EmptyState` | 1 use per page at most | hand-rolled states in Planning and elsewhere |

Consequences seen on screen:

- **KPI tiles** differ between and within pages: hairline-top serif (Portfolio Overview), filled semibold (Dividends, Cashflow), sans 13px (Opportunity Costs, Net Worth rule-of-three), band-style (FIRE).
- **Inputs** on one tab differ in height (27px vs 38px), text size (12px vs 16px) and label position. Text under 16px triggers iOS zoom on focus.
- **Buttons** come in 4px, 6px and 8px radii; Settings → Security shows forest and sage primaries side by side.
- **Section headers** flip between hairline-above and hairline-below, and all carry the contradictory `pt-6 py-3 md:py-5`.
- **Eyebrows and table heads** are serif 11px uppercase in most places; the guide's `text-label` is sans.
- **Placeholders** fall back to Tailwind's default cool grey (~2.4:1) because inputs bypass `.apple-input::placeholder`.

Recommendation: build `Section`, `Field` (label + input + unit suffix), `MetricCard`, `Button` and `AddChip` once, then migrate page by page. Decide whether `text-heading` is serif (as coded) or sans (as documented) and update one of them.

### 3.2 Formatting has no single source

- **Decimal separators:** currency uses de-DE commas; percentages use `toFixed` dots, often in the same tile.
- **Dates:** German month abbreviations on chart axes (`PeriodSelector.tsx:103-125` hard-codes de-DE), English `en-GB` months on Cashflow (`Cashflow.tsx:31`), ISO strings on FX / Cash Flow / Projection axes, `dd.mm.yyyy` in tables, and US `mm/dd/yyyy` in native date inputs.
- **Axis abbreviations:** "K", "k", "0k", unabbreviated negatives ("-100000"), and en-US "10,000".
- **Currency:** "€" in most places, "EUR" in recommended actions and the tax calendar; cents shown in some contexts and not others.
- **Quantities:** forced to three decimals, so they read as thousands.
- **Formatters:** `fmt` is defined separately in `Cashflow.tsx`, `Lab.tsx` and `components/assets/format.ts`.

Recommendation: one `format.ts` with currency, percent, quantity, date, month and axis-compact helpers on a single locale; delete the local copies.

### 3.3 Language is mixed without a rule

The UI is English, but chart months, the whole Tax Calendar, one Planning section title ("Rentenlücke"), several chip labels ("Sonstige", "Private RV") and one notification are German. Raw identifiers also surface as copy: `transfer_out`, `savings_plan`, `asset_purchase`, `morgan_stanley`, `admin`, `ok` / `never`.

Recommendation: keep German legal terms with a one-line gloss, translate generic words, and add one label map for enum values. One literal bug: the calendar title "FSA-Nutzung unter 80%%" prints a double percent (`internal/handler/analysis.go:3365`).

### 3.4 Page structure and headings

- Only Planning, Cashflow, Settings and the 404 page have a page `<h1>`. On the rest, the only h1 is the sidebar brand, which is hidden on mobile.
- Section and sub-chart headings share one style, so there is no visual hierarchy between "Risk Analytics" and "Drawdown Over Time".
- Several section headings are 11px eyebrows above 15–16px content.

### 3.5 Tabs and period selector

- `TabBar` sets `aria-controls="panel-…"` but no page renders a matching `role="tabpanel"`; there is no arrow-key navigation (`components/ui/TabBar.tsx:29`).
- Period buttons have no `aria-pressed` (`components/ui/PeriodSelector.tsx:54-67`).
- The active period pill mixes `border-l-[3px]` with `border`, giving a lopsided left stroke (`PeriodSelector.tsx:46-48`).
- On mobile, tab bars and period selectors scroll with a hidden scrollbar and no edge fade; "All" and "Goals & Alerts" start off-screen.
- Section headers that pair a title with a period selector don't wrap on mobile (`Analysis.tsx:549`, `:586`, `:1686`; `Portfolio.tsx:783`).
- Tab state is persisted for Tax, Settings and Planning but not for Analysis; none are in the URL.

### 3.6 Style-guide rule violations

- **Alpha modifiers on var-backed tokens** (rule 1), 8 occurrences, which silently render wrong: the 404 numeral, Login placeholders, `HoldingsTable.tsx:203`, `Analysis.tsx:736`, `PeriodSelector.tsx:42`, and the Transactions filter focus ring (`focus:ring-forest/30`, `Transactions.tsx:126-176`), which also suppresses the global focus outline.
- **Hardcoded colours** (rule 4): iOS blue/green/red fills in the Projection and Drawdown charts (`NetWorth.tsx:1227-1257`, `Analysis.tsx:469`); `PART_COLORS` and a white card background in `components/assets/format.ts:23-43`, which make the Assets bar nearly invisible in dark mode.
- **Colour as the only signal** (rule 2): active mobile tab, Portfolio health score, holdings weight ≥30% in claret, Loss Pots mobile cards, stress-test severity dots, Data Quality dots.
- **`color-scheme` is never set**, so native date-picker icons are near-invisible in dark mode.
- **Radii:** `rounded-xl` with `border-l-[3px]` curves the accent stripe on several bands; `CsvUploader` uses 16px.
- **Doc drift:** the guide says desktop uses a top nav; the shell is a left sidebar.

### 3.7 Small tap targets and text

- Transactions has ~50 controls under 32px on desktop, almost all the per-row category select (`Transactions.tsx:283-301`), which is also unlabelled.
- Settings → Accounts rows carry four ~22px inline actions each.
- Planning inputs are ~27px tall; remove links are ~16px.
- Chart text is 9–10px in many options; the bottom-nav labels are 10px.

### 3.8 Scroll depth

Measured in viewport heights (desktop / mobile). `TASKS.md` lists a three-screen target; most pages now meet it on desktop.

| View | Desktop | Mobile |
|---|---|---|
| Analysis → Allocation | 4.2 | 4.7 |
| Transactions | 4.0 | 6.3 |
| Net Worth → Overview | 2.9 | 3.5 |
| Analysis → Risk | 2.5 | 3.4 |
| All other views | ≤ 2.0 | ≤ 2.8 |

Analysis → Allocation is long mostly because single-category charts keep fixed heights (section 6). Transactions is long because rows wrap and a page holds 50 rows (section 8).

---

## 4. Shell, Login, 404

- **Login:** placeholders are the only visible labels; no `autoComplete`; the error has no `role="alert"`; the disabled Sign In button renders at ~2:1 on load and looks broken (`pages/Login.tsx`).
- **404:** the "404" numeral is 64px bold sans at full strength and overpowers the serif heading (`pages/NotFound.tsx:6`).
- **Bell and alerts:** "10 unread alerts" on Net Worth is link-styled but not interactive and duplicates the bell badge (`pages/NetWorth.tsx:545-548`).
- **Sign Out** appears in both the sidebar and Settings on desktop, styled as destructive in Settings.
- **Motion:** nav links use 250ms rather than the guide's 350ms out-expo (`App.tsx:289`, `:298`).

## 5. Net Worth

- **Wealth Attribution grid** shows a solid divider-coloured block in its empty sixth cell (`NetWorth.tsx:650`).
- **Sensitivity chart** clips its "Monthly contribution" label and plots bars from zero rather than from the baseline (`NetWorth.tsx:1398-1405`).
- **Similar greens:** Contributions and Market Returns, and Property and Equities in the donut, are hard to tell apart, especially in dark mode (`NetWorth.tsx:695-696`, `:763-775`).
- **Counterparty table** scrolls sideways on mobile with the Status column off-screen; on desktop two headers run together for lack of cell padding (`NetWorth.tsx:1444-1466`).
- **Hero sparkline** has no axes and uses `dataMin`, so a small monthly change looks like a cliff (`NetWorth.tsx:488-489`).
- **Hero period line** appears on tabs that have no period control (`NetWorth.tsx:445-451`).
- **Mobile Overview** truncates recommended-action titles and the donut label; the period selector hides "All".
- **Projection controls:** the slider value column is too narrow; "Save Scenario" reads as disabled; the scenario delete "×" is 11px with no label (`NetWorth.tsx:1115-1186`).
- **Assets tab:** cards are pure white with 12px corners in light mode; chips lose their background in dark mode (`components/assets/format.ts:43`, `AssetsOverview.tsx`).
- **Account cards:** a flat-balance sparkline renders as a grey box (`components/AccountCard.tsx:92-99`).

## 6. Portfolio, Cashflow

- **Growth Over Time:** repeated month labels, clipped "−100%" on the right axis, a 10px legend colliding with rotated labels, and two y-axes with zero at different heights (`Portfolio.tsx:367-436`).
- **Price Alerts "Add" button** sticks out past the content column on desktop (`Portfolio.tsx:1341-1343`).
- **Holdings table:** "Scroll for more →" always shows; `-mx-5` makes the table overhang; the name column wraps (`HoldingsTable.tsx:130-134`).
- **Security detail** opens from a `div onClick` and renders below the whole RSU block, far from the click (`HoldingsTable.tsx:86`, `Portfolio.tsx:860-863`).
- **Allocation History** is a solid 100% block with a single holding (`Portfolio.tsx:779`).
- **Goals & Alerts** lists system digests and tax reminders under Price Alerts, duplicating the bell (`Portfolio.tsx:1363-1383`).
- **RSU dates** use different formats on mobile and desktop (`UnvestedPanel.tsx:132` vs `:172`); the panel puts a € total next to $ subtotals without saying which is converted.
- **Cashflow mobile table** pushes Net and Trend off-screen with no scroll hint (`Cashflow.tsx:182`).
- **Cashflow colours:** the spend KPI uses claret for a non-negative amount; the Calibrate CTA uses the amber warning band for a neutral action (`Cashflow.tsx:136`, `:153`).
- **Legend pager** on the donut is unthemed and nearly invisible in dark mode (`charts/theme.ts`).

## 7. Analysis, Tax

- **Tab contents don't match tab names.** Allocation holds FX, Cash Flow, Purchasing Power and the benchmark; Costs holds Overlap and Correlation. Cash Flow appears twice with different encodings, alongside a top-level Cashflow page.
- **Degenerate charts keep fixed heights.** With one sector, country or currency, each chart still takes 300–450px (`Analysis.tsx:1680-1717`).
- **Crisis Stress Test table** is cut mid-number on mobile (`min-w-[700px]`, `Analysis.tsx:709`).
- **Dark-mode pie labels** render white with a black outline (`Analysis.tsx:2467`).
- **Line identity:** 30d and 365d volatility are both green and the legend shows circles, not line styles.
- **Tax Overview empty state hides the year selector and Export**, so there's no way to switch to a year that has data; "Tools" looks like a link but isn't (`Analysis.tsx:920-933`).
- **Tax Calendar** lists five past events before the two upcoming ones, dimmed with `opacity-60` (`Analysis.tsx:1495`).
- **FIFO lot rows** have no disclosure chevron (`Analysis.tsx:1260`).
- **Tools tab** sections carry an extra `p-4`, so headings sit 16px right of everything else (`Analysis.tsx:1162`, `:1244`, `:1340`).
- **Small bugs:** "1 sectors" / "1 currencies" (`Analysis.tsx:186`, `:396`); country label clipped on mobile (`:257`); treemap breadcrumb overlaps the chart (`:352-355`).
- **Dead code:** `pages/Lab.tsx` is unreachable (`/lab` redirects to `/tax`).
- **Wasted requests:** Tax mounts the full Analysis component and fires all its requests.

## 8. Planning

- **Playbook empty state** says "enter gross income and monthly budget above", but both inputs are on the Growth tab (`FinancialIndependencePlaybook.tsx:146`). Shared profile inputs (age, income, budget, tax rate) are scattered across tabs.
- **Labels:** 26 `<label>`s and no `htmlFor`; several visible labels differ from their `aria-label`; casing is mixed.
- **No units on currency inputs**; "EUR/year" exists only as a placeholder.
- **Number fields snap back to defaults when cleared**, so a value can't be deleted and retyped (`Rentenluecke.tsx:42-50`, `HumanCapital.tsx:58`).
- **Money Left on the Table and Opportunity Costs duplicate each other** with different styling.
- **Colour semantics:** zero-cost tiles show zero in claret (`OpportunityCosts.tsx:45`, `:50`).
- **Life Event chips** carry placeholder letters ("H Home Purchase"); Early Retirement fills a field with −999999 (`LifeEventSimulator.tsx:11-20`).
- **Remove controls** are ~16px text with no confirm or undo; Estate's is a bare "x" with no label.
- **Playbook checkboxes aren't saved**, and Export is offered on an empty plan (`FinancialIndependencePlaybook.tsx:201`, `:212-263`).
- **Opportunity Costs chart** has no caption or axis name, so monthly vs cumulative is unclear.
- **Mobile left edges** jump between sections because six components omit the `px-1` their headings use.

## 9. Transactions, Settings

- **No page heading** on Transactions; the first element is the Import CSV button.
- **Desktop table wastes height:** account names wrap, Qty and Price are "—" on most rows, and long counterparties truncate with no `title` (`Transactions.tsx:263-280`).
- **Pagination:** the pager is at the bottom, the total at the top; no month grouping or sticky header.
- **Security transfers get a cashflow category select** (`Transactions.tsx:20`, `:282`).
- **Mobile cards drop the account name and the category.**
- **Import gives no result feedback**; Import History draws "0" imported in the success colour (`Transactions.tsx:109-113`, `:358-361`).
- **Empty-state copy ignores date filters**; each page change swaps the list for a centred "Loading…" (`Transactions.tsx:189-193`).
- **Settings → Data** prints a raw debug string for the snapshot job, and a backup job that has never run shows the quietest chip on the page (`Settings.tsx:247-252`, `:287`).
- **Settings → Accounts** leads with the Add Account form and mixes in a portfolio concentration warning; the "Active" chip's accessible name differs from its text (`Settings.tsx:388-570`).
- **Settings → Notifications** holds Wealth Reports; View and PDF links differ in colour; "1 warnings" plural bug (`Settings.tsx:510-512`, `:862-974`).

---

## What works well

- The serif figures, hairline rules and serif section heads give every page the same calm, editorial rhythm.
- Dark mode is a properly re-tuned palette; surfaces, text and accents hold parity across all four variants.
- Where mobile card layouts exist (Holdings, RSUs, milestones, Vorabpauschale, Assets), they are clean and worth extending to the tables that still scroll sideways.
- Gains and losses carry explicit signs as well as colour; transaction badges use the outlined-chip pattern with text.
- The Contribution Router and Human Capital charts are the chart pattern to copy: theme colours, divider gridlines, themed legend.
- Tab bars meet 44px touch height and split long pages sensibly; Cashflow has proper loading, error and empty states.
- Accessibility groundwork exists: skip link, `aria-pressed` on theme and privacy toggles, descriptive `role="img"` labels on the Assets bars.
