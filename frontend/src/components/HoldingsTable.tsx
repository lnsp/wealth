import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import type { HoldingRow } from '../api/client';
import { Chip, EmptyState } from './ui';
import { fmtEUR, fmtMoney, fmtPct, fmtQty, labelFor } from '../utils/format';

/** A single holding above this share of the portfolio is flagged as a concentration risk. */
export const CONCENTRATION_LIMIT_PCT = 30;

export const holdingKey = (h: HoldingRow) => `${h.account_id}-${h.security_isin}`;

interface Props {
  holdings: HoldingRow[];
  /** Key (see `holdingKey`) of the row whose detail is open. */
  expandedKey?: string | null;
  /** Called when a security's name button is pressed. Omit to render plain, non-expandable rows. */
  onToggle?: (holding: HoldingRow) => void;
  /** Detail content, rendered directly under the expanded row. */
  detail?: ReactNode;
}

/**
 * Currency facts the holdings API guarantees:
 *  - `current_price` is in `price_currency`;
 *  - `avg_cost_paid` is per unit in `avg_cost_currency`, the currency the units were paid in;
 *  - `market_value`, `unrealized_pl`, `value_eur`, `cost_basis_eur` and `avg_cost_eur` are EUR, the
 *    cost at each purchase date's ECB rate (so the gain includes currency moves since purchase).
 * For unpriced rows `value_eur` is the EUR cost. Gain % always divides EUR by EUR.
 */
function eurFigures(h: HoldingRow) {
  const priced = h.market_value != null && h.unrealized_pl != null;
  const value = h.market_value ?? h.value_eur;
  const gain = priced ? (h.unrealized_pl as number) : null;
  const cost = h.cost_basis_eur ?? (gain != null ? value - gain : h.value_eur);
  const gainPct = gain != null && cost > 0 ? (gain / cost) * 100 : null;
  return { value, gain, cost, gainPct };
}

/**
 * Concentration is a property of the security, not of one account's row: two 20 % rows of the
 * same ISIN are a 40 % position. Falls back to the row weight for older responses.
 */
function concentrationPct(h: HoldingRow): number | null {
  return h.isin_weight_pct ?? h.weight_pct;
}

function toneClass(n: number | null): string {
  if (n == null || n === 0) return 'text-ink';
  return n > 0 ? 'text-sage' : 'text-claret';
}

function useIsDesktop(): boolean {
  const query = '(min-width: 1024px)';
  const [match, setMatch] = useState(() => typeof window === 'undefined' || !window.matchMedia ? true : window.matchMedia(query).matches);
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return;
    const mq = window.matchMedia(query);
    const on = () => setMatch(mq.matches);
    on();
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, []);
  return match;
}

function Chevron({ open }: { open: boolean }) {
  return (
    <svg className="w-4 h-4 shrink-0 text-ink-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={open ? 'M6 9l6 6 6-6' : 'M9 6l6 6-6 6'} />
    </svg>
  );
}

/** Percent label, bar on a fixed-width track, and — below the bar — the concentration chip. */
function WeightCell({ pct, isinPct, multiAccount }: { pct: number | null; isinPct: number | null; multiAccount: boolean }) {
  if (pct == null) return <span className="text-ink-muted">—</span>;
  const across = multiAccount && isinPct != null && Math.abs(isinPct - pct) >= 0.05;
  return (
    <>
      <div className="flex items-center gap-2">
        <span className="w-[52px] shrink-0 text-ink">{fmtPct(pct)}</span>
        <span className="flex-1 h-1.5 rounded-[3px] bg-divider overflow-hidden" aria-hidden="true">
          <span className="block h-full bg-forest" style={{ width: `${Math.max(0, Math.min(pct, 100))}%` }} />
        </span>
      </div>
      {isinPct != null && isinPct > CONCENTRATION_LIMIT_PCT && (
        <Chip tone="warning" className="mt-1.5">
          {across ? `${fmtPct(isinPct)} across accounts · over ${CONCENTRATION_LIMIT_PCT} % limit` : `Over ${CONCENTRATION_LIMIT_PCT} % limit`}
        </Chip>
      )}
    </>
  );
}

