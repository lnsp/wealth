export function escapeHtml(value: unknown): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function formatCurrencySafe(
  amount: number,
  currency?: string | null,
  options?: { maximumFractionDigits?: number; minimumFractionDigits?: number },
): string {
  const safeVal = Number.isFinite(amount) ? amount : 0;
  let cur = (currency || 'EUR').trim();
  let scaled = safeVal;
  if (cur === 'GBp' || cur === 'GBX') {
    cur = 'GBP';
    scaled = safeVal * 0.01;
  } else {
    cur = cur.toUpperCase() || 'EUR';
  }
  try {
    const nf = new Intl.NumberFormat('de-DE', {
      style: 'currency',
      currency: cur,
      ...options,
    });
    const out = nf.format(scaled);
    // A value that rounds to zero is shown unsigned ("0,00 €", not "-0,00 €").
    return /[1-9]/.test(out) ? out : nf.format(0);
  } catch {
    const formatted = scaled.toLocaleString('de-DE', {
      minimumFractionDigits: options?.minimumFractionDigits ?? 2,
      maximumFractionDigits: options?.maximumFractionDigits ?? 2,
    });
    return `${formatted} ${cur}`;
  }
}

/* ───────────────────────── Shared display formatters ─────────────────────────
 * Single locale for the whole app: German number formatting (decimal comma,
 * thousands dot), English month names. Use these instead of toFixed /
 * toLocaleString in components.
 */

export const LOCALE = 'de-DE';
const MINUS = '−';

/**
 * Formats |value| with `fmt`, then signs the rounded text: a value that rounds
 * to zero at the shown precision gets no sign ("0,00 €" for −0.004, not
 * "−0,00 €"; "0,0 %" rather than "+0,0 %").
 */
function signed(value: number, fmt: (abs: number) => string, sign: boolean): string {
  const body = fmt(Math.abs(value));
  if (!/[1-9]/.test(body)) return body;
  if (value < 0) return `${MINUS}${body}`;
  return sign ? `+${body}` : body;
}

const fixed = (digits: number) => (abs: number) =>
  abs.toLocaleString(LOCALE, { minimumFractionDigits: digits, maximumFractionDigits: digits });

function finite(n: number | null | undefined): number {
  return typeof n === 'number' && Number.isFinite(n) ? n : 0;
}

const minorUnitCache = new Map<string, number>();

/** Decimal places a currency is quoted in (ISO 4217 minor units via Intl): 2 for USD, 0 for JPY, 3 for KWD. */
function minorUnits(currency: string): number {
  let digits = minorUnitCache.get(currency);
  if (digits === undefined) {
    try {
      digits = new Intl.NumberFormat(LOCALE, { style: 'currency', currency }).resolvedOptions().maximumFractionDigits ?? 2;
    } catch {
      digits = 2;
    }
    minorUnitCache.set(currency, digits);
  }
  return digits;
}

export interface MoneyOptions {
  /** Show cents. Default false (whole units) — use true for single transactions and prices. */
  cents?: boolean;
  /** Prefix positive values with "+". */
  sign?: boolean;
}

/** Plain number, German grouping. `digits` fixes the decimals. */
export function fmtNum(value: number | null | undefined, digits = 0): string {
  return signed(finite(value), fixed(digits), false);
}

/** EUR amount: "12.480 €", "−84,30 €". */
export function fmtEUR(value: number | null | undefined, opts: MoneyOptions = {}): string {
  return `${signed(finite(value), fixed(opts.cents ? 2 : 0), !!opts.sign)} €`;
}

/**
 * Amount in any currency: symbol for EUR, ISO code for everything else ("97,60 USD").
 * EUR follows `cents` (whole euros by default). Other currencies show their own
 * minor units ("97,60 USD", "500 JPY") unless `cents: false` asks for whole units.
 */
export function fmtMoney(value: number | null | undefined, currency?: string | null, opts: MoneyOptions = {}): string {
  let cur = (currency || 'EUR').trim();
  let v = finite(value);
  if (cur === 'GBp' || cur === 'GBX') { cur = 'GBP'; v *= 0.01; }
  cur = cur.toUpperCase() || 'EUR';
  if (cur === 'EUR') return fmtEUR(v, opts);
  const d = opts.cents === false ? 0 : minorUnits(cur);
  return `${signed(v, fixed(d), !!opts.sign)} ${cur}`;
}

/** Percent from a percent value (7.4 → "7,4 %"). Pass `sign` for changes. */
export function fmtPct(value: number | null | undefined, opts: { digits?: number; sign?: boolean } = {}): string {
  return `${signed(finite(value), fixed(opts.digits ?? 1), !!opts.sign)} %`;
}

/**
 * Quantity: up to four decimals, trailing zeros dropped ("1.250", "5,123").
 * Below 1 it keeps four significant digits, so small crypto amounts don't
 * round away ("0,00004", "0,1235").
 */
export function fmtQty(value: number | null | undefined): string {
  const v = finite(value);
  const a = Math.abs(v);
  const opts: Intl.NumberFormatOptions = a > 0 && a < 1
    ? { maximumSignificantDigits: 4 }
    : { minimumFractionDigits: 0, maximumFractionDigits: 4 };
  return signed(v, (x) => x.toLocaleString(LOCALE, opts), false);
}

