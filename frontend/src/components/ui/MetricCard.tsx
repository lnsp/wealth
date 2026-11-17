import type { ReactNode } from 'react';

interface MetricCardProps {
  label: string;
  value: ReactNode;
  sublabel?: ReactNode;
  /** Colours the value. Zero and neutral figures stay ink — never claret for a non-negative amount. */
  tone?: 'neutral' | 'positive' | 'negative';
  /** Value is a monetary amount: blurred in privacy mode. Default true. */
  money?: boolean;
  className?: string;
  /** Escape hatch for legacy callers; prefer `tone`. */
  valueClassName?: string;
}

const TONE = { neutral: 'text-ink', positive: 'text-sage', negative: 'text-claret' } as const;

/** The one KPI tile: hairline above, caps label, serif figure, optional caption. */
export default function MetricCard({ label, value, sublabel, tone = 'neutral', money = true, className = '', valueClassName }: MetricCardProps) {
  return (
    <div className={`border-t border-divider pt-3 flex flex-col gap-0.5 min-w-0 ${className}`}>
      <span className="label-caps">{label}</span>
      <span
        className={`font-serif text-[24px] leading-8 ${valueClassName ?? TONE[tone]}`}
        data-privacy={money ? 'blur' : undefined}
      >
        {value}
      </span>
      {sublabel && <span className="text-[12px] text-ink-muted">{sublabel}</span>}
    </div>
  );
}

/** Responsive row of MetricCards: 4 across on desktop, 2 on phones, never a lone orphan below 150px. */
export function MetricGrid({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <div className={`grid gap-x-6 gap-y-4 grid-cols-2 md:grid-cols-[repeat(auto-fit,minmax(150px,1fr))] ${className}`}>
      {children}
    </div>
  );
}
