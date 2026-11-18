import type { Account, NetWorthSnapshot } from '../../api/client';
import {
  ABGELTUNGSTEUER, BAV_MIN_EMPLOYER_SUBSIDY, BAV_SV_FREE_2026, BBG_RV_2026, EARLIEST_STATUTORY_AGE,
  GRUNDFREIBETRAG_2026, KV_RATE, KV_ZUSATZ_2026, PENSION_EARLY_PER_MONTH, PENSION_LATE_PER_MONTH,
  PV_RATE_2026, PV_RATE_CHILDLESS_2026, REGELALTERSGRENZE, RENTENWERT, RIESTER_GRUNDZULAGE,
  RIESTER_KINDERZULAGE, RIESTER_MAX, RUERUP_CAP_2026, RV_RATE_2026, SA_PAUSCHBETRAG,
  SOLI_FREIGRENZE_2026, SOLI_MILDERUNGSZONE, SOLI_RATE, VERSORGUNGSBEZUG_FREIBETRAG_2026,
  WK_PAUSCHBETRAG_RENTE,
} from './constants';

// Pure planning math, free of React so each rule can be checked by hand.
// The tax rules mirror internal/analytics/retirement_tax.go.

/** A stored percentage: unparseable → fallback, but a real 0 stays 0 (unlike `parseFloat(x) || 42`). */
export function parsePct(value: string | number | null | undefined, fallback: number): number {
  const n = typeof value === 'number' ? value : parseFloat(value ?? '');
  return Number.isFinite(n) ? n : fallback;
}

/** Cash + securities: total net worth without property, cars and the loans against them. */
export function investableNetWorth(s: NetWorthSnapshot | undefined): number {
  if (!s) return 0;
  return s.total - (s.physical_component ?? 0) + (s.debt_component ?? 0);
}

// ── Income tax ──────────────────────────────────────────────────────────────

/** § 32a EStG 2026 income tax on a taxable income; joint uses splitting. */
export function incomeTax2026(zvE: number, joint = false): number {
  if (joint) return 2 * tariff2026(Math.floor(zvE / 2));
  return tariff2026(zvE);
}

function tariff2026(zvE: number): number {
  const x = Math.floor(zvE);
  if (x <= GRUNDFREIBETRAG_2026) return 0;
  if (x <= 17799) {
    const y = (x - GRUNDFREIBETRAG_2026) / 10000;
    return Math.floor((914.51 * y + 1400) * y);
  }
  if (x <= 69878) {
    const z = (x - 17799) / 10000;
    return Math.floor((173.10 * z + 2397) * z + 1034.87);
  }
  if (x <= 277825) return Math.floor(0.42 * x - 11135.63);
  return Math.floor(0.45 * x - 19470.38);
}

/** Solidaritätszuschlag 2026: Freigrenze, then the lower of 5,5 % and 11,9 % of the excess. */
export function soli2026(incomeTax: number, joint = false): number {
  const limit = SOLI_FREIGRENZE_2026 * (joint ? 2 : 1);
  if (incomeTax <= limit) return 0;
  return Math.min(incomeTax * SOLI_RATE, (incomeTax - limit) * SOLI_MILDERUNGSZONE);
}

/** Income tax + Soli + church tax on a taxable income. */
export function personalTax(zvE: number, joint: boolean, churchRate: number): number {
  const est = incomeTax2026(Math.max(0, zvE), joint);
  return est + soli2026(est, joint) + est * Math.max(0, churchRate);
}

/** § 32d Abs. 1 EStG: Abgeltungsteuer + Soli + church tax, church tax deductible (26,375 % without). */
export function capitalIncomeTaxRate(churchRate = 0): number {
  const k = Math.min(0.09, Math.max(0, churchRate));
  return (1 / (4 + k)) * (1 + SOLI_RATE + k);
}

/** Flat tax on selling `gross` of a portfolio whose value is `gainShare` gain: gain only, after Teilfreistellung and the allowance. */
export function capitalGainsTax(gross: number, gainShare: number, tfs: number, rate: number, allowance: number): number {
  if (gross <= 0 || gainShare <= 0) return 0;
  const taxable = gross * Math.min(1, gainShare) * (1 - tfs) - Math.max(0, allowance);
  return taxable > 0 ? taxable * rate : 0;
}

// ── Pensions ────────────────────────────────────────────────────────────────

/** § 22 Nr. 1 S. 3 a aa EStG (Wachstumschancengesetz): taxable share of a statutory or Rürup pension by start year. */
export function besteuerungsanteil(startYear: number): number {
  if (startYear <= 2005) return 0.5;
  if (startYear <= 2020) return 0.5 + 0.02 * (startYear - 2005);
  if (startYear <= 2022) return 0.8 + 0.01 * (startYear - 2020);
  if (startYear >= 2058) return 1;
  return 0.825 + 0.005 * (startYear - 2023);
}

