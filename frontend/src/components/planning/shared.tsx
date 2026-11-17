import type { ReactNode } from 'react';
import { fmtMonth, fmtMonthShort } from '../../utils/format';

// Small pieces shared by the planning calculators. Anything generic enough for other pages
// belongs in components/ui instead.

/** localStorage read that never throws (private windows, blocked storage). */
export function readStored(key: string, fallback = ''): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

export function writeStored(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch { /* storage unavailable: the value still lives in memory for this visit */ }
}

/** Parse a stored/typed numeric string; anything unparseable is 0. */
export function toNum(value: string | number | null | undefined): number {
  const n = typeof value === 'number' ? value : parseFloat(value ?? '');
  return Number.isFinite(n) ? n : 0;
}

/** "2031-04" (or a full ISO date) as "April 2031"; anything else is returned unchanged. */
export function monthLabel(value: string, short = false): string {
  if (!/^\d{4}-\d{2}/.test(value)) return value;
  const d = new Date(value.length === 7 ? `${value}-01` : value);
  if (Number.isNaN(d.getTime())) return value;
  return short ? fmtMonthShort(d) : fmtMonth(d);
}

/** Destructive row control: a real button, 44px on phones, named after the row it removes. */
export function RemoveButton({ name, onClick }: { name: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={`Remove ${name}…`}
      className="apple-btn-danger apple-btn-sm min-h-[44px] md:min-h-[36px] shrink-0"
    >
      Remove…
    </button>
  );
}

/** Bordered block for one editable row (a pension source, a policy, a life event). */
export function ItemCard({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`rounded-apple border border-divider p-4 ${className}`}>{children}</div>;
}

/** The assumptions a result rests on, stated in plain words next to that result. */
export function Assumptions({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <div className={`mt-4 border-t border-divider pt-3 ${className}`}>
      <p className="label-caps mb-1.5">Assumptions</p>
      <div className="flex flex-col gap-1 text-[13px] text-ink-body">{children}</div>
    </div>
  );
}

/** Horizontally scrollable, keyboard-reachable box for a table that cannot fit a phone. */
export function ScrollTable({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div role="region" tabIndex={0} aria-label={label} className="overflow-x-auto rounded-apple border border-divider">
      {children}
    </div>
  );
}

/** Text download (checklists, playbook). */
export function downloadText(filename: string, lines: string[]): void {
  const blob = new Blob([lines.join('\n')], { type: 'text/plain' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
