# Calculation audit, 2026-11-18

Eight `financial-auditor` subagents each checked one page against HEAD `82d789a`. Analysis was split across two of them; Settings was covered together with Transactions.

**Method**
- Each figure was traced from component → `client.ts` → route → handler → SQL/analytics.
- Key figures were recomputed independently against the seeded local DB, read-only: 122 transactions, 2022-12 → 2026-05, goose v28.
- Where needed, the real handlers ran in a read-only DB session.
- No repo files or DB rows were changed.

**Status markers**
- *(v)*: also re-checked by the orchestrating session, in code or the DB.
- **KNOWN**: already listed in TASKS.md (2026-11-16 design review).
- Unmarked findings are VERIFIED by the auditor, by recomputation or end-to-end trace, unless marked *suspected*.

**Caveats**
- Numbers come from seeded test data, not prod.
- The local DB lacks migration 029.
- These external constants should be checked before changing them:
  - Basiszins 2026: 3.20 %
  - Grundfreibetrag 2026: 12,348 €
  - Rentenwert since 2025-07: 40.79 €
  - BBG RV 2026: 101,400 €

---

## Cross-cutting root causes

The same few mistakes recur across pages. Fixing them once fixes many figures.

1. **Amount signs are handled inconsistently.**
   - The seeded "Test Checking" withdrawals are stored negative *(v)*.
   - The SQL cash queries use `-amount` (queries.sql:151-223), so negative withdrawals become inflows. Go `txnSignedCashDelta` uses `abs()`.
   - Effect: checking cash is 33,328.97 instead of 18,671.03 (+14,657.94).
   - This flows into net worth, the risk series, the benchmark replay and the snapshots.
   - The Scalable and N26 parsers can also lose signs (Transactions C4).
   - Fix: normalise signs at import or add `CHECK (amount >= 0)`. Make SQL use `ABS`.
   - Check prod: `SELECT type, count(*) FROM transactions WHERE amount < 0 GROUP BY 1`.
2. **"Monthly contribution" means gross deposits.**
   - Calculation: `portfolio.go:1889-1917`, copied at 3305 and 3816.
   - Used by Projection, FIRE, Pension Gap, Life Events and the Playbook.
   - Salary deposits are counted, spending is never subtracted, and RSU vests are excluded.
   - Seed: 2,168 €/mo shown vs 1,557 €/mo net.
   - The Savings plans section (all-time average) and the stress test (1 % of portfolio) also break the trailing-12-month rule.
3. **Total net worth, including property, car and mortgage, is used where portfolio or investable scope is meant.**
   - Affected figures:
     - Goals: both pages.
     - FIRE progress, Human Capital and the Playbook.
     - The risk series: buying the apartment shows as a +1,400 % day.
     - The benchmark comparison.
     - The health-score risk component.
     - The nominal/real change cards.
   - `investableNetWorth` already exists in assets.go:1075.
4. **Stale constants.**

   | Constant | Where | In code | Correct |
   |---|---|---|---|
   | Basiszins 2026 | tax.go:113 | 2.53 *(v)* | 3.20 |
   | Grundfreibetrag | portfolio.go:3751, WithdrawalSimulation.tsx:51 | 11,604 *(v)* | 12,348 |
   | Rentenwert | portfolio.go:3685 | 39.32 *(v)* | 40.79 |
   | bAV cap | ContributionRouter.tsx:10, MoneyLeftOnTable.tsx:5 | 7,248 *(v)* | 8,112 |
   | Rürup cap | ContributionRouter.tsx:14, MoneyLeftOnTable.tsx:7 | 27,566 *(v)* | ~30,826 |
   | Sparer-Pauschbetrag before 2023 | tax.go:15 | 1,000 | 801 |
   | Besteuerungsanteil | Pension Gap | 50 + (year − 2005) | statutory table: 2030 → 86 %, 2040 → 91 % |
   | Risk-free rate | Risk tab | 3 % | ECB deposit rate 2 % |

5. **German tax rules are implemented wrongly in several places.** Details are in the Tax section.
   - The loss pots are swapped.
   - Teilfreistellung is not applied to fund losses.
   - Interest is missing from the Overview.
   - The Vorabpauschale is never added to income.
   - Transfers reset cost basis and acquisition date.
   - Crypto and gold are taxed under §20 instead of §23.
   - Anlage KAP lines are mapped wrongly.
   - The FSA view ignores losses.
   - FIRE and Withdrawal planning use the income-tax rate on whole withdrawals.
