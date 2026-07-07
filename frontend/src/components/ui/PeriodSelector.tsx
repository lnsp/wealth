import { useState, useRef, useEffect, useLayoutEffect } from 'react';

export type Period = '1M' | '3M' | '6M' | 'YTD' | '1Y' | '3Y' | 'All';

const PERIODS: Period[] = ['1M', '3M', '6M', 'YTD', '1Y', '3Y', 'All'];

interface PeriodSelectorProps {
  value: Period;
  onChange: (period: Period) => void;
  periods?: Period[];
  className?: string;
}

export default function PeriodSelector({ value, onChange, periods = PERIODS, className = '' }: PeriodSelectorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [indicator, setIndicator] = useState({ left: 0, width: 0 });
  const [ready, setReady] = useState(false);

  // Position the sliding indicator behind the active button
  const updateIndicator = () => {
    if (!containerRef.current) return;
    const active = containerRef.current.querySelector('[data-active="true"]') as HTMLElement | null;
    if (active) {
      setIndicator({
        left: active.offsetLeft,
        width: active.offsetWidth,
      });
      setReady(true);
    }
  };

  useLayoutEffect(updateIndicator, [value, periods]);
  useEffect(() => {
    // Recalculate on resize (font loading, etc.)
    window.addEventListener('resize', updateIndicator);
    return () => window.removeEventListener('resize', updateIndicator);
  }, []);

  return (
    <div
      ref={containerRef}
      className={`relative inline-flex items-center gap-0.5 rounded-full bg-parchment-deep dark:bg-ink/10 p-0.5 ${className}`}
      role="group"
      aria-label="Time period"
    >
      {/* Sliding indicator */}
      <span
        className="absolute top-0.5 h-[calc(100%-4px)] rounded-full bg-inset border-l-[3px] border-forest dark:bg-inset border-l-[3px] border-forest border border-forest dark:border-forest transition-all duration-200 ease-out"
        style={{
          left: indicator.left,
          width: indicator.width,
          opacity: ready ? 1 : 0,
        }}
      />
      {periods.map((p) => (
        <button
          key={p}
          data-active={value === p}
          onClick={() => onChange(p)}
          className={`relative z-10 rounded-full px-3 md:px-3 py-2.5 md:py-1.5 text-[11px] md:text-[12px] font-medium transition-colors duration-150 min-h-[40px] md:min-h-[32px] ${
            value === p
              ? 'text-forest dark:text-sage'
              : 'text-ink-muted hover:text-ink-body'
          }`}
        >
          {p}
        </button>
      ))}
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
  const d = new Date(dateStr);
  switch (period) {
    case '1M':
      return d.toLocaleDateString('de-DE', { day: 'numeric', month: 'short' });
    case '3M':
    case '6M':
    case 'YTD':
      return d.toLocaleDateString('de-DE', { day: 'numeric', month: 'short' });
    case '1Y':
      return d.toLocaleDateString('de-DE', { month: 'short', year: '2-digit' });
    case '3Y':
    case 'All':
      return d.toLocaleDateString('de-DE', { month: 'short', year: '2-digit' });
  }
}
