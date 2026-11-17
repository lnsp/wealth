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

/**
 * Filter a time-series array by period.
 * Items must have a `date` field (ISO string).
 */
export function filterByPeriod<T extends { date: string }>(data: T[], period: Period): T[] {
  const cutoff = periodCutoff(period);
  if (!cutoff) return data;
  return data.filter(item => new Date(item.date) >= cutoff);
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