6. **Mixed bases.**
   - Cost-basis weights are labelled "market value".
   - Historical prices are converted at today's FX.
   - Nominal growth is compared against needs in today's euros.
   - Calendar-day series are annualised as trading days, and weekly series with √252.
7. **Look-through data is incomplete.**
   - ETF constituents are top 10 only.
   - The `SYNTH:` ID regression from fe4671e stops constituents from matching across ETFs *(v)*.
   - Fund metadata is fetched only for `asset_class = 'etf'`.
   - IE00B4L5YC18 "ISHSIII-MSCI EM" is classified as `stock`.
8. **Tests that lock in wrong behaviour.** They need rewriting along with the fixes:
   - `loss_pots_test.go:52`: single stock → general pot.
   - `anlage_kap_test.go:99`: losses not reduced by Teilfreistellung.
   - `spending_test.go:54`: lifetime-count subscription annualisation.
   - `sell_tax_test.go:25-37`: church-tax rate.
   - Several tests (anlage_kap, fsa_utilization, cross_broker_note, spending) test local copies rather than the handlers.

---

## Net Worth (`/`)

**Correct:**
- Net worth composition: cash + securities + physical − loans, with no double counting of financed assets or RSUs.
- The attribution identity, which closes to the cent.
- Loan schedule, projection compounding, Breakdown Over Time and account cards.

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| F1 | CRIT | Goal progress uses total net worth for every goal *(v)*. `funding_account_id` and `priority` are dropped from the response. | portfolio.go:1516-1536 | A 15k emergency fund shows 1,063 % complete. Glide path is always 50 %. Labels read "Priority undefined". Measure against the funding account. |
| F2 | CRIT | Asset Allocation double-counts securities in pension, crypto and precious-metals accounts *(v)*. | NetWorth.tsx:922-932 | Each account adds its full `balance`, then its holdings again. Add only `cash_balance`, as brokerage does. |
| F3 | CRIT | Projection's default contribution is gross deposits (root cause 2). `goals[0].monthly_contribution` silently overrides it. | portfolio.go:1893-1917 | 2,168 vs 1,557 €/mo. |
| F4 | CRIT | "Cash flow today" is a residual, not cash flow. | portfolio.go:2511-2520 | Shows "Market −155 € · Cash flow +159 €" with no transactions. The residual is price-date mismatch, FX and property/loan steps. |
| F5 | CRIT | The period change excludes the cutoff-day snapshot (UTC vs local time). | NetWorth.tsx:291-295 | YTD +91,781 (+135.5 %) vs correct +97,415 (+156.8 %). 1Y covers 364 days. |
| F6 | CRIT | Negative withdrawals inflate cash (root cause 1). | queries.sql:204-223 | Net worth +14,658 (9.2 %). |
| F7 | WARN | The "Market return" KPI uses different definitions for All and other periods. | portfolio.go:4266/4343 | Fees and taxes are included in one, excluded in the other. |
| F8 | WARN | Backfilled snapshots keep foreign cash at the FX of the transaction date; live snapshots use the latest FX. | jobs.go:1499-1646 | Creates a fake "today" move after every rebuild. |
| F9 | WARN | The "Why?" sentence prints a double minus. | portfolio.go:2546 | "B (--40 EUR)". |
| F10 | WARN | Two "liquid wealth" definitions, on the card and in the projection. | NetWorth.tsx:567 vs assets.go:1075 | |
| F11 | WARN | "Worth about" tax actions are overstated. | portfolio.go:2838-3043 | Harvest saving assumes gains exist to offset. Vorabpauschale uses today's value with no cap and Basiszins 2.53. |
| F12 | WARN | "Today" is the latest snapshot date. | NetWorth.tsx:300 | |
| F13 | WARN | Deposit-insurance thresholds differ between tabs (80/100k vs 90/100k). | NetWorth.tsx:992, AccountCard.tsx:110 | Morgan Stanley USD cash is SIPC-insured, not Einlagensicherung. |
| F14 | WARN | Concentration is checked per account row, not per ISIN. | NetWorth.tsx:1087 | |
| F15 | WARN | `goals[0]` sets contribution, return, target and horizon for the projection. | portfolio.go:1914-1988 | |
| F16 | WARN | "Projection Accuracy" back-tests with the current slider values. | portfolio.go:2313 | |
| I | INFO | Minor issues. | | Confidence band √21 should be √30.44 (17 % too narrow). "Contributed" includes the starting value. All-zero snapshots 2016–2022 are never cleaned up. "All" is capped at 5,000 days. Goal top-up ignores return. |

