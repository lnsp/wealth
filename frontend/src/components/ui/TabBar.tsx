import { useRef, useEffect, useCallback } from 'react';
import type { KeyboardEvent, ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';

interface Tab<T extends string> {
  id: T;
  label: string;
}

interface TabBarProps<T extends string> {
  tabs: Tab<T>[];
  activeTab: T;
  onTabChange: (tab: T) => void;
  /** Accessible name for the tablist, e.g. "Portfolio views". */
  ariaLabel?: string;
}

/**
 * Underlined tabs. Pair each tab with a <TabPanel id={tab.id}> so aria-controls resolves.
 * Arrow keys / Home / End move between tabs (roving tabindex).
 */
export default function TabBar<T extends string>({ tabs, activeTab, onTabChange, ariaLabel }: TabBarProps<T>) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    ref.current?.querySelector('[aria-selected="true"]')
      ?.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'smooth' });
  }, [activeTab]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = tabs.findIndex(t => t.id === activeTab);
    let next = -1;
    if (e.key === 'ArrowRight') next = (i + 1) % tabs.length;
    else if (e.key === 'ArrowLeft') next = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = tabs.length - 1;
    if (next < 0) return;
    e.preventDefault();
    onTabChange(tabs[next].id);
    requestAnimationFrame(() => {
      ref.current?.querySelector<HTMLButtonElement>(`#tab-${tabs[next].id}`)?.focus();
    });
  };

  return (
    <div className="border-b border-divider">
      <div ref={ref} className="flex gap-6 md:gap-7 scroll-fade-x scrollbar-hide -mb-px" role="tablist" aria-label={ariaLabel} onKeyDown={onKeyDown}>
        {tabs.map(tab => {
          const selected = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              id={`tab-${tab.id}`}
              role="tab"
              aria-selected={selected}
              aria-controls={`panel-${tab.id}`}
              tabIndex={selected ? 0 : -1}
              onClick={() => onTabChange(tab.id)}
              className={`min-h-[44px] px-0.5 text-[14px] whitespace-nowrap border-b-2 transition-colors last:mr-6 md:last:mr-0 ${
                selected
                  ? 'border-forest text-ink font-semibold'
                  : 'border-transparent text-ink-muted font-medium hover:text-ink'
              }`}
            >
              {tab.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}

/** The panel a tab controls. Render only the active one. */
export function TabPanel({ id, children, className = '' }: { id: string; children: ReactNode; className?: string }) {
  return (
    <div role="tabpanel" id={`panel-${id}`} aria-labelledby={`tab-${id}`} className={className}>
      {children}
    </div>
  );
}

/**
 * Tab state kept in the URL (?tab=…) so it survives reloads, error recovery and sharing.
 * Unknown or missing values fall back to `fallback`.
 */
export function useTabParam<T extends string>(ids: readonly T[], fallback: T, param = 'tab'): [T, (tab: T) => void] {
  const [params, setParams] = useSearchParams();
  const raw = params.get(param);
  const active = raw && (ids as readonly string[]).includes(raw) ? (raw as T) : fallback;
  const setTab = useCallback((tab: T) => {
    setParams(prev => {
      const next = new URLSearchParams(prev);
      if (tab === fallback) next.delete(param); else next.set(param, tab);
      return next;
    }, { replace: true });
  }, [setParams, fallback, param]);
  return [active, setTab];
}
