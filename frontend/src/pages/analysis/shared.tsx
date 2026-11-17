import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Chip, ErrorState, LoadingState } from '../../components/ui';
import { fmtEUR } from '../../utils/format';

/* ───────────── data loading ───────────── */

export interface LoadState<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  reload: () => void;
}

/** Loads one resource for one section. Each section owns its request, so a failure stays local. */
export function useLoad<T>(loader: () => Promise<T>, deps: readonly unknown[] = []): LoadState<T> {
  const [state, setState] = useState<{ data: T | null; loading: boolean; error: string | null }>({ data: null, loading: true, error: null });
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let cancelled = false;
    setState(s => ({ ...s, loading: true, error: null }));
    loader()
      .then(data => { if (!cancelled) setState({ data, loading: false, error: null }); })
      .catch((e: unknown) => {
        if (!cancelled) setState({ data: null, loading: false, error: e instanceof Error ? e.message : String(e) });
      });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tick, ...deps]);
  const reload = useCallback(() => setTick(t => t + 1), []);
  return { ...state, reload };
}

/** Renders loading, error (with retry) or the loaded content — never a blank section. */
export function Loaded<T>({ state, lines, children }: { state: LoadState<T>; lines?: number; children: (data: T) => ReactNode }) {
  if (state.data === null && state.loading) return <LoadingState lines={lines} />;
  if (state.data === null) return <ErrorState onRetry={state.reload} details={state.error ?? undefined} />;
  return <>{children(state.data)}</>;
}

export async function getJson<T>(url: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const body = await resp.json().catch(() => null) as { error?: string } | null;
    throw new Error(`Request failed (${resp.status}): ${body?.error || resp.statusText || 'Unknown error'}`);
  }
  return resp.json() as Promise<T>;
}