## Portfolio (`/portfolio`)

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| C1 | CRIT | Goals always show "0 % · 0 €" *(v)*. The page calls `/settings/goals`, which returns no progress fields. | Portfolio.tsx:482 | Use `api.getGoalsProgress()`, then fix Net Worth F1. |
| C2 | CRIT | Switch comparison ignores transfers and vests, and its gain formula is wrong. | portfolio.go:4928-5001 | Seed: tax 19,771 € on a position at −864 €; correct is 0. Buy 100 @ 10, sell 50 @ 20 → gain 1,000 vs FIFO 500. |
| C3 | CRIT | Savings-plan monthly total is an all-time average, includes stopped plans and lump sums. | portfolio.go:3158-3209 | Use trailing 12 months ÷ 12. |
| C4 | CRIT | Allocation history uses cost-basis weights but is labelled "% of market value". Points are dated the 1st. | analysis.go:1770-1800 | 50/50 shown vs 66.7/33.3 actual. |
| C5 | CRIT | Rebalance never sells untargeted holdings; trades don't net out; deposit mode can show "Buy 0 €". | portfolio.go:1478, 1672-1695 | |
| C6 | CRIT | "Cumulative return vs MSCI World": money-weighted vs price return, not rebased by period, capped at 1,000 rows. | portfolio.go:739, 868-887 | "1Y" starts at +37.7 %. |
| C7 | CRIT | "Dividends received" adds up mixed currencies and labels the total €. | 029 view `divs`, Portfolio.tsx:503 | |
| W1 | WARN | Performance scope includes checking/savings cash; the account filter isn't applied to the Growth chart. | portfolio.go:518 | Growth chart shows 30,415 vs market value 11,744. |
| W2 | WARN | TWR/IRR show 0.0 % instead of "not enough history"; a short window is still labelled "12 months". | performance.go:24/114 | |
| W3 | WARN | "Total gain %" on net capital explodes after withdrawals. | portfolio.go:611 | +238.6 % vs IRR 46.4 %. |
| W4-W13 | WARN | Various. | | Dividend growth 0 % with no base. Yield-on-cost numerator includes sold positions. Dividend calendar averages per transaction. Drift and health score contradict each other; health income score isn't monotonic. Security chart uses today's FX (+5.8 %). Savings-plan value ignores sells. Average cost isn't FIFO and isn't labelled. Concentration chip per account row. Switch-tax helpers ignore Teilfreistellung and loss pots. Time Machine rows don't sum. "Prices as of" uses the newest date. |
| I | INFO | Minor issues. | | UnvestedPanel uppercases "GBp" (pence) to "GBP", so pence-priced RSUs show 100× too high. IRR is annualised for histories under 1 year. |
| — | KNOWN | Cost basis at latest FX. | | Unrealised −1,686.55 shown next to remaining gain −864.21 for the same position. |

## Analysis: Risk & Performance

On the seed, the Risk tab shows:
- volatility 716.71 %
- max drawdown 99.99 %
- VaR −97,249.57 €
- all-time high 58,137,953 €

With F1 and F2 fixed:
- volatility 14.40 %
- max drawdown 10.52 %
- VaR −1,855.58 €
- all-time high 161,749 €

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| F1 | CRIT | The flow-adjusted index forces factor 1 when a value is 0 and doesn't carry the 0 forward *(v)*. Every Morgan Stanley sell-all + wire followed by a new vest becomes a −77 % "day". | analytics/risk.go:236-250 | Compute the factor with denominator prev + inflows. Add a liquidate→re-enter test. |
| F2 | CRIT | The risk series is total net worth; asset purchases and revaluations count as returns. | analysis.go:913-930, 157-208 | Apartment = +1,400 % day; car = +40.7 %. Use cash + investment component. |
| F3 | CRIT | The FX chart is inverted with `1/rate`; ECB rates are already units per EUR *(v)*. | analysis.go:1501 | Shows "1 € = 0.91 USD"; actual 1.0989. |
| F4 | CRIT | "Lost to inflation" is always 0 € because the last index point is 100. | analysis.go:2314/2380 | Should be 1,541 €. |
| F5 | CRIT | Inflation index isn't chained across years; VPI is labelled HVPI; the 2025 rate is a placeholder. | analysis.go:2318-2336 | 2022 real values overstated 15 %. |
| F6 | CRIT | Benchmark comparison is not like-for-like: total net worth vs a deposit-only replay with no vests; negative withdrawals skipped. | analysis.go:2465-2561 | "+133,527 € ahead", of which 114,453 is property equity. |
| F7 | CRIT | Benchmark volatility annualises weekly data with √252. | risk.go:316, yahoo.go:109 | 31.4 % vs 14.3 % true. |
| F8 | CRIT | Concentration alerts skip directly held stocks. | analysis.go:628 | 90 % in Alphabet → no alert. |
| F9-F16 | WARN | Various. | | Negative withdrawals treated inconsistently. All-time high is synthetic. Sparplan streak stops at last purchase. VaR uses calendar days (16 % low). "With monthly investing" is the same for every user. Stress test treats mixed funds as 100 % equity and uses USD crisis figures. Nominal/real cards include deposits and don't state the window. Benchmark chart dates misaligned. |
| I | INFO | Minor issues. | | Risk-free 3 %. Fixed FX currency list. Leading zero snapshots plotted. |
| — | KNOWN | Cash-buffer months based on 5 % of securities. | | Confirmed. |

