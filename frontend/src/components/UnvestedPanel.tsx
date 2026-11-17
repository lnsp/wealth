import { useState, useEffect, useCallback } from 'react';
import { api, type UnvestedResponse, type UnvestedAccount } from '../api/client';
import { Section, MetricCard, MetricGrid, ErrorState, Chip } from './ui';
import { fmtEUR, fmtMoney, fmtPct, fmtQty, fmtDate } from '../utils/format';

interface Props {
  mode: 'summary' | 'detail';
}

const DEFAULT_ROWS = 12;

export default function UnvestedPanel({ mode }: Props) {
  const [data, setData] = useState<UnvestedResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [expanded, setExpanded] = useState(false);

  const load = useCallback(() => {
    setError(null);
    setLoading(true);
    api.getUnvested()
      .then(setData)
      .catch(e => setError(e.message || 'Failed to load unvested RSUs'))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { load(); }, [load]);

  // People without RSU accounts never see this section, so it stays out of the way while loading.
  if (loading) return null;
  if (error) {
    return (
      <Section title="Unvested RSUs">
        <ErrorState message="Unvested RSUs couldn’t be loaded." details={error} onRetry={load} />
      </Section>
    );
  }
  if (!data || data.accounts.length === 0) return null;

  // Summary mode: compact display for the Net Worth page
  if (mode === 'summary') {
    return (
      <Section title="Unvested RSUs" subtitle="Estimated net value after withholding, converted to EUR at the latest ECB reference rate.">
        {data.accounts.map((acc, i) => (
          <UnvestedSummaryCard key={acc.account_id} acc={acc} isFirst={i === 0} />
        ))}
      </Section>
    );
  }

  const currencies = Object.keys(data.total_value_by_currency);
  const foreign = currencies.filter(c => c.toUpperCase() !== 'EUR');

  // Detail mode: full vesting schedule for the Portfolio page
  return (
    <Section
      title="Unvested RSUs"
      subtitle="Estimated net value after withholding. Not part of the holdings above until the shares vest."
    >
      <MetricGrid className="mb-6">
        <MetricCard
          label="Total · in EUR"
          value={fmtEUR(data.total_value_eur)}
          sublabel={foreign.length > 0 ? 'Converted at the latest ECB reference rate' : 'Estimated net value'}
        />
        {foreign.map(cur => (
          <MetricCard
            key={cur}
            label={`Of which · in ${cur.toUpperCase()}`}
            value={fmtMoney(data.total_value_by_currency[cur], cur, { cents: false })}
            sublabel="Before conversion, at the current share price"
          />
        ))}
      </MetricGrid>

      {data.accounts.map(acc => {
        const vests = acc.by_vest || [];
        const visibleVests = expanded ? vests : vests.slice(0, DEFAULT_ROWS);
        const cur = (acc.current_price_currency || acc.currency || 'EUR').toUpperCase();

        return (
          <div key={acc.account_id} className="mb-6 last:mb-0">
            {/* Account header */}
            <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1 mb-3">
              <div className="min-w-0">
                <h3 className="text-[15px] font-medium text-ink">{acc.account_name}</h3>
                <p className="text-[12px] text-ink-muted flex flex-wrap items-center gap-x-1.5 gap-y-1">
                  <span>{acc.symbol || acc.security_isin}</span>
                  <span aria-hidden="true">·</span>
                  <span>Net shares ≈ {fmtPct(acc.ratio * 100, { digits: 0 })} of gross</span>
                  {acc.default_ratio_used && <Chip tone="warning">Estimated ratio</Chip>}
                </p>
              </div>
              <div className="text-right shrink-0">
                <p className="text-[15px] font-medium text-ink" data-privacy="blur">
                  {fmtMoney(acc.total_value_estimate, cur, { cents: false })}
                </p>
                <p className="text-[12px] text-ink-muted">
                  {fmtQty(acc.total_net_estimate)} net shares at {fmtMoney(acc.current_price, cur, { cents: true })}
                </p>
              </div>
            </div>

            {/* Mobile: card layout */}
            <ul className="md:hidden flex flex-col gap-2">
              {visibleVests.map((v, i) => (
                <li key={i} className="rounded-apple border border-divider px-3 py-2.5 flex items-center justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-[14px] font-medium text-ink">{fmtDate(v.vest_date)}</p>
                    <p className="text-[12px] text-ink-muted truncate">Grant {v.grant_number}</p>
                  </div>
                  <div className="text-right shrink-0">
                    <p className="text-[14px] font-medium text-ink" data-privacy="blur">{fmtMoney(v.value_estimate, cur, { cents: false })}</p>
                    <p className="text-[12px] text-ink-muted">
                      {fmtQty(v.gross_quantity)} gross → {fmtQty(v.net_estimate)} net shares
                    </p>
                  </div>
                </li>
              ))}
            </ul>

            {/* Desktop: table layout */}
            <div className="hidden md:block overflow-x-auto rounded-apple border border-divider" role="region" tabIndex={0} aria-label={`Vesting schedule, ${acc.account_name}`}>
              <table className="apple-table">
                <thead>
                  <tr>
                    <th scope="col">Vest date</th>
                    <th scope="col">Grant</th>
                    <th scope="col" className="num">Gross shares</th>
                    <th scope="col" className="num">Net shares (est.)</th>
                    <th scope="col" className="num">Value (est.) · {cur}</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleVests.map((v, i) => (
                    <tr key={i}>
                      <td className="whitespace-nowrap">{fmtDate(v.vest_date)}</td>
                      <td>{v.grant_number}</td>
                      <td className="num">{fmtQty(v.gross_quantity)}</td>
                      <td className="num">{fmtQty(v.net_estimate)}</td>
                      <td className="num whitespace-nowrap"><span className="text-ink" data-privacy="blur">{fmtMoney(v.value_estimate, cur, { cents: false })}</span></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Show more / less toggle */}
            {vests.length > DEFAULT_ROWS && (
              <button
                type="button"
                onClick={() => setExpanded(prev => !prev)}
                aria-expanded={expanded}
                className="apple-btn-secondary w-full mt-3 min-h-[44px]"
              >
                {expanded ? 'Show fewer vests' : `Show all ${vests.length} vests`}
              </button>
            )}
          </div>
        );
      })}
    </Section>
  );
}

interface SummaryCardProps {
  acc: UnvestedAccount;
  isFirst: boolean;
}

function UnvestedSummaryCard({ acc, isFirst }: SummaryCardProps) {
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

  const share = acc.total_value_eur > 0 ? (next12Total / acc.total_value_eur) * 100 : 0;

  return (
    <div className={isFirst ? '' : 'mt-6 pt-5 border-t border-divider'}>
      {/* Header row */}
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 mb-4">
        <div className="min-w-0">
          <h3 className="text-[15px] font-medium text-ink truncate">{acc.account_name}</h3>
          <p className="text-[12px] text-ink-muted flex flex-wrap items-center gap-x-1.5 gap-y-1">
            {acc.symbol && <><span>{acc.symbol}</span><span aria-hidden="true">·</span></>}
            <span>{fmtQty(acc.total_net_estimate)} net shares</span>
            <span aria-hidden="true">·</span>
            <span>≈ {fmtPct(acc.ratio * 100, { digits: 0 })} of gross</span>
            {acc.default_ratio_used && <Chip tone="warning">Estimated ratio</Chip>}
          </p>
        </div>
        <span className="font-serif text-[22px] leading-7 text-ink shrink-0" data-privacy="blur">
          {fmtEUR(acc.total_value_eur)}
        </span>
      </div>

      <MetricGrid>
        <MetricCard
          label="Next vest"
          value={nextVest ? fmtEUR(nextVest.value_estimate_eur) : '—'}
          money={!!nextVest}
          sublabel={nextVest ? `on ${fmtDate(nextVest.vest_date)}` : 'No vest scheduled'}
        />
        <MetricCard
          label="Next 12 months"
          value={fmtEUR(next12Total)}
          sublabel={`${fmtPct(share, { digits: 0 })} of total`}
        />
        <MetricCard
          label="Beyond 12 months"
          value={fmtEUR(remainingTotal)}
          sublabel={`over ${remainingYears.size} ${remainingYears.size === 1 ? 'year' : 'years'}`}
        />
      </MetricGrid>

      {/* Year timeline */}
      {yearEntries.length > 1 && (
        <div className="mt-5 flex flex-col gap-2">
          {yearEntries.map(([yr, val]) => {
            const pct = maxYearValue > 0 ? (val / maxYearValue) * 100 : 0;
            return (
              <div key={yr} className="flex items-center gap-3 text-[13px]">
                <span className="w-10 shrink-0 text-ink-muted">{yr}</span>
                <div className="flex-1 h-2 bg-divider rounded-[2px] overflow-hidden" aria-hidden="true">
                  <div className="h-full bg-forest" style={{ width: `${pct}%` }} />
                </div>
                <span className="w-20 shrink-0 text-right text-ink" data-privacy="blur">{fmtEUR(val)}</span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