export function postJson<T>(url: string, body?: unknown): Promise<T> {
  return getJson<T>(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
}

/** API arrays can arrive as null or be missing; always hand the UI an array. */
export function arr<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

/** API objects used as maps can arrive as null. */
export function rec<V>(value: Record<string, V> | null | undefined): Record<string, V> {
  return value && typeof value === 'object' ? value : {};
}

export function num(value: number | null | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** Triggers a background job (fund data, historical prices) and reports what happened in words. */
export function useJobTrigger(endpoint: string, onStarted?: () => void) {
  const [status, setStatus] = useState<'idle' | 'running' | 'started' | 'failed'>('idle');
  const run = useCallback(() => {
    setStatus('running');
    postJson<unknown>(endpoint)
      .then(() => {
        setStatus('started');
        // The job runs in the background; look again once it has had a moment.
        if (onStarted) window.setTimeout(onStarted, 8000);
      })
      .catch(() => setStatus('failed'));
  }, [endpoint, onStarted]);
  const message =
    status === 'running' ? 'Starting…'
    : status === 'started' ? 'Refresh started. This can take a minute; the section reloads by itself.'
    : status === 'failed' ? 'The refresh could not be started. Try again from Settings, Data.'
    : null;
  return { run, status, message };
}

/* ───────────── small presentational pieces ───────────── */

type Tone = 'neutral' | 'positive' | 'negative';
const TONE_CLASS: Record<Tone, string> = { neutral: 'text-ink', positive: 'text-sage', negative: 'text-claret' };

/** Tone for a signed change: zero stays ink. */
export function toneOf(value: number): Tone {
  return value > 0 ? 'positive' : value < 0 ? 'negative' : 'neutral';
}

/** A EUR amount, blurred in privacy mode. */
export function Money({ value, sign, cents, tone, className = '' }: { value: number | null | undefined; sign?: boolean; cents?: boolean; tone?: Tone; className?: string }) {
  return <span data-privacy="blur" className={`${tone ? TONE_CLASS[tone] : ''} ${className}`}>{fmtEUR(value, { sign, cents })}</span>;
}

/** Compact one-line statement used instead of a chart when there is nothing to compare. */
export function Statement({ children }: { children: ReactNode }) {
  return <p className="rounded-apple border border-divider px-4 py-3 text-[14px] text-ink-body">{children}</p>;
}

type CalloutTone = 'danger' | 'warning' | 'success' | 'info';
const CALLOUT_BORDER: Record<CalloutTone, string> = { danger: 'border-claret', warning: 'border-amber', success: 'border-sage', info: 'border-forest' };

/** Notice with a text chip, so the level is never carried by colour alone. */
export function Callout({ tone, label, title, children }: { tone: CalloutTone; label: string; title?: ReactNode; children?: ReactNode }) {
  return (
    <div className={`border-l-2 ${CALLOUT_BORDER[tone]} pl-3 py-1`}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Chip tone={tone}>{label}</Chip>
        {title && <span className="text-[14px] font-medium text-ink">{title}</span>}
      </div>
      {children && <div className="mt-1 text-[13px] text-ink-body">{children}</div>}
    </div>
  );
}

/** Horizontally scrollable box for a table that cannot fit a phone; keyboard reachable. */
export function TableRegion({ label, children, className = '' }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div role="region" aria-label={label} tabIndex={0} className={`overflow-x-auto rounded-apple border border-divider ${className}`}>
      {children}
    </div>
  );
}

/** Label / value rows for phone cards. */
export function KeyValues({ rows }: { rows: { label: string; value: ReactNode }[] }) {
  return (
    <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1 text-[13px]">
      {rows.map(r => (
        <div key={r.label} className="contents">
          <dt className="text-ink-muted">{r.label}</dt>
          <dd className="text-right text-ink">{r.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Sub-heading row with right-aligned controls that wrap under the heading on phones. */
export function SubHeadingRow({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mt-6 mb-2 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
      <h3 className="text-[13px] leading-5 font-semibold text-ink">{children}</h3>
      {actions}
    </div>
  );
}

export interface ShareBarItem {
  label: string;
  /** Size of the bar. */
  value: number;
  /** Formatted figure, e.g. "1.450 €". */
  display: string;
  /** Formatted share, e.g. "40,2 %". */
  share?: string;
  /** Short text flag next to the label, e.g. "Above 10 %". */
  note?: string;
  /** The display value is money: blur it in privacy mode. */
  money?: boolean;
}

/**
 * Ranked bars with the label on its own line on phones, so long names ("United States")
 * are never clipped. Local to Analysis; same visual language as ui/RankedBars.
 */
export function ShareBars({ items, scale = 'max' }: { items: ShareBarItem[]; scale?: 'max' | 'total' | number }) {
  const denom = typeof scale === 'number' ? scale
    : scale === 'max' ? Math.max(...items.map(i => Math.abs(i.value)), 0)
    : items.reduce((s, i) => s + Math.abs(i.value), 0);
  return (
    <ul className="flex flex-col gap-3">
      {items.map(item => (
        <li key={item.label} className="grid items-center gap-x-3 gap-y-1 text-[13px] grid-cols-[1fr_auto_auto] md:grid-cols-[minmax(140px,1fr)_minmax(80px,2fr)_auto_auto]">
          <span className="min-w-0 text-ink break-words">
            {item.label}
            {item.note && <span className="ml-2 text-[12px] text-ink-muted">{item.note}</span>}
          </span>
          <span className="order-last col-span-3 md:order-none md:col-span-1 h-2 rounded-[2px] bg-divider overflow-hidden" aria-hidden="true">
            <span className="block h-full bg-forest" style={{ width: `${denom > 0 ? Math.min(100, (Math.abs(item.value) / denom) * 100) : 0}%` }} />
          </span>
          <span className="text-right text-ink min-w-[56px]" data-privacy={item.money ? 'blur' : undefined}>{item.display}</span>
          {item.share !== undefined ? <span className="text-right text-ink-muted min-w-[52px]">{item.share}</span> : <span className="hidden md:block" />}
        </li>
      ))}
    </ul>
  );
}

/** Disclosure chevron; rotates when open. */
export function Chevron({ open }: { open: boolean }) {
  return (
    <svg className={`w-4 h-4 shrink-0 text-ink-muted transition-transform ${open ? 'rotate-90' : ''}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M9 6l6 6-6 6" />
    </svg>
  );
}

/* ───────────── chart helpers ───────────── */

/** Legend icons that show the line style, for charts where two lines differ by dash. */
export const LEGEND_LINE_SOLID = 'path://M0,0L24,0L24,3L0,3Z';
export const LEGEND_LINE_DASHED = 'path://M0,0L6,0L6,3L0,3ZM9,0L15,0L15,3L9,3ZM18,0L24,0L24,3L18,3Z';
export const LEGEND_LINE_DOTTED = 'path://M0,0L3,0L3,3L0,3ZM7,0L10,0L10,3L7,3ZM14,0L17,0L17,3L14,3ZM21,0L24,0L24,3L21,3Z';

/** Legend block whose icons are short line samples (solid / dashed / dotted). */
export function lineLegend(items: { name: string; style?: 'solid' | 'dashed' | 'dotted' }[]) {
  return {
    bottom: 0,
    itemWidth: 24,
    itemHeight: 3,
    data: items.map(i => ({
      name: i.name,
      icon: i.style === 'dashed' ? LEGEND_LINE_DASHED : i.style === 'dotted' ? LEGEND_LINE_DOTTED : LEGEND_LINE_SOLID,
    })),
  };
}

/** Blend two token colours (hex) so heatmap cells keep readable ink labels in both themes. */
export function mixHex(a: string, b: string, t: number): string {
  const parse = (h: string): [number, number, number] | null => {
    const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(h.trim());
    if (!m) return null;
    const s = m[1].length === 3 ? m[1].split('').map(c => c + c).join('') : m[1];
    return [parseInt(s.slice(0, 2), 16), parseInt(s.slice(2, 4), 16), parseInt(s.slice(4, 6), 16)];
  };
  const ca = parse(a);
  const cb = parse(b);
  if (!ca || !cb) return b;
  const ch = (i: number) => Math.round(ca[i] + (cb[i] - ca[i]) * t).toString(16).padStart(2, '0');
  return `#${ch(0)}${ch(1)}${ch(2)}`;
}