## Analysis: Costs, Exposure & Spending

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| C1 | CRIT | Spending's month denominator counts any month with any transaction, from any account. | analysis.go:3442-3574 | 42 months vs 5 with checking data. Housing 156.88 €/mo shown vs 1,317.80. |
| C2 | CRIT | Subscriptions are annualised from the lifetime charge count. | analysis.go:3523 | 15.99/mo seen 3× → 47.97/yr; yearly 120 seen 3× → 360. |
| C3 | CRIT | Regression in fe4671e: `SYNTH:<sha(etf|name)>` IDs never match across ETFs; `holdingKey` only knows `XX_` *(v)*. | analysis.go:26, etf.go:316 | Overlap drops from 17.4 % to 0 %. Top holdings are duplicated. ExposureTab.tsx:409 shows the hashes. |
| C4 | CRIT | Overlap uses top-10 constituents only. | etf.go:282 | Two MSCI World trackers ≈ 20 % instead of 95 %. The 70 % alert can't fire. |
| C5 | CRIT | Treemap sizes ETFs by their top-10 sum; all sectors are "Other". | decomposition.go:123 | A 10k ETF is drawn as 2k. |
| C6 | CRIT | Cost grade and saving use the TER diluted by stocks and unknown funds *(v)*. | analysis.go:1254-1275 | "A+, saving 53 €" vs B+, 8 €. |
| W1-W12 | WARN | Various. | | Fee drag is linear (30 y 600 € vs 2,519 € at 5 %). Fund data only for `etf`. Spending window and scope differ from Cashflow. Subscriptions include inflows. Correlation uses native currency and misaligned intervals (0.03 vs 0.29). Top holdings use an ETF-only denominator. Overlap isn't deduplicated by ISIN. Stocks always "Unknown" sector. Currency fallback treats an ETF as 100 % trading currency. Sector drift mixes cost and market weights. TCO fee span is wrong. Alternatives ignore switch tax; IE00BJ0KDQ92 is mislabelled ACWI. |
| — | KNOWN | 'Transfer' asymmetry. | | 91 % of the seed category chart; Morgan Stanley wires counted as spending. |

## Planning (`/planning`)