/** § 22 Nr. 1 S. 3 a bb EStG: Ertragsanteil of a private annuity by age at the start. */
export function ertragsanteil(startAge: number): number {
  const table: [number, number][] = [
    [50, 30], [52, 29], [53, 28], [54, 27], [56, 26], [57, 25], [58, 24], [59, 23], [61, 22], [62, 21],
    [63, 20], [64, 19], [66, 18], [67, 17], [68, 16], [70, 15], [71, 14], [73, 13], [74, 12], [75, 11],
  ];
  for (const [maxAge, pct] of table) if (startAge <= maxAge) return pct / 100;
  return 0.1;
}

/** § 77 SGB VI Zugangsfaktor: −0,3 %/month before 67 (not before 63), +0,5 %/month after. */
export function statutoryAgeFactor(claimAge: number): number {
  const age = Math.max(claimAge, EARLIEST_STATUTORY_AGE);
  const months = (age - REGELALTERSGRENZE) * 12;
  return months < 0 ? 1 + months * PENSION_EARLY_PER_MONTH : 1 + months * PENSION_LATE_PER_MONTH;
}

/**
 * Taxable share of a pension source. A stored share > 0 is used as entered; 0 or missing means
 * "derive it": statutory and Rürup by start year, private annuities by start age, everything
 * else (bAV, Riester, other income) in full.
 */
export function pensionTaxableShare(type: string, startYear: number, startAge: number, storedPct?: number | null): number {
  if (storedPct != null && storedPct > 0) return Math.min(1, storedPct / 100);
  if (type === 'gesetzliche' || type === 'ruerup') return besteuerungsanteil(startYear);
  if (type === 'private') return ertragsanteil(startAge);
  return 1;
}

/** Monthly KV + PV a KVdR member pays on a statutory pension. */
export function statutoryHealthMonthly(gross: number, childless: boolean): number {
  if (gross <= 0) return 0;
  return gross * ((KV_RATE + KV_ZUSATZ_2026) / 2 + (childless ? PV_RATE_CHILDLESS_2026 : PV_RATE_2026));
}

/** Monthly KV + PV on all company pensions together (one § 226 Freibetrag). Private, Riester and Rürup pay none. */
export function occupationalHealthMonthly(totalGross: number, childless: boolean): number {
  if (totalGross <= 0) return 0;
  const kv = Math.max(0, totalGross - VERSORGUNGSBEZUG_FREIBETRAG_2026) * (KV_RATE + KV_ZUSATZ_2026);
  const pv = totalGross > VERSORGUNGSBEZUG_FREIBETRAG_2026 ? totalGross * (childless ? PV_RATE_CHILDLESS_2026 : PV_RATE_2026) : 0;
  return kv + pv;
}

/** Taxable income from pensions: taxable parts minus the pension allowances and the deductible KV/PV. */
export function pensionZvE(annualTaxable: number, annualHealth: number): number {
  if (annualTaxable <= 0) return 0;
  return Math.max(0, annualTaxable - WK_PAUSCHBETRAG_RENTE - SA_PAUSCHBETRAG - Math.max(0, annualHealth));
}

// ── Accounts ────────────────────────────────────────────────────────────────

export type AccountTaxClass = 'cash' | 'capital' | 'private_sale' | 'bav' | 'riester' | 'rurup' | 'excluded';

/**
 * How withdrawals from an account are taxed. Cash is keyed on the account type first, because
 * `tax_treatment` defaults to 'taxable' for every account. Loans, credit cards and property are
 * not withdrawal sources.
 */
export function accountTaxClass(a: Pick<Account, 'type' | 'tax_treatment'>): AccountTaxClass {
  if (a.type === 'liability' || a.type === 'credit' || a.type === 'real_estate') return 'excluded';
  if (a.type === 'checking' || a.type === 'savings') return 'cash';
  if (a.tax_treatment === 'bav' || a.tax_treatment === 'riester' || a.tax_treatment === 'rurup') return a.tax_treatment;
  if (a.type === 'crypto' || a.type === 'precious_metals') return 'private_sale';
  if (a.tax_treatment === 'savings') return 'cash';
  return 'capital';
}

// ── Contribution routing (2026 limits) ──────────────────────────────────────

/** Rürup room: the § 10 Abs. 3 cap minus employee + employer statutory pension contributions. */
export function rurupRoom(grossIncome: number): number {
  const rv = grossIncome > 0 ? RV_RATE_2026 * Math.min(grossIncome, BBG_RV_2026) : 0;
  return Math.max(0, RUERUP_CAP_2026 - rv);
}

/**
 * Riester: the 2.100 € cap includes the allowances, so your own money is 2.100 € − allowances.
 * The deduction (§ 10a) is worth cap × marginal rate; the Finanzamt pays the part above the
 * allowances (Günstigerprüfung), so the total state support is the larger of the two.
 */