// Fixed three-letter months: en-GB gives "Sept", which is wider than the rest on chart axes.
const MONTHS_SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

// "2026-11-16", "2026-11", or the API's serialised DATE "2026-11-16T00:00:00Z".
const DATE_ONLY = /^(\d{4})-(\d{2})(?:-(\d{2}))?(?:T00:00:00(?:\.0+)?(?:Z|[+-]00:?00)?)?$/;

/**
 * Calendar dates are parsed as local dates. `new Date("2026-11-16")` (and the
 * API's "…T00:00:00Z") is UTC midnight, which is the previous day west of UTC.
 */
function toDate(d: string | number | Date): Date {
  if (d instanceof Date) return d;
  if (typeof d === 'string') {
    const m = DATE_ONLY.exec(d.trim());
    if (m) return new Date(Number(m[1]), Number(m[2]) - 1, m[3] ? Number(m[3]) : 1);
  }
  return new Date(d);
}

/** Timestamps keep their instant. */
function toInstant(d: string | number | Date): Date {
  return d instanceof Date ? d : new Date(d);
}

/** Numeric date for tables: "16.11.2026". */
export function fmtDate(d: string | number | Date | null | undefined): string {
  if (d === null || d === undefined || d === '') return '—';
  const dt = toDate(d);
  if (Number.isNaN(dt.getTime())) return '—';
  return dt.toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' });
}

/** Numeric date with time: "16.11.2026, 08:12". */
export function fmtDateTime(d: string | number | Date | null | undefined): string {
  if (d === null || d === undefined || d === '') return '—';
  const dt = toInstant(d);
  if (Number.isNaN(dt.getTime())) return '—';
  return dt.toLocaleString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
}

/** Written-out date for prose: "16 Nov 2026". */
export function fmtDateLong(d: string | number | Date | null | undefined): string {
  if (d === null || d === undefined || d === '') return '—';
  const dt = toDate(d);
  if (Number.isNaN(dt.getTime())) return '—';
  return `${dt.getDate()} ${MONTHS_SHORT[dt.getMonth()]} ${dt.getFullYear()}`;
}

/** Full month: "November 2026". */
export function fmtMonth(d: string | number | Date): string {
  return toDate(d).toLocaleDateString('en-GB', { month: 'long', year: 'numeric' });
}

/** Short month for chart axes and dense tables: "Nov ’26". */
export function fmtMonthShort(d: string | number | Date): string {
  const dt = toDate(d);
  return `${MONTHS_SHORT[dt.getMonth()]} ’${String(dt.getFullYear()).slice(-2)}`;
}

/** Day and short month for short-range chart axes: "16 Nov". */
export function fmtDayMonth(d: string | number | Date): string {
  const dt = toDate(d);
  return `${dt.getDate()} ${MONTHS_SHORT[dt.getMonth()]}`;
}

/** Compact axis label: "340k", "1,2M", "−100k". Lower-case k, upper-case M. */
export function fmtAxis(value: number | null | undefined): string {
  const v = finite(value);
  const trim = (n: number) => n.toLocaleString(LOCALE, { maximumFractionDigits: 1 });
  return signed(v, (a) => {
    if (a >= 1_000_000) return `${trim(a / 1_000_000)}M`;
    if (a >= 1_000) return `${trim(a / 1_000)}k`;
    return trim(a);
  }, false);
}

/** Display labels for raw enum values that reach the UI. */
const ENUM_LABELS: Record<string, string> = {
  buy: 'Buy',
  sell: 'Sale',
  dividend: 'Dividend',
  interest: 'Interest',
  deposit: 'Deposit',
  withdrawal: 'Withdrawal',
  transfer: 'Transfer',
  transfer_in: 'Transfer in',
  transfer_out: 'Transfer out',
  savings_plan: 'Savings plan',
  asset_purchase: 'Asset purchase',
  asset_sale: 'Asset sale',
  fee: 'Fee',
  tax: 'Tax',
  vest: 'Vesting',
  morgan_stanley: 'Morgan Stanley',
  scalable: 'Scalable Capital',
  trade_republic: 'Trade Republic',
  admin: 'Administrator',
  user: 'Member',
  readonly: 'Read-only',
  ok: 'Up to date',
  never: 'Never run',
  stale: 'Stale',
  error: 'Failed',
  failed: 'Failed',
  running: 'Running',
  completed: 'Completed',
  pending: 'Pending',
  member: 'Member',
  etf: 'ETF',
  etc: 'ETC',
  stock: 'Stock',
  bond: 'Bond',
  fund: 'Fund',
  crypto: 'Crypto',
  rsu: 'RSU',
  ntfy: 'ntfy',
};

/** Human label for an enum/identifier; falls back to sentence-casing snake_case. */
export function labelFor(value: string | null | undefined): string {
  if (!value) return '—';
  const key = value.toLowerCase();
  if (ENUM_LABELS[key]) return ENUM_LABELS[key];
  const words = key.replace(/[_-]+/g, ' ').trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}