**Correct:** Estate calculator (§16/§19 ErbStG) and loan amortisation.

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| C1 | CRIT | The FIRE target is grossed up at marginal income tax × 0.7 on the whole withdrawal *(v)*. | Planning.tsx:174, portfolio.go:2196 | "Target portfolio" 1.03M vs independence date based on 1.46M: about 7 years apart. Use 26.375 % on the gain share only. |
| C2 | CRIT | FIRE, Human Capital and the Playbook start from total net worth including the house and car *(v)*. | Planning.tsx:183 | Progress 15.5 % vs 4.4 %. |
| C3 | CRIT | The 12-month contribution is gross deposits (root cause 2), and it is mislabelled when a goal overrides it. | portfolio.go:1889 | |
| C4 | CRIT | Pension Gap compares a nominal portfolio with needs in today's euros. | portfolio.go:3832 | Withdrawal 8,443 vs ~4,480 real (+88 %). |
| C5 | CRIT | Pension Gap tax and KV rules. | portfolio.go:3685-3767 | Grundfreibetrag applied per source. Stale constants (root cause 4). Besteuerungsanteil formula wrong. Flat 7.7 % KV on every source (statutory ≈ 12.35 %; private pensions 0 % for KVdR members). |
| C6 | CRIT | The Withdrawal Simulation never taxes Pension Gap sources (`tax_portion_pct: 0`). | Rentenluecke.tsx:62 | |
| C7 | CRIT | Withdrawal Simulation tax formulas. | WithdrawalSimulation.tsx:52-169 | Pension taxed at the full marginal rate. Brokerage at 18.5 % of the whole withdrawal. Precious metals get Teilfreistellung (should be §23 tax-free after 1 year). |
| C8 | CRIT | Retirement spending starts in today's euros while balances grow nominally. | WithdrawalSimulation.tsx:56-92 | Year-1 spending understated 47 %. |
| C9 | CRIT | "Tax-optimised order saves X" gives the basic-allowance band only to the "optimal" order. | WithdrawalSimulation.tsx:125-161 | 109k claimed vs 7.7k. |
| C10 | CRIT | Life-event "What to sell" ignores FIFO within a depot. | portfolio.go:3549 | Tax 0 shown vs ≥1,107.75. |
| C11 | CRIT | Contribution Router / unused accounts: stale caps; Rürup relief at 90 % (100 % since 2023); Rürup cap not reduced by GRV contributions; wrong Riester formula. | ContributionRouter.tsx, MoneyLeftOnTable.tsx | Rürup tax effect 11,578 shown vs ~6,697. |
| C12 | CRIT | Withdrawal Sequence uses hard-coded 83 %/80 % taxable shares (100 % for retirement in 2058) and treats checking as taxable. | WithdrawalSequence.tsx:148-180 | |
| W1-W7 | WARN | Various. | | FIRE: nominal vs real, withdrawals never inflation-indexed, pension counted from the FI date. Pension Gap: `start_age` ignored; early-retirement reduction applied down to 55 (−43 %; legally min. 63 and max. 14.4 %). Playbook disagrees with FIRE and Human Capital. Money-Left costs. Life-event offsets truncated; 0 % return becomes 7 %. bAV lock uses current age. bAV match assumed 50 %. |
| — | KNOWN | Liabilities listed as withdrawal sources; fixed life-event independence target. | | Confirmed. |

## Cashflow (`/cashflow`)

**Correct:** all arithmetic. Every month on the seed matches plain SQL exactly. The errors are in classification and in what Apply writes.

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| F1 | CRIT | Any unmatched inflow becomes "Refund" income: own-account transfers, Revolut top-ups, broker and Morgan Stanley wires. | cashflow.go:168-173 | A 5,000 Tagesgeld deposit makes March income 10,200. |
| F2 | CRIT | Payments to a broker or own savings account count as spending; the classifier has no investment or own-transfer keywords. | classifier.go:137-154 | 1,000 €/mo plan → savings rate 53.7 % vs 72.9 %. |
| F3 | CRIT | `fee` maps to CatInvestment, so bank fees and withheld tax disappear from spending *(v)*. | classifier.go:170 | |
| F4 | CRIT | Withdrawals containing ERSTATTUNG/RÜCKZAHLUNG reduce income instead of counting as spend *(v)*. | classifier.go:177 | |
| F5 | CRIT | "Annual income" is net inflow, not annualised; Apply writes it to `gross_annual_income`. | cashflow.go:253, Cashflow.tsx:118 | Seed: 26,000 for 5 months. Disability-insurance need 942 €/mo vs ~3,900. |
| F6-F10 | WARN | Various. | | Tax column ignored. Partial current month included in medians. "Typical net" = median income − median spend. Disagrees with the Spending tab (72.9 % vs −32.8 %). Apply keeps old values when a figure is ≤ 0. |

## Tax (`/tax`)