export function riesterPlan(marginalPct: number, children: number) {
  const zulagen = RIESTER_GRUNDZULAGE + Math.max(0, children) * RIESTER_KINDERZULAGE;
  const own = Math.max(0, RIESTER_MAX - zulagen);
  const deduction = RIESTER_MAX * Math.max(0, marginalPct) / 100;
  const support = Math.max(zulagen, deduction);
  return { zulagen, own, deduction, support, supportPct: support / RIESTER_MAX * 100 };
}

/** Statutory minimum employer subsidy on a bAV (15 % of the social-security-free conversion). */
export const BAV_MIN_SUBSIDY_ANNUAL = BAV_MIN_EMPLOYER_SUBSIDY * BAV_SV_FREE_2026;

// ── Human capital ───────────────────────────────────────────────────────────

/** Present value of `years` of gross income: year 1 earns `income`, then it grows by `growth`, discounted at `discount`. */
export function humanCapitalPV(income: number, growth: number, discount: number, years: number, fromYear = 0): number {
  let pv = 0;
  for (let r = fromYear + 1; r <= years; r++) {
    pv += income * Math.pow(1 + growth, r - 1) / Math.pow(1 + discount, r - fromYear);
  }
  return pv;
}

export type CapitalPoint = { yearIndex: number; human: number; financial: number };

/** Remaining human capital against financial capital, year by year until retirement. */
export function humanCapitalCrossover(p: {
  income: number; growth: number; discount: number; years: number;
  startWealth: number; monthlyContrib: number; portfolioGrowth: number;
}): { series: CapitalPoint[]; crossoverIndex: number | null } {
  const series: CapitalPoint[] = [];
  let fin = p.startWealth;
  for (let y = 0; y <= p.years; y++) {
    series.push({ yearIndex: y, human: humanCapitalPV(p.income, p.growth, p.discount, p.years, y), financial: fin });
    fin = (fin + p.monthlyContrib * 12) * (1 + p.portfolioGrowth);
  }
  const hit = series.find(d => d.financial >= d.human);
  return { series, crossoverIndex: hit ? hit.yearIndex : null };
}

// ── Withdrawal simulation ───────────────────────────────────────────────────

export type SimPension = {
  type: string;
  /** Yearly gross in today's euros at the start age. */
  annualGross: number;
  startAge: number;
  taxableShare: number;
  /** Already after tax and health insurance (the FIRE Calculator's pension figure). */
  isNet?: boolean;
};

export type WithdrawalParams = {
  retireAge: number;
  yearsToRetire: number;
  firstCalendarYear: number;
  /** Yearly spending in today's euros. */
  annualNeed: number;
  inflation: number;
  returnRate: number;
  cashRate: number;
  pensionAdj: number;
  /** Balances at retirement (nominal). Capital is taxed under § 20, private-sale assets are tax-free after a year (§ 23). */
  capital: number;
  capitalBasis: number;
  privateSale: number;
  cash: number;
  tfs: number;
  capRate: number;
  allowance: number;
  pensions: SimPension[];
  childless: boolean;
  joint: boolean;
  churchRate: number;
  years: number;
  careFromAge?: number | null;
  /** Extra yearly care cost in today's euros. */
  careCost?: number;
};

export type SimYear = {
  year: number; age: number; need: number;
  fromBrokerage: number; fromCash: number; fromPension: number;
  taxPaid: number; health: number; remaining: number; shortfall: number;
};

export type WithdrawalOrder = 'optimal' | 'brokerage_first' | 'cash_first';

/**
 * Year-by-year retirement simulation in euros of each year. Spending is inflated from today
 * ((1 + i)^(years to retirement + y)); pensions entered in today's euros rise by the pension
 * adjustment from today. Pension income is taxed with the § 32a tariff on the year's total
 * (the tariff assumed to rise with prices), after the pension allowances and the deductible
 * KV/PV. Brokerage sales are taxed on their gain share only, after Teilfreistellung and the
 * saver's allowance, with the Günstigerprüfung (§ 32d Abs. 6) — capital gains are taxed at the
 * personal rate when that is lower — for every withdrawal order alike.
 */
