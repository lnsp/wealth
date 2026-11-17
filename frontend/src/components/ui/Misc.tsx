import type { ButtonHTMLAttributes, ReactNode } from 'react';

type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'info';
const CHIP_TONE: Record<Tone, string> = {
  neutral: '',
  success: 'border-sage text-sage',
  warning: 'border-amber text-amber',
  danger: 'border-claret text-claret',
  info: 'border-forest text-forest',
};

/** Outlined chip with text — status is never colour alone. */
export function Chip({ tone = 'neutral', children, className = '', title }: { tone?: Tone; children: ReactNode; className?: string; title?: string }) {
  return <span title={title} className={`apple-chip ${CHIP_TONE[tone]} ${className}`}>{children}</span>;
}

/** Dashed "+ Add …" button for adding a row or item. */
export function AddChip({ children, className = '', ...rest }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button type="button" {...rest} className={`inline-flex items-center gap-1.5 min-h-[44px] md:min-h-[40px] px-3.5 rounded-[4px] border border-dashed border-ink-muted text-[14px] font-medium text-forest hover:border-forest transition-colors ${className}`}>
      <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
      {children}
    </button>
  );
}

export interface RankedBarItem {
  label: string;
  /** Numeric size of the bar. */
  value: number;
  /** Formatted amount, e.g. "1.450 €". */
  display: string;
  /** Formatted share, e.g. "40,2 %". */
  share?: string;
}

/**
 * Ranked horizontal bars: label, bar, amount, share. Use instead of a donut whenever
 * categories must be told apart — one colour, identity carried by the label.
 * `scale="max"` sizes bars against the largest item, `scale="total"` against the sum.
 */
export function RankedBars({ items, scale = 'max', className = '' }: { items: RankedBarItem[]; scale?: 'max' | 'total'; className?: string }) {
  const denom = scale === 'max'
    ? Math.max(...items.map(i => Math.abs(i.value)), 0)
    : items.reduce((s, i) => s + Math.abs(i.value), 0);
  return (
    <div className={`flex flex-col gap-3 ${className}`}>
      {items.map((item) => (
        <div key={item.label} className="grid items-center gap-2.5 text-[13px] grid-cols-[minmax(96px,1fr)_minmax(40px,2fr)_auto_auto]">
          <span className="text-ink break-words">{item.label}</span>
          <span className="h-2 rounded-[2px] bg-divider overflow-hidden" aria-hidden="true">
            <span className="block h-full bg-forest" style={{ width: `${denom > 0 ? Math.min(100, (Math.abs(item.value) / denom) * 100) : 0}%` }} />
          </span>
          <span className="text-right text-ink min-w-[64px]" data-privacy="blur">{item.display}</span>
          {item.share !== undefined && <span className="text-right text-ink-muted min-w-[48px]">{item.share}</span>}
        </div>
      ))}
    </div>
  );
}

/** Thin progress bar with an accessible name and a text value beside it at the call site. */
export function ProgressBar({ value, max = 100, label, className = '' }: { value: number; max?: number; label: string; className?: string }) {
  const pct = max > 0 ? Math.max(0, Math.min(100, (value / max) * 100)) : 0;
  return (
    <div role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={max} aria-valuenow={Math.round(value)} className={`h-2 rounded-[2px] bg-divider overflow-hidden ${className}`}>
      <div className="h-full bg-forest" style={{ width: `${pct}%` }} />
    </div>
  );
}
