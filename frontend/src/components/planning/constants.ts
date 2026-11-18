// Legal and statistical constants for the planning calculators, in one place.
// Every value names its source and the year it is valid for. The backend
// mirrors them in internal/analytics/retirement_tax.go; change both together.
// Review each January (tax values, social-security figures) and July
// (Rentenwert).

// ── Income tax ──────────────────────────────────────────────────────────────
/** § 32a Abs. 1 EStG, VZ 2026 (Steuerfortentwicklungsgesetz): Grundfreibetrag. Zone formulas in planningMath.incomeTax2026. */
export const GRUNDFREIBETRAG_2026 = 12348;
/** SolzG 1995 § 3 Abs. 3: no Soli up to 20.350 € income tax (single) in 2026; § 4: at most 11,9 % of the excess. */
export const SOLI_FREIGRENZE_2026 = 20350;
export const SOLI_RATE = 0.055;
export const SOLI_MILDERUNGSZONE = 0.119;
/** § 9a S. 1 Nr. 3 EStG Werbungskosten-Pauschbetrag on pension income; § 10c EStG Sonderausgaben-Pauschbetrag. */
export const WK_PAUSCHBETRAG_RENTE = 102;
export const SA_PAUSCHBETRAG = 36;

// ── Capital income ──────────────────────────────────────────────────────────
/** § 32d Abs. 1 EStG Abgeltungsteuer. */
export const ABGELTUNGSTEUER = 0.25;
/** § 20 Abs. 9 EStG Sparer-Pauschbetrag since 2023 (doubled for joint filers). */
export const SPARER_PAUSCHBETRAG = 1000;
/** § 20 Abs. 1 InvStG Teilfreistellung for equity funds (≥ 51 % equities). */
export const TFS_EQUITY_FUND = 0.3;

// ── Statutory pension ───────────────────────────────────────────────────────
/** § 68 SGB VI, Rentenwertbestimmungsverordnung 2026: 42,52 € per point and month from 1 July 2026 (40,79 € before). */
export const RENTENWERT = 42.52;
/** § 35 SGB VI Regelaltersgrenze for anyone born 1964 or later. */
export const REGELALTERSGRENZE = 67;
/** §§ 36, 236 SGB VI: earliest old-age pension (35 years insured) at 63; § 77 SGB VI −0,3 % a month early (max. 14,4 %), +0,5 % a month late. */
export const EARLIEST_STATUTORY_AGE = 63;
export const PENSION_EARLY_PER_MONTH = 0.003;
export const PENSION_LATE_PER_MONTH = 0.005;
/** Earliest payout of Rürup (§ 10 Abs. 1 Nr. 2 b aa EStG), Riester (§ 1 Abs. 1 Nr. 2 AltZertG) and tax-favoured bAV: age 62 for contracts since 2012. */
export const PRIVATE_PENSION_EARLIEST_AGE = 62;

// ── Health and care insurance of retirees (KVdR), 2026 ─────────────────────
/** § 241 SGB V general rate; KVdR members pay half on the statutory pension (§ 249a), all of it on a bAV (§ 248). */
export const KV_RATE = 0.146;
/** § 242a SGB V average Zusatzbeitrag 2026, set by the BMG. */
export const KV_ZUSATZ_2026 = 0.029;
/** § 55 SGB XI Pflegeversicherung 2026: 3,6 %, childless 4,2 %; retirees pay it in full. */
export const PV_RATE_2026 = 0.036;
export const PV_RATE_CHILDLESS_2026 = 0.042;
/** § 226 Abs. 2 SGB V: 1/20 of the monthly Bezugsgröße (3.955 € in 2026) — Freibetrag for KV, Freigrenze for PV on a bAV. */
export const VERSORGUNGSBEZUG_FREIBETRAG_2026 = 197.75;

// ── Tax-advantaged saving, 2026 ─────────────────────────────────────────────
/** Beitragsbemessungsgrenze allgemeine Rentenversicherung 2026 (annual). */
export const BBG_RV_2026 = 101400;
/** § 158 SGB VI statutory pension contribution (employee + employer). */
export const RV_RATE_2026 = 0.186;
/** § 3 Nr. 63 EStG: 8 % of the BBG RV tax-free (8.112 €), 4 % also free of social security (4.056 €). */
export const BAV_TAX_FREE_2026 = 8112;
export const BAV_SV_FREE_2026 = 4056;
/** § 1a Abs. 1a BetrAVG: employers pass on at least 15 % of converted salary, as far as they save social security. */
export const BAV_MIN_EMPLOYER_SUBSIDY = 0.15;
/** § 10 Abs. 3 EStG: Basisvorsorge cap 2026 = 24,7 % × 124.800 € (knappschaftliche BBG) = 30.826 €, 100 % deductible since 2023, shared with statutory pension contributions. */
export const RUERUP_CAP_2026 = 30826;
/** § 10a Abs. 1 EStG: at most 2.100 € a year including the allowances; § 84 Grundzulage 175 €, § 85 Kinderzulage 300 € (children born from 2008). */
export const RIESTER_MAX = 2100;
export const RIESTER_GRUNDZULAGE = 175;
export const RIESTER_KINDERZULAGE = 300;

// ── Planning assumptions (not law) ──────────────────────────────────────────
export const DEFAULT_RETURN_PCT = 7;
/** ECB medium-term inflation target. */
export const DEFAULT_INFLATION_PCT = 2;
/** Marginal income-tax rate when Your Profile has none. */
export const DEFAULT_MARGINAL_RATE_PCT = 42;
/** Emergency reserve in months of expenses (Contribution Router and Money Left on the Table). */
export const RESERVE_MONTHS = 6;
