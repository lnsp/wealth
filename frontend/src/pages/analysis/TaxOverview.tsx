import { useEffect, useState } from 'react';
import { api, type TaxData } from '../../api/client';
import { EmptyState, MetricCard, MetricGrid, ProgressBar, Section, SectionBoundary, SelectField } from '../../components/ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { Callout, KeyValues, Loaded, Money, Statement, SubHeadingRow, TableRegion, arr, getJson, num, useLoad } from './shared';

interface FsaStatus {
  year: number;
  /** One row per bank: a Freistellungsauftrag is filed per bank, which nets gains and losses first. */
  accounts?: { account_name: string; dividends: number; interest: number; realized_gains: number; total_income: number; accepts_fsa?: boolean; vorabpauschale?: number }[] | null;
  total_income: number;
  /** Income at foreign brokers, which use the allowance only through Anlage KAP. */
  foreign_income?: number;
  allowance: number;
  joint: boolean;
  used: number;
  remaining: number;
  utilization_pct: number;
  recommendation?: { account: string; projected_income: number; recommended_fsa: number }[] | null;
}

/** Sparer-Pauschbetrag for a single filer, the basis of the tax summary. */
const SAVERS_ALLOWANCE = 1000;
/** Abgeltungsteuer plus solidarity surcharge. */
const FLAT_TAX_RATE = 0.26375;

function readFlag(key: string): boolean {
  try { return localStorage.getItem(key) === '1'; } catch { return false; }
}

export default function TaxOverview({ onOpenTools }: { onOpenTools: () => void }) {
  return (
    <>
      <SectionBoundary><Overview onOpenTools={onOpenTools} /></SectionBoundary>
      <SectionBoundary><ExemptionOrders /></SectionBoundary>
    </>
  );
}