**Correct:** the FIFO core. An independent Python FIFO with ECB rates matches to the cent: 2023 tax 55.78, 2024 197.89, 2025 1,365.67.

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| C1 | CRIT | Loss pots are swapped: equity ETFs go to the Aktientopf and stocks to the general pot *(v)*. General losses never offset share gains. | tax.go:727, 766-780 | §20 Abs. 6 S. 4 EStG restricts only *share* losses. |
| C2 | CRIT | Overview and CSV export net all gains and losses together, ignoring the pots. | tax.go:945-967 | Stock −5k, ETF +5k, interest 600: tax 0 shown vs 817.63. |
| C3 | CRIT | Teilfreistellung not applied to fund losses. Overview and Loss-pots tabs disagree. | tax.go:948, analysis.go:2163 | ETF −5k + dividends 6k: 0 € vs 395.63 €. |
| C4 | CRIT | Interest is missing from the Overview summary *(v)*. | tax.go:886-943 | |
| C5 | CRIT | Anlage KAP: Morgan Stanley (foreign broker) income on lines 7ff instead of 18ff; line 8 mislabelled; line 16 filled with 1,000. | analysis.go:2236-2254 | Following it overpays 263.75 € for 2025 on the seed. Check line numbers against the official form for each year. |
| C6 | CRIT | An incoming depot transfer opens a new lot at transfer value and date; §43a Abs. 2 S. 3 requires the original. | tax.go:418/692/890 | |
| C7 | CRIT | Crypto and gold ETCs are taxed under §20 instead of §23. | analysis.go:81-155 | BTC held 2 years: 0 € correct vs 2,373.75 € shown. |
| C8 | CRIT | Vorabpauschale is never added to the next year's income or subtracted from sale gains; the table year is mislabelled. | §18 Abs. 3 / §19 Abs. 1 InvStG | |
| C9 | CRIT | Basiszins 2026 is 2.53; it should be 3.20 *(v)*. | tax.go:113 | |
| C10 | CRIT | FSA status ignores realised losses. | analysis.go:3162 | Gain 900 / loss 600 → "900 used" vs 300. |
| C11 | CRIT | "Tax if everything were sold" adds only lots in profit. | tax.go:478-483 | Seed: 10.37 € vs 0 €. |
| C12 | CRIT | Sparer-Pauschbetrag is 1,000 for every year (801 until 2022). | tax.go:15 | |
| W1-W7 | WARN | Various. | | Vorabpauschale pro-rated before the cap. Loss-harvest figures ignore FIFO and the pots. Joint filing and church tax applied only in parts of the page. FSA projections misleading. Steuerbescheid reconciliation parses "6.177" as 6.177 and never compares 0. Tests lock in wrong law. Foreign withholding tax mapped to KapESt lines. |
| I | INFO | Calendar dates. | | Adviser deadline for 2026 is 29.02.2028. 31.07.2027 is a Saturday → 02.08.2027. Vorabpauschale due 03.01.2028. The Verlustbescheinigung deadline (15 Dec) is not mentioned. |
| — | KNOWN | Sell-simulator church-tax rate; latest-FX cost basis; bond-ETF Teilfreistellung. | | Correct church-tax rates are 27.82 % / 27.995 %. The bond-ETF name fallback still gives 30 % to "Corp Bond", "Govies" and "LifeStrategy 20%". |

## Transactions & Settings

**Correct:** counts, pagination, filters and date bounds.

| # | Sev | Finding | Where | Evidence / fix |
|---|---|---|---|---|
| C1 | CRIT | Stored wealth reports are stale and can't be regenerated (409); none of them match the current snapshots *(v)*. | reports.go:164-187 | 88 of 133 show net worth 0. 2026-10 shows 47,425→47,210 vs 157,060→157,246. |
| C2 | CRIT | Negative seed withdrawals (root cause 1) *(v)*. | queries.sql | |
| C3 | CRIT | Row amount leaves out fee and tax, although the comment says it mirrors `txnSignedCashDelta`. | Transactions.tsx:46-57 | |
| C4 | CRIT | Scalable and N26 parsers lose signs. | scalable.go:270-307, n26.go:87-104 | Tax refund +45.10 shown as −45.10. Overdraft interest −4.12 shown as +4.12. |
| W1-W5 | WARN | Various. | | Morgan Stanley sale fees hidden in the net amount ($30.72 total). Import history capped at 50 and not scoped per user. Morgan Stanley vests need "Stock ISIN" or become cash outflows. ISHSIII EM ETF classified as stock (0 % Teilfreistellung). CSV export not scoped per user and fields not quoted. |
| I | INFO | Formatting. | format.ts | "−0,00 €". `fmtQty` keeps 4 decimals (0.00004 BTC → "0"). Month picker only 36 months. |

---

## Suggested fix order

1. **Tax filing correctness:** Tax C1-C5, C8, C9. These affect what goes into the Steuererklärung.
2. **Risk series:** Risk F1+F2. They make every Risk-tab number meaningless for RSU users.
3. **Goals:** Portfolio C1 + Net Worth F1. Goals are broken on both pages.
4. **Contribution source and scope:** root causes 2 and 3. They feed Projection, FIRE, Pension Gap and Life Events.
5. **Amount signs:** root cause 1, plus parser sign fixes and a DB constraint.
6. **Regression from fe4671e:** Exposure C3.
7. **Constants:** root cause 4.
