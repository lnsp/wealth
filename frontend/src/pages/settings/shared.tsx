import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

/** Row action sizes: 44px on phones, 36px on desktop. */
export const ROW_BTN = 'apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]';
export const ROW_BTN_DANGER = 'apple-btn-danger apple-btn-sm min-h-[44px] md:min-h-[36px]';

/** Card that holds an inline form (add account, add channel, …). */
export const FORM_CARD = 'rounded-apple border border-divider p-4 md:p-5 flex flex-col gap-4';

export async function fetchJson<T>(url: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const body = await resp.json().catch(() => null);
    throw new Error(body?.error || `Request failed (HTTP ${resp.status})`);
  }
  return resp.json() as Promise<T>;
}

export function errorText(e: unknown, fallback: string): string {
  const msg = e instanceof Error ? e.message : '';
  if (!msg) return fallback;
  return msg.charAt(0).toUpperCase() + msg.slice(1);
}

export interface LoadResult<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  /** Re-fetch. Keeps the current data on screen while loading. */
  reload: () => Promise<void>;
}

/** Fetch once on mount; expose loading / error / reload for one independently-loaded section. */
export function useLoad<T>(fn: () => Promise<T>): LoadResult<T> {
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const alive = useRef(true);

  const reload = useCallback(async () => {
    try {
      const d = await fnRef.current();
      if (!alive.current) return;
      setData(d);
      setError(null);
    } catch (e) {
      if (!alive.current) return;
      setError(errorText(e, 'Request failed'));
    } finally {
      if (alive.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    reload();
    return () => { alive.current = false; };
  }, [reload]);

  return { data, loading, error, reload };
}

/** Inline message for a failed action. */
export function InlineError({ children }: { children: ReactNode }) {
  if (!children) return null;
  return <p role="alert" className="text-[13px] text-claret">{children}</p>;
}

/** "1 warning" / "2 warnings". */
export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** Bordered list whose rows are separated by hairlines. */
export function RowList({ children }: { children: ReactNode }) {
  return <ul className="rounded-apple border border-divider divide-y divide-divider">{children}</ul>;
}