/** Facts from the holdings row that have no column of their own. */
function RowFacts({ h }: { h: HoldingRow }) {
  const facts: { label: string; value: ReactNode }[] = [
    { label: 'Account', value: h.account_name || '—' },
    { label: 'Asset class', value: `${labelFor(h.asset_class)}${h.symbol ? ` · ${h.symbol}` : ''}` },
    { label: 'Cost basis · average cost', value: <span data-privacy="blur">{fmtEUR(eurFigures(h).cost)}</span> },
    { label: 'Dividends received · gross', value: <span data-privacy="blur">{fmtEUR(h.dividends_eur ?? 0, { cents: true })}</span> },
  ];
  if (h.fx_impact_pct != null) {
    facts.push({
      label: `Est. currency effect, 1 year${h.fx_exposure ? ` (${h.fx_exposure})` : ''}`,
      value: <span className={toneClass(h.fx_impact_pct)}>{fmtPct(h.fx_impact_pct, { sign: true })}</span>,
    });
  }
  return (
    <dl className="grid grid-cols-2 sm:grid-cols-3 gap-x-6 gap-y-3 mb-4">
      {facts.map(f => (
        <div key={f.label} className="min-w-0">
          <dt className="label-caps">{f.label}</dt>
          <dd className="text-[14px] text-ink mt-0.5 break-words">{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export default function HoldingsTable({ holdings, expandedKey, onToggle, detail }: Props) {
  const isDesktop = useIsDesktop();

  if (holdings.length === 0) {
    return (
      <EmptyState
        title="No holdings yet"
        message="Import your broker's transactions to see positions, values and gains here."
        action={{ label: 'Import transactions', href: '/settings?tab=data' }}
      />
    );
  }

  const sorted = [...holdings].sort((a, b) => (b.weight_pct ?? 0) - (a.weight_pct ?? 0));
  const multiAccount = new Set(holdings.map(h => h.account_id)).size > 1;

  const totalValue = sorted.reduce((s, h) => s + eurFigures(h).value, 0);
  const anyPriced = sorted.some(h => eurFigures(h).gain != null);
  const totalGain = sorted.reduce((s, h) => s + (eurFigures(h).gain ?? 0), 0);
  const totalCost = totalValue - totalGain;
  const totalGainPct = anyPriced && totalCost > 0 ? (totalGain / totalCost) * 100 : null;
  const totalWeight = sorted.reduce((s, h) => s + (h.weight_pct ?? 0), 0);
  const hasWeights = sorted.some(h => h.weight_pct != null);
  const unpriced = sorted.filter(h => h.market_value == null).length;

  const nameButton = (h: HoldingRow, open: boolean, detailId: string) => {
    const label = (
      <span className="min-w-0 flex-1">
        <span className="block text-[14px] font-medium text-ink line-clamp-2 break-words" title={h.name}>{h.name}</span>
        <span className="block text-[12px] text-ink-muted truncate">
          {multiAccount && h.account_name ? `${h.account_name} · ` : ''}
          <span className="font-mono">{h.security_isin}</span>
        </span>
      </span>
    );
    if (!onToggle) return <div className="flex items-center gap-2 min-h-[44px]">{label}</div>;
    return (
      <button
        type="button"
        onClick={() => onToggle(h)}
        aria-expanded={open}
        aria-controls={detailId}
        className="flex items-center gap-2 w-full min-h-[44px] text-left rounded-[4px] hover:text-forest"
      >
        <Chevron open={open} />
        {label}
      </button>
    );
  };

  // Average cost in the currency it was paid in; in EUR when purchases mixed currencies.
  const priceLines = (h: HoldingRow) => {
    const priceCurrency = h.price_currency || h.currency || 'EUR';
    const avgCost = h.avg_cost_paid != null && h.avg_cost_currency
      ? fmtMoney(h.avg_cost_paid, h.avg_cost_currency, { cents: true })
      : fmtEUR(h.avg_cost_eur ?? eurFigures(h).cost / (h.quantity || 1), { cents: true });
    return {
      price: h.current_price != null ? fmtMoney(h.current_price, priceCurrency, { cents: true }) : null,
      avgCost,
    };
  };

  const footnote = (
    <p className="mt-3 text-[12px] text-ink-muted">
      Average cost is shown in the currency you paid. Cost basis is the average purchase cost in EUR, each purchase
      converted at the ECB rate of its date; value uses today’s price and rate, so gain and gain % include currency
      movement since purchase. Tax is worked out first in, first out, which can differ after partial sales.
      {unpriced > 0 && ` ${unpriced} ${unpriced === 1 ? 'holding has' : 'holdings have'} no market price and ${unpriced === 1 ? 'is' : 'are'} shown at cost.`}
    </p>
  );

  if (!isDesktop) {
    return (
      <>
        <ul className="flex flex-col gap-3">
          {sorted.map(h => {
            const key = holdingKey(h);
            const open = expandedKey === key;
            const detailId = `holding-detail-${key}`;
            const { value, gain, gainPct } = eurFigures(h);
            const { price, avgCost } = priceLines(h);
            return (
              <li key={key} className={`rounded-apple border border-divider px-4 py-3 ${open ? 'bg-parchment-deep' : ''}`}>
                {nameButton(h, open, detailId)}
                <div className="mt-2 flex items-start justify-between gap-3">
                  <div className="min-w-0 text-[13px] text-ink-body">
                    <p>{fmtQty(h.quantity)} units</p>
                    <p>{price ?? 'No price'}</p>
                    <p className="text-[12px] text-ink-muted">avg cost {avgCost}</p>
                  </div>
                  <div className="text-right shrink-0">
                    <p className="text-[15px] font-medium text-ink" data-privacy="blur">{fmtEUR(value)}</p>
                    {gain != null ? (
                      <p className={`text-[13px] ${toneClass(gain)}`}>
                        <span data-privacy="blur">{fmtEUR(gain, { sign: true })}</span>
                        {gainPct != null && <span className="block text-[12px]">{fmtPct(gainPct, { sign: true })}</span>}
                      </p>
                    ) : (
                      <p className="text-[12px] text-ink-muted">at cost</p>
                    )}
                  </div>
                </div>
                <div className="mt-2 text-[13px]"><WeightCell pct={h.weight_pct} isinPct={concentrationPct(h)} multiAccount={multiAccount} /></div>
                {open && (
                  <div id={detailId} className="mt-3 pt-3 border-t border-divider">
                    <RowFacts h={h} />
                    {detail}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
        <div className="mt-3 flex items-start justify-between gap-3 rounded-apple border border-divider bg-inset px-4 py-3">
          <span className="text-[14px] font-semibold text-ink">Total</span>
          <div className="text-right">
            <p className="text-[15px] font-semibold text-ink" data-privacy="blur">{fmtEUR(totalValue)}</p>
            {anyPriced && (
              <p className={`text-[13px] ${toneClass(totalGain)}`}>
                <span data-privacy="blur">{fmtEUR(totalGain, { sign: true })}</span>
                {totalGainPct != null && <> · {fmtPct(totalGainPct, { sign: true })}</>}
              </p>
            )}
          </div>
        </div>
        {footnote}
      </>
    );
  }

  return (
    <>
      <div role="region" aria-label="Holdings table" tabIndex={0} className="overflow-x-auto rounded-apple border border-divider">
        <table className="apple-table table-fixed min-w-[700px]">
          <colgroup>
            <col />
            <col className="w-[84px]" />
            <col className="w-[132px]" />
            <col className="w-[92px]" />
            <col className="w-[92px]" />
            <col className="w-[132px]" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col" className="!px-2">Security</th>
              <th scope="col" className="num !px-2">Quantity</th>
              <th scope="col" className="num !px-2">Price</th>
              <th scope="col" className="num !px-2">Value</th>
              <th scope="col" className="num !px-2">Gain</th>
              <th scope="col" className="!px-2">Weight</th>
            </tr>
          </thead>
          {sorted.map(h => {
            const key = holdingKey(h);
            const open = expandedKey === key;
            const detailId = `holding-detail-${key}`;
            const { value, gain, gainPct } = eurFigures(h);
            const { price, avgCost } = priceLines(h);
            const bg = open ? 'bg-parchment-deep' : '';
            return (
              <tbody key={key}>
                <tr className={bg}>
                  <td className="!p-2 !border-b !border-divider">{nameButton(h, open, detailId)}</td>
                  <td className="num !p-2 !border-b !border-divider whitespace-nowrap">{fmtQty(h.quantity)}</td>
                  <td className="num !p-2 !border-b !border-divider whitespace-nowrap">
                    {price ?? <span className="text-ink-muted">No price</span>}
                    <span className="block text-[12px] text-ink-muted">avg cost {avgCost}</span>
                  </td>
                  <td className="num !p-2 !border-b !border-divider whitespace-nowrap !text-ink">
                    <span data-privacy="blur">{fmtEUR(value)}</span>
                    {gain == null && <span className="block text-[12px] text-ink-muted">at cost</span>}
                  </td>
                  <td className="num !p-2 !border-b !border-divider whitespace-nowrap">
                    {gain != null ? (
                      <div className={toneClass(gain)}>
                        <span data-privacy="blur">{fmtEUR(gain, { sign: true })}</span>
                        <span className="block text-[12px]">{gainPct != null ? fmtPct(gainPct, { sign: true }) : '—'}</span>
                      </div>
                    ) : <span className="text-ink-muted">—</span>}
                  </td>
                  <td className="!p-2 !border-b !border-divider"><WeightCell pct={h.weight_pct} isinPct={concentrationPct(h)} multiAccount={multiAccount} /></td>
                </tr>
                {open && (
                  <tr id={detailId} className={bg}>
                    <td colSpan={6} className="!px-4 !pt-2 !pb-5 !border-b !border-divider">
                      <RowFacts h={h} />
                      {detail}
                    </td>
                  </tr>
                )}
              </tbody>
            );
          })}
          <tfoot>
            <tr className="bg-inset">
              <td className="!px-2 !py-3 font-semibold !text-ink" colSpan={3}>Total</td>
              <td className="num !px-2 !py-3 font-semibold !text-ink whitespace-nowrap"><span data-privacy="blur">{fmtEUR(totalValue)}</span></td>
              <td className="num !px-2 !py-3 font-semibold whitespace-nowrap">
                {anyPriced ? (
                  <div className={toneClass(totalGain)}>
                    <span data-privacy="blur">{fmtEUR(totalGain, { sign: true })}</span>
                    <span className="block text-[12px] font-normal">{totalGainPct != null ? fmtPct(totalGainPct, { sign: true }) : '—'}</span>
                  </div>
                ) : '—'}
              </td>
              <td className="!px-2 !py-3 !text-ink">{hasWeights ? fmtPct(totalWeight) : '—'}</td>
            </tr>
          </tfoot>
        </table>
      </div>
      {footnote}
    </>
  );
}