function Overview({ onOpenTools }: { onOpenTools: () => void }) {
  const thisYear = new Date().getFullYear();
  const [year, setYear] = useState(thisYear);
  const tax = useLoad<TaxData>(() => api.getTax(year), [year]);

  // The selector and the export stay available whatever the selected year contains,
  // so an empty year can always be swapped for one with data.
  const years = [...new Set([thisYear, year, ...arr(tax.data?.available_years)])].sort((a, b) => b - a);

  return (
    <Section first title="Tax Overview"
      subtitle="Abgeltungsteuer (flat tax on capital income, 26,375 % with solidarity surcharge), after the Teilfreistellung (partial exemption) for equity funds."
      actions={
        <>
          <SelectField label="Tax year" className="w-32" value={year} onChange={e => setYear(parseInt(e.target.value, 10))}>
            {years.map(y => <option key={y} value={y}>{y}</option>)}
          </SelectField>
          <a href={`/api/analysis/export-tax?year=${year}`} download className="apple-btn-secondary min-h-[44px] self-end">Export CSV</a>
        </>
      }>
      <Loaded state={tax} lines={6}>
        {data => {
          const s = data?.summary;
          const hasEvents = !!s && (num(s.realized_gains) > 0 || num(s.realized_losses) < 0 || num(s.dividend_income) > 0);
          if (!s || !hasEvents) {
            return (
              <EmptyState
                title={`No taxable events in ${year}`}
                message="No realised gains, losses or dividends are recorded for this year. Choose another year above, or preview the tax on a sale you are considering."
                action={{ label: 'Open the sell simulator', onClick: onOpenTools }}
              />
            );
          }
          const used = num(s.freistellung_used);
          const remaining = num(s.freistellung_remaining);
          const vp = arr(data.vorabpauschale);
          const hints = arr(s.tax_loss_hints);
          return (
            <>
              <MetricGrid>
                <MetricCard label="Realised gains" value={fmtEUR(s.realized_gains)} tone={num(s.realized_gains) > 0 ? 'positive' : 'neutral'} />
                <MetricCard label="Realised losses" value={fmtEUR(s.realized_losses)} tone={num(s.realized_losses) < 0 ? 'negative' : 'neutral'} />
                {num(s.dividend_income) > 0 && <MetricCard label="Dividend income" value={fmtEUR(s.dividend_income)} />}
                <MetricCard label="Net taxable" value={fmtEUR(s.taxable_gain)}
                  sublabel={num(s.teilfreistellung_amt) > 0 ? `After ${fmtEUR(s.teilfreistellung_amt)} partial exemption` : undefined} />
                <MetricCard label="Estimated tax" value={fmtEUR(s.estimated_tax)} sublabel={`Effective rate ${fmtPct(s.effective_rate, { digits: 2 })}`} />
              </MetricGrid>

              <SubHeadingRow>Sparer-Pauschbetrag (saver’s allowance)</SubHeadingRow>
              <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 text-[13px]">
                <span className="text-ink"><Money value={used} /> of <Money value={SAVERS_ALLOWANCE} /> used</span>
                <span className="text-ink-muted">
                  {remaining > 0 ? <><Money value={remaining} /> still tax-free this year</> : 'Fully used. Further gains are taxed.'}
                </span>
              </div>
              <ProgressBar className="mt-2" value={used} max={SAVERS_ALLOWANCE} label="Saver’s allowance used" />

              {vp.length > 0 && (
                <>
                  <SubHeadingRow>Vorabpauschale (advance flat-rate tax on accumulating funds)</SubHeadingRow>
                  <ul className="md:hidden flex flex-col gap-3">
                    {vp.map(v => (
                      <li key={v.isin} className="rounded-apple border border-divider px-4 py-3">
                        <p className="mb-2 text-[14px] font-medium text-ink">{v.name}</p>
                        <KeyValues rows={[
                          { label: 'Value on 1 January', value: <Money value={v.jan1_value} /> },
                          { label: 'Basiszins (base rate)', value: fmtPct(v.basiszins, { digits: 2 }) },
                          { label: 'Basisertrag (base return)', value: <Money value={v.basisertrag} /> },
                          { label: 'Vorabpauschale', value: <Money value={v.vorabpauschale} /> },
                          { label: 'Tax', value: <Money value={v.tax_on_vp} cents /> },
                        ]} />
                      </li>
                    ))}
                  </ul>
                  <TableRegion label="Vorabpauschale by fund" className="hidden md:block">
                    <table className="apple-table">
                      <thead>
                        <tr>
                          <th scope="col">Fund</th>
                          <th scope="col" className="num">Value on 1 Jan</th>
                          <th scope="col" className="num">Basiszins</th>
                          <th scope="col" className="num">Basisertrag</th>
                          <th scope="col" className="num">Vorabpauschale</th>
                          <th scope="col" className="num">Tax</th>
                        </tr>
                      </thead>
                      <tbody>
                        {vp.map(v => (
                          <tr key={v.isin}>
                            <td className="font-medium text-ink">{v.name}</td>
                            <td className="num"><Money value={v.jan1_value} /></td>
                            <td className="num">{fmtPct(v.basiszins, { digits: 2 })}</td>
                            <td className="num"><Money value={v.basisertrag} /></td>
                            <td className="num"><Money value={v.vorabpauschale} /></td>
                            <td className="num text-ink"><Money value={v.tax_on_vp} cents /></td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TableRegion>
                  <p className="mt-2 text-[12px] text-ink-muted">Basiszins is the base interest rate set each year; Basisertrag is the notional return it implies.</p>
                </>
              )}

              {hints.length > 0 && (
                <>
                  <SubHeadingRow>Tax-Loss Harvesting Opportunities</SubHeadingRow>
                  <ul className="divide-y divide-divider border-y border-divider">
                    {hints.map(h => (
                      <li key={h.isin} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-2 text-[13px]">
                        <span className="min-w-0">
                          <span className="block text-ink">{h.name}</span>
                          <span className="block text-ink-muted">Unrealised loss <Money value={h.unrealized_pl} tone="negative" /></span>
                        </span>
                        <span className="text-ink-body">Selling could save about <Money value={h.potential_saving} className="font-medium text-ink" /> in tax</span>
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Freistellungsauftrag by account ───────────── */

function ExemptionOrders() {
  const [joint, setJoint] = useState(() => readFlag('fsa_joint'));
  useEffect(() => {
    try { localStorage.setItem('fsa_joint', joint ? '1' : '0'); } catch { /* preference only */ }
  }, [joint]);
  const fsa = useLoad(() => getJson<FsaStatus>(`/api/analysis/fsa-status${joint ? '?joint=1' : ''}`), [joint]);

  return (
    <Section title="Freistellungsauftrag by Bank"
      subtitle="The Freistellungsauftrag (tax exemption order) tells each German bank how much of your saver’s allowance it may apply. Each bank first nets your gains and losses, so this shows the income left at each bank this year."
      actions={
        <label className="flex min-h-[44px] cursor-pointer items-center gap-2 text-[14px] text-ink">
          <input type="checkbox" checked={joint} onChange={e => setJoint(e.target.checked)} className="h-5 w-5 accent-forest" />
          Joint filing (allowance doubles)
        </label>
      }>
      <Loaded state={fsa} lines={4}>
        {data => {
          const accounts = arr(data?.accounts);
          const recs = arr(data?.recommendation);
          if (accounts.length === 0) return <Statement>No account has earned taxable capital income this year, so none of the allowance has been used.</Statement>;
          return (
            <>
              <ul className="divide-y divide-divider border-y border-divider">
                {accounts.map(a => {
                  const parts: string[] = [];
                  if (num(a.realized_gains) > 0) parts.push(`gains ${fmtEUR(a.realized_gains)}`);
                  if (num(a.realized_gains) < 0) parts.push(`net losses ${fmtEUR(-num(a.realized_gains))}`);
                  if (num(a.dividends) > 0) parts.push(`dividends ${fmtEUR(a.dividends)}`);
                  if (num(a.interest) > 0) parts.push(`interest ${fmtEUR(a.interest)}`);
                  if (num(a.vorabpauschale) > 0) parts.push(`Vorabpauschale ${fmtEUR(a.vorabpauschale)}`);
                  return (
                    <li key={a.account_name} className="flex items-center justify-between gap-4 py-2 text-[13px]">
                      <span className="min-w-0">
                        <span className="block font-medium text-ink">{a.account_name}</span>
                        {parts.length > 0 && <span className="block text-ink-muted first-letter:uppercase" data-privacy="blur">{parts.join(' · ')}</span>}
                        {a.accepts_fsa === false && <span className="block text-ink-muted">Foreign broker: takes no Freistellungsauftrag; the allowance applies through Anlage KAP.</span>}
                      </span>
                      <Money value={a.total_income} className="shrink-0 text-[14px] text-ink" />
                    </li>
                  );
                })}
              </ul>

              <div className="mt-4 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 text-[13px]">
                <span className="text-ink"><Money value={data.used} /> of <Money value={data.allowance} /> used ({fmtPct(data.utilization_pct, { digits: 0 })})</span>
                {num(data.remaining) > 0 && (
                  <span className="text-ink-muted">
                    <Money value={data.remaining} /> unused, worth up to <Money value={num(data.remaining) * FLAT_TAX_RATE} /> in tax
                  </span>
                )}
              </div>
              <ProgressBar className="mt-2" value={num(data.used)} max={num(data.allowance)} label="Allowance used across banks" />
              {num(data.foreign_income) > 0 && (
                <p className="mt-2 text-[12px] text-ink-muted">
                  <Money value={data.foreign_income} /> of the income is at foreign brokers, which withhold no German tax; it uses the allowance when you file Anlage KAP.
                </p>
              )}

              {recs.length > 1 && (
                <div className="mt-5">
                  <Callout tone="info" label="Suggestion" title={`Split of the exemption order for ${num(data.year) + 1} across your German banks`}>
                    <ul className="mt-1 flex flex-col gap-1">
                      {recs.map(r => (
                        <li key={r.account} className="flex flex-wrap justify-between gap-x-4">
                          <span>{r.account}</span>
                          <span><Money value={r.recommended_fsa} className="font-medium text-ink" /> (projected income <Money value={r.projected_income} />)</span>
                        </li>
                      ))}
                    </ul>
                  </Callout>
                </div>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}
