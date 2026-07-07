import { useState, useEffect } from 'react';
import { api, type UnvestedResponse, type UnvestedAccount } from '../api/client';

interface Props {
  mode: 'summary' | 'detail';
}

export default function UnvestedPanel({ mode }: Props) {
  const [data, setData] = useState<UnvestedResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    api.getUnvested()
      .then(setData)
      .catch(e => setError(e.message || 'Failed to load unvested RSUs'))
      .finally(() => setLoading(false));
  }, []);

  if (loading) return null;
  if (error) {
    return (
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <div className="flex items-center justify-between">
          <p className="text-[13px] text-claret">{error}</p>
          <button
            onClick={() => {
              setError(null);
              setLoading(true);
              api.getUnvested()
                .then(setData)
                .catch(e => setError(e.message || 'Failed to load unvested RSUs'))
                .finally(() => setLoading(false));
            }}
            className="text-forest text-[13px] font-medium"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }
  if (!data || data.accounts.length === 0) return null;

  const fmt = (n: number, currency = 'EUR') =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency }).format(n);

  const fmtCompact = (n: number) =>
    new Intl.NumberFormat('de-DE', {
      style: 'currency',
      currency: 'EUR',
      maximumFractionDigits: 0,
    }).format(n);

  const fmtQty = (n: number) =>
    new Intl.NumberFormat('de-DE', { minimumFractionDigits: 0, maximumFractionDigits: 3 }).format(n);

  // Summary mode: compact display for NetWorth page
  if (mode === 'summary') {
    return (
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        {data.accounts.map((acc, i) => (
          <UnvestedSummaryCard
            key={acc.account_id}
            acc={acc}
            isFirst={i === 0}
            fmt={fmt}
            fmtCompact={fmtCompact}
            fmtQty={fmtQty}
          />
        ))}
      </div>
    );
  }

  const totalValue = data.total_value_eur;

  // Detail mode: full table for Portfolio page
  const DEFAULT_ROWS = 12;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <div className="flex items-center justify-between mb-3 md:mb-4 px-1 md:px-0">
        <div>
          <h2 className="font-serif text-heading text-ink">Unvested RSUs</h2>
          <p className="text-[13px] text-ink-muted mt-0.5">
            Estimated net value after withholding
          </p>
        </div>
        <span className="font-serif text-[20px] md:text-[22px] text-ink tabular-nums">
          {fmt(totalValue)}
        </span>
      </div>

      {data.accounts.map(acc => {
        const vests = acc.by_vest || [];
        const visibleVests = expanded ? vests : vests.slice(0, DEFAULT_ROWS);

        return (
          <div key={acc.account_id} className="mb-4 px-1 md:px-0">
            {/* Account header */}
            <div className="flex items-center justify-between mb-2">
              <div>
                <p className="text-[15px] font-medium text-ink">{acc.account_name}</p>
                <p className="text-[12px] text-ink-muted">
                  {acc.symbol || acc.security_isin}
                  <span className="mx-1.5">&middot;</span>
                  Net/Gross ratio: {(acc.ratio * 100).toFixed(0)}%
                  {acc.default_ratio_used && (
                    <span className="text-amber ml-1">(estimated)</span>
                  )}
                </p>
              </div>
              <div className="text-right shrink-0 ml-3">
                <p className="text-[15px] font-medium tabular-nums">
                  {fmt(acc.total_value_estimate, acc.current_price_currency)}
                </p>
                <p className="text-[12px] text-ink-muted tabular-nums">
                  {fmtQty(acc.total_net_estimate)} net @ {fmt(acc.current_price, acc.current_price_currency)}
                </p>
              </div>
            </div>

            {/* Mobile: card layout */}
            <div className="md:hidden space-y-2">
              {visibleVests.map((v, i) => (
                <div key={i} className="rounded-apple bg-parchment-deep px-3 py-2.5">
                  <div className="flex items-center justify-between">
                    <div>
                      <p className="text-[13px] font-medium text-ink">
                        {new Date(v.vest_date).toLocaleDateString('de-DE', {
                          day: '2-digit', month: 'short', year: 'numeric',
                        })}
                      </p>
                      <p className="text-[11px] text-ink-muted">{v.grant_number}</p>
                    </div>
                    <div className="text-right">
                      <p className="text-[13px] font-medium tabular-nums">
                        {fmt(v.value_estimate, acc.current_price_currency)}
                      </p>
                      <p className="text-[11px] text-ink-muted tabular-nums">
                        {fmtQty(v.gross_quantity)} gross &rarr; {fmtQty(v.net_estimate)} net
                      </p>
                    </div>
                  </div>
                </div>
              ))}
            </div>

            {/* Desktop: table layout */}
            <div className="hidden md:block overflow-x-auto">
              <table className="w-full min-w-[600px]">
                <thead>
                  <tr className="text-left font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em]">
                    <th className="pb-2 font-medium">Vest Date</th>
                    <th className="pb-2 font-medium">Grant</th>
                    <th className="pb-2 font-medium text-right">Gross Shares</th>
                    <th className="pb-2 font-medium text-right">Net Estimate</th>
                    <th className="pb-2 font-medium text-right">Value Estimate</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleVests.map((v, i) => (
                    <tr
                      key={i}
                      className={`transition-colors hover:bg-parchment-deep ${
                        i < visibleVests.length - 1 ? 'border-b border-divider' : ''
                      }`}
                    >
                      <td className="py-2.5 text-[15px] text-ink tabular-nums">
                        {new Date(v.vest_date).toLocaleDateString('de-DE', {
                          day: '2-digit', month: '2-digit', year: 'numeric',
                        })}
                      </td>
                      <td className="py-2.5 text-[13px] text-ink-muted">{v.grant_number}</td>
                      <td className="py-2.5 text-[15px] text-right tabular-nums">{fmtQty(v.gross_quantity)}</td>
                      <td className="py-2.5 text-[15px] text-right tabular-nums">{fmtQty(v.net_estimate)}</td>
                      <td className="py-2.5 text-[15px] text-right tabular-nums font-medium">
                        {fmt(v.value_estimate, acc.current_price_currency)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Show more / less toggle */}
            {vests.length > DEFAULT_ROWS && (
              <button
                onClick={() => setExpanded(prev => !prev)}
                className="w-full mt-2 py-2 text-[13px] text-forest font-medium"
              >
                {expanded
                  ? 'Show less'
                  : `Show all ${vests.length} vests`}
              </button>
            )}
          </div>
        );
      })}

      {/* Total by currency */}
      {Object.keys(data.total_value_by_currency).length > 1 && (
        <div className="border-t border-divider pt-3 mt-2 px-1 md:px-0">
          <p className="text-[12px] text-ink-muted mb-1">Total by Currency</p>
          <div className="flex gap-4">
            {Object.entries(data.total_value_by_currency).map(([cur, val]) => (
              <span key={cur} className="text-[13px] font-medium tabular-nums text-ink">
                {fmt(val, cur)}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

interface SummaryCardProps {
  acc: UnvestedAccount;
  isFirst: boolean;
  fmt: (n: number, currency?: string) => string;
  fmtCompact: (n: number) => string;
  fmtQty: (n: number) => string;
}

function UnvestedSummaryCard({ acc, isFirst, fmt, fmtCompact, fmtQty }: SummaryCardProps) {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const oneYear = new Date(today);
  oneYear.setFullYear(oneYear.getFullYear() + 1);

  // Vests come from the API sorted by date; defensively re-sort so we can rely on it.
  const futureVests = (acc.by_vest || [])
    .filter(v => new Date(v.vest_date) >= today)
    .sort((a, b) => a.vest_date.localeCompare(b.vest_date));

  const nextVest = futureVests[0];
  const next12Total = futureVests
    .filter(v => new Date(v.vest_date) < oneYear)
    .reduce((sum, v) => sum + v.value_estimate_eur, 0);
  const remainingTotal = futureVests
    .filter(v => new Date(v.vest_date) >= oneYear)
    .reduce((sum, v) => sum + v.value_estimate_eur, 0);
  const remainingYears = new Set(
    futureVests.filter(v => new Date(v.vest_date) >= oneYear).map(v => v.vest_date.slice(0, 4))
  );

  // Year-by-year bucket for the timeline bars.
  const byYear = new Map<string, number>();
  for (const v of futureVests) {
    const yr = v.vest_date.slice(0, 4);
    byYear.set(yr, (byYear.get(yr) ?? 0) + v.value_estimate_eur);
  }
  const yearEntries = Array.from(byYear.entries()).sort((a, b) => a[0].localeCompare(b[0]));
  const maxYearValue = yearEntries.reduce((m, [, v]) => Math.max(m, v), 0);

  return (
    <div className={isFirst ? '' : 'mt-5 pt-5 border-t border-divider'}>
      {/* Header row */}
      <div className="flex items-baseline justify-between gap-3 mb-1 px-1 md:px-0">
        <div className="min-w-0">
          <h2 className="font-serif text-heading text-ink truncate">
            {isFirst ? 'Unvested RSUs' : acc.account_name}
          </h2>
          <p className="text-[12px] text-ink-muted mt-0.5">
            {isFirst && <span className="truncate">{acc.account_name} · </span>}
            {acc.symbol && <>{acc.symbol} · </>}
            {fmtQty(acc.total_net_estimate)} net shares · Net/Gross ~{Math.round(acc.ratio * 100)}%
            {acc.default_ratio_used && <span className="text-amber ml-1">(estimated)</span>}
          </p>
        </div>
        <span className="font-serif text-[20px] md:text-[22px] text-ink tabular-nums shrink-0">
          {fmt(acc.total_value_eur)}
        </span>
      </div>

      {/* Stat tiles */}
      <div className="grid grid-cols-3 gap-px bg-divider mt-3 rounded-apple overflow-hidden">
        <div className="bg-parchment-deep px-3 py-2.5 min-w-0">
          <p className="font-serif text-[10px] tracking-[0.1em] uppercase text-ink-muted mb-1" style={{ fontVariantCaps: 'small-caps' }}>
            Next vest
          </p>
          {nextVest ? (
            <>
              <p className="text-[15px] font-medium tabular-nums text-ink truncate">
                {new Date(nextVest.vest_date).toLocaleDateString('de-DE', { day: '2-digit', month: 'short', year: '2-digit' })}
              </p>
              <p className="text-[11px] text-ink-muted tabular-nums mt-0.5 truncate">
                ~{fmtCompact(nextVest.value_estimate_eur)}
              </p>
            </>
          ) : (
            <p className="text-[15px] text-ink-muted">—</p>
          )}
        </div>
        <div className="bg-parchment-deep px-3 py-2.5 min-w-0">
          <p className="font-serif text-[10px] tracking-[0.1em] uppercase text-ink-muted mb-1" style={{ fontVariantCaps: 'small-caps' }}>
            Next 12 months
          </p>
          <p className="text-[15px] font-medium tabular-nums text-ink truncate">
            {fmtCompact(next12Total)}
          </p>
          <p className="text-[11px] text-ink-muted mt-0.5">
            {Math.round((next12Total / Math.max(acc.total_value_eur, 1)) * 100)}% of total
          </p>
        </div>
        <div className="bg-parchment-deep px-3 py-2.5 min-w-0">
          <p className="font-serif text-[10px] tracking-[0.1em] uppercase text-ink-muted mb-1" style={{ fontVariantCaps: 'small-caps' }}>
            Beyond 12 mo
          </p>
          <p className="text-[15px] font-medium tabular-nums text-ink truncate">
            {fmtCompact(remainingTotal)}
          </p>
          <p className="text-[11px] text-ink-muted mt-0.5">
            over {remainingYears.size} {remainingYears.size === 1 ? 'year' : 'years'}
          </p>
        </div>
      </div>

      {/* Year timeline */}
      {yearEntries.length > 1 && (
        <div className="mt-4 space-y-1.5 px-1 md:px-0">
          {yearEntries.map(([yr, val]) => {
            const pct = maxYearValue > 0 ? (val / maxYearValue) * 100 : 0;
            return (
              <div key={yr} className="flex items-center gap-3 text-[12px]">
                <span className="w-10 shrink-0 text-ink-muted tabular-nums">{yr}</span>
                <div className="flex-1 h-2.5 bg-divider rounded-full overflow-hidden">
                  <div
                    className="h-full bg-forest rounded-full transition-all"
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <span className="w-20 shrink-0 text-right tabular-nums text-ink">
                  {fmtCompact(val)}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