export function simulateWithdrawals(p: WithdrawalParams, order: WithdrawalOrder): SimYear[] {
  let capital = p.capital;
  let basis = Math.min(p.capitalBasis, p.capital);
  let priv = p.privateSale;
  let cash = p.cash;
  const out: SimYear[] = [];

  for (let y = 0; y < p.years; y++) {
    const age = p.retireAge + y;
    const infl = Math.pow(1 + p.inflation, p.yearsToRetire + y);
    const adj = Math.pow(1 + p.pensionAdj, p.yearsToRetire + y);
    const need = p.annualNeed * infl + (p.careFromAge != null && age >= p.careFromAge ? (p.careCost ?? 0) * infl : 0);

    // Pensions paid this year.
    let gross = 0;
    let taxable = 0;
    let netGiven = 0;
    let statutoryGross = 0;
    let bavGross = 0;
    for (const src of p.pensions) {
      if (age < src.startAge || src.annualGross <= 0) continue;
      const g = src.annualGross * adj;
      if (src.isNet) { netGiven += g; continue; }
      gross += g;
      taxable += g * src.taxableShare;
      if (src.type === 'gesetzliche') statutoryGross += g;
      if (src.type === 'bav') bavGross += g;
    }
    const health = 12 * (statutoryHealthMonthly(statutoryGross / 12, p.childless) + occupationalHealthMonthly(bavGross / 12, p.childless));
    // Tax in today's euros with today's tariff, then back to this year's euros.
    const zvEReal = pensionZvE(taxable / infl, health / infl);
    const pensionTax = personalTax(zvEReal, p.joint, p.churchRate) * infl;
    const netPension = Math.max(0, gross - health - pensionTax) + netGiven;

    const gainShare = capital > 0 ? Math.max(0, 1 - basis / capital) : 0;
    const taxablePerEuro = gainShare * (1 - p.tfs);
    // Tax on selling `x` of the brokerage this year, with the Günstigerprüfung.
    const capTax = (x: number) => {
      const k = Math.max(0, x * taxablePerEuro - p.allowance);
      if (k <= 0) return 0;
      const flat = k * p.capRate;
      const tariff = (personalTax(zvEReal + k / infl, p.joint, p.churchRate) - personalTax(zvEReal, p.joint, p.churchRate)) * infl;
      return Math.min(flat, tariff);
    };
    // Brokerage sale whose after-tax proceeds are `net`, with `already` sold before this call.
    const sellFor = (net: number, already: number) => {
      const avail = capital - already;
      if (net <= 0 || avail <= 0) return 0;
      const netOf = (x: number) => x - (capTax(already + x) - capTax(already));
      if (netOf(avail) <= net) return avail;
      let lo = 0, hi = avail;
      for (let i = 0; i < 50; i++) {
        const mid = (lo + hi) / 2;
        if (netOf(mid) < net) lo = mid; else hi = mid;
      }
      return hi;
    };

    let remaining = Math.max(0, need - netPension);
    let sold = 0;
    let fromCash = 0;
    const takeCash = () => {
      const c = Math.min(remaining, cash);
      cash -= c; fromCash += c; remaining -= c;
      const pr = Math.min(remaining, priv);
      priv -= pr; fromCash += pr; remaining -= pr;
    };
    const takeBrokerage = () => {
      const x = sellFor(remaining, sold);
      const net = x - (capTax(sold + x) - capTax(sold));
      sold += x; remaining = Math.max(0, remaining - net);
    };
    if (order === 'optimal') {
      // Fill the tax-free band first: the saver's allowance plus any basic allowance the pensions leave unused.
      const freeGain = p.allowance + Math.max(0, GRUNDFREIBETRAG_2026 - zvEReal) * infl;
      const freeSale = taxablePerEuro > 0 ? freeGain / taxablePerEuro : capital;
      const x = Math.min(remaining, capital, freeSale);
      sold += x; remaining -= x - capTax(x);
      takeCash();
      takeBrokerage();
    } else if (order === 'brokerage_first') {
      takeBrokerage();
      takeCash();
    } else {
      takeCash();
      takeBrokerage();
    }
    remaining = Math.max(0, remaining);

    const tax = pensionTax + capTax(sold);
    if (capital > 0) basis *= 1 - sold / capital;
    capital -= sold;
    capital *= 1 + p.returnRate;
    priv *= 1 + p.returnRate;
    cash *= 1 + p.cashRate;

    out.push({
      year: p.firstCalendarYear + y, age, need: Math.round(need),
      fromBrokerage: Math.round(sold), fromCash: Math.round(fromCash), fromPension: Math.round(netPension),
      taxPaid: Math.round(tax), health: Math.round(health),
      remaining: Math.round(capital + priv + cash), shortfall: Math.round(remaining),
    });
  }
  return out;
}

/** Age at which the money first falls short, or null. */
export function depletionAge(sim: SimYear[]): number | null {
  return sim.find(y => y.shortfall > 0)?.age ?? null;
}

/** Statutory pension at the claim age from pension points, in today's euros a year. */
export function statutoryAnnual(points: number, claimAge: number): number {
  return points * RENTENWERT * statutoryAgeFactor(claimAge) * 12;
}

/** Combined flat rate on capital income for display (e.g. 26,375 % → "26,4 %"). */
export const FLAT_RATE_NO_CHURCH = ABGELTUNGSTEUER * (1 + SOLI_RATE);
