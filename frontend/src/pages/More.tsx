import { Link } from 'react-router-dom';
import type { ReactNode } from 'react';

interface Item { to: string; label: string; hint: string; icon: ReactNode }

const svg = (d: string) => (
  <svg className="w-5 h-5 text-forest shrink-0" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={d} /></svg>
);

const PAGES: Item[] = [
  { to: '/analysis', label: 'Analysis', hint: 'Risk, costs, exposure and performance', icon: svg('M5 20V10M12 20V4M19 20v-7') },
  { to: '/planning', label: 'Planning', hint: 'Financial independence, pension gap, protection', icon: svg('M5 21V4h11l-2 4 2 4H5') },
  { to: '/tax', label: 'Tax', hint: 'Allowance, loss pots, calendar and filing', icon: svg('M6 3h12v18l-3-2-3 2-3-2-3 2zM9 8h6M9 12h6') },
];

const ACCOUNT: Item[] = [
  { to: '/settings', label: 'Settings', hint: 'Data, accounts, security, notifications', icon: svg('M4 7h10M18 7h2M4 17h2M10 17h10M16 5v4M8 15v4') },
];

function Row({ item }: { item: Item }) {
  return (
    <Link to={item.to} className="flex items-center gap-3.5 min-h-[64px] px-1 py-2 border-b border-divider text-ink">
      {item.icon}
      <span className="flex-1 min-w-0 flex flex-col">
        <span className="font-serif text-[19px] leading-6 font-medium text-ink">{item.label}</span>
        <span className="text-[13px] text-ink-muted">{item.hint}</span>
      </span>
      <svg className="w-4 h-4 text-ink-muted shrink-0" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M9 6l6 6-6 6" /></svg>
    </Link>
  );
}

/** Mobile "More" tab: the pages that don't fit the five-tab bar. Also reachable on desktop at /more. */
export default function More({ onLogout }: { onLogout?: () => void }) {
  return (
    <div>
      <h1 className="hidden md:block font-serif text-title text-ink mb-6">More</h1>
      <h2 className="label-caps pb-2 border-b border-ink">Pages</h2>
      {PAGES.map(i => <Row key={i.to} item={i} />)}
      <h2 className="label-caps mt-7 pb-2 border-b border-ink">Account</h2>
      {ACCOUNT.map(i => <Row key={i.to} item={i} />)}
      {onLogout && (
        <button type="button" onClick={onLogout} className="w-full flex items-center gap-3.5 min-h-[64px] px-1 py-2 border-b border-divider text-left">
          {svg('M15 8V5H5v14h10v-3M10 12h11M18 9l3 3-3 3')}
          <span className="font-serif text-[19px] leading-6 font-medium text-ink">Sign out</span>
        </button>
      )}
    </div>
  );
}
