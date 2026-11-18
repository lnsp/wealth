import { fmtDayMonth, fmtMonthShort } from '../../utils/format';

export type Period = '1M' | '3M' | '6M' | 'YTD' | '1Y' | '3Y' | 'All';

const PERIODS: Period[] = ['1M', '3M', '6M', 'YTD', '1Y', '3Y', 'All'];

interface PeriodSelectorProps {
  value: Period;
  onChange: (period: Period) => void;
  periods?: Period[];
  className?: string;
}

export default function PeriodSelector({ value, onChange, periods = PERIODS, className = '' }: PeriodSelectorProps) {
  return (
    <div className={`inline-flex flex-wrap items-center gap-1.5 ${className}`} role="group" aria-label="Time period">
      {periods.map((p) => {
        const active = value === p;
        return (
          <button
            key={p}
            type="button"
            aria-pressed={active}
            onClick={() => onChange(p)}
            className={`min-h-[44px] md:min-h-[32px] min-w-[44px] md:min-w-[40px] px-2.5 rounded-[4px] border text-[12px] transition-colors ${
              active
                ? 'bg-forest border-forest text-parchment font-semibold'
                : 'border-field text-ink-body font-medium hover:border-forest hover:text-ink'
            }`}
          >
            {p}
          </button>
        );
      })}
    </div>
  );
}

/**
 * Convert a Period to a cutoff date.
 * Returns null for 'All' (no filtering).
 */
export function periodCutoff(period: Period): Date | null {
  const now = new Date();
  switch (period) {
    case '1M': { const d = new Date(now); d.setMonth(d.getMonth() - 1); return d; }
    case '3M': { const d = new Date(now); d.setMonth(d.getMonth() - 3); return d; }
    case '6M': { const d = new Date(now); d.setMonth(d.getMonth() - 6); return d; }
    case 'YTD': return new Date(now.getFullYear(), 0, 1);
    case '1Y': { const d = new Date(now); d.setFullYear(d.getFullYear() - 1); return d; }
    case '3Y': { const d = new Date(now); d.setFullYear(d.getFullYear() - 3); return d; }
    case 'All': return null;
  }
}

/** A local calendar day as "YYYY-MM-DD" (no shift to UTC). */
export function localDateKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/**
 * The calendar day of a series point. Snapshot and price dates are days sent
 * as UTC midnight ("2026-01-01T00:00:00Z"); comparing them as Date objects
 * with a local cutoff drops or shifts the boundary day, so compare the
 * "YYYY-MM-DD" prefix instead.
 */
export function dateKeyOf(date: string): string {
  return date.slice(0, 10);
}

/** A "YYYY-MM-DD" key as a local Date at midnight, for display. */
export function keyToLocalDate(key: string): Date {
  const [y, m, d] = key.slice(0, 10).split('-').map(Number);
  return new Date(y, m - 1, d);
}

/**
 * The day whose closing value a period's change is measured from: 31 Dec of
 * last year for YTD, otherwise the same calendar day one period ago (month
 * ends clamped, so 31 Mar − 1M is 28/29 Feb). Null for 'All'.
 */
export function periodBaseKey(period: Period, now: Date = new Date()): string | null {
  const back = (months: number) => {
    const first = new Date(now.getFullYear(), now.getMonth() - months, 1);
    const lastDay = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate();
    return localDateKey(new Date(first.getFullYear(), first.getMonth(), Math.min(now.getDate(), lastDay)));
  };
  switch (period) {
    case '1M': return back(1);
    case '3M': return back(3);
    case '6M': return back(6);
    case 'YTD': return `${now.getFullYear() - 1}-12-31`;
    case '1Y': return back(12);
    case '3Y': return back(36);
    case 'All': return null;
  }
}

/**
 * Filter a time-series array by period: every point from the period's base
 * day (see periodBaseKey) on, compared as calendar days so the boundary day
 * is kept in every time zone. Items must have a `date` field (ISO string).
 */
export function filterByPeriod<T extends { date: string }>(data: T[], period: Period): T[] {
  const base = periodBaseKey(period);
  if (!base) return data;
  return data.filter(item => dateKeyOf(item.date) >= base);
}

/**
 * The points of a newest-first series that a period covers, and the one its
 * change is measured from: the last point on or before the base day. When
 * the history is shorter than the period, the oldest point is the base and
 * `truncated` is true.
 */
export function periodWindow<T extends { date: string }>(newestFirst: T[], period: Period, now: Date = new Date()): { points: T[]; base: T | null; truncated: boolean } {
  if (newestFirst.length === 0) return { points: [], base: null, truncated: false };
  const key = periodBaseKey(period, now);
  const oldest = newestFirst[newestFirst.length - 1];
  if (!key) return { points: newestFirst, base: oldest, truncated: false };
  const i = newestFirst.findIndex(p => dateKeyOf(p.date) <= key);
  if (i < 0) return { points: newestFirst, base: oldest, truncated: true };
  return { points: newestFirst.slice(0, i + 1), base: newestFirst[i], truncated: false };
}

/**
 * Format a date string for chart x-axis labels, adapting to the selected period.
 */
export function formatDateForPeriod(dateStr: string, period: Period): string {
  switch (period) {
    case '1M':
    case '3M':
    case '6M':
    case 'YTD':
      return fmtDayMonth(dateStr);
    case '1Y':
    case '3Y':
    case 'All':
      return fmtMonthShort(dateStr);
  }
}
