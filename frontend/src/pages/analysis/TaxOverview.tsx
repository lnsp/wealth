import { useEffect, useState, type ReactNode } from 'react';
import { api, type PrivateSaleSummary, type TaxData, type TaxLossHint, type TaxSummary } from '../../api/client';
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

const CHURCH_TAX_KEY = 'church_tax_rate';
const JOINT_KEY = 'fsa_joint';

function readFlag(key: string): boolean {
  try { return localStorage.getItem(key) === '1'; } catch { return false; }
}

function readChurchTax(): number {
  try {
    const v = parseFloat(localStorage.getItem(CHURCH_TAX_KEY) || '0');
    return v === 0.08 || v === 0.09 ? v : 0;
  } catch { return 0; }
}

/**
 * Tax on a euro of capital income: Abgeltungsteuer + 5,5 % Soli + church tax k, with the church
 * tax deductible from the base (§32d Abs. 1 EStG): (1 + 0,055 + k) / (4 + k).
 * k = 0 → 26,375 %, k = 0,08 → 27,819 %, k = 0,09 → 27,995 %.
 */
function capitalIncomeTaxRate(churchTax: number): number {
  const k = Math.min(Math.max(churchTax, 0), 0.09);
  return (1 + 0.055 + k) / (4 + k);
}

/** A year shows the full overview once anything in it is taxable or carries a loss. */
function hasTaxableEvents(s: TaxSummary | undefined, vpRows: number): boolean {
  if (!s) return vpRows > 0;
  return num(s.realized_gains) > 0 || num(s.realized_losses) < 0 || num(s.dividend_income) !== 0 ||
    num(s.interest_income) > 0 || num(s.vorabpauschale_income) > 0 || num(s.loss_carry_in_share) < 0 ||
    num(s.loss_carry_in_general) < 0 || !!s.private_sales || vpRows > 0;
}

export default function TaxOverview({ onOpenTools }: { onOpenTools: () => void }) {
  // Filing settings shared with the Tools tab and the exemption-order section; kept in the browser.
  const [joint, setJoint] = useState(() => readFlag(JOINT_KEY));
  const [churchTax, setChurchTax] = useState(readChurchTax);
  useEffect(() => {
    try { localStorage.setItem(JOINT_KEY, joint ? '1' : '0'); } catch { /* preference only */ }
  }, [joint]);
  const changeChurchTax = (v: number) => {
    setChurchTax(v);
    try { localStorage.setItem(CHURCH_TAX_KEY, String(v)); } catch { /* preference only */ }
  };
  return (
    <>
      <SectionBoundary><Overview onOpenTools={onOpenTools} joint={joint} churchTax={churchTax} onChurchTaxChange={changeChurchTax} /></SectionBoundary>
      <SectionBoundary><ExemptionOrders joint={joint} onJointChange={setJoint} churchTax={churchTax} /></SectionBoundary>
    </>
  );
}

function Overview({ onOpenTools, joint, churchTax, onChurchTaxChange }: {
  onOpenTools: () => void; joint: boolean; churchTax: number; onChurchTaxChange: (v: number) => void;
}) {
  const thisYear = new Date().getFullYear();
  const [year, setYear] = useState(thisYear);
  const tax = useLoad<TaxData>(() => api.getTax(year, { joint, churchTax }), [year, joint, churchTax]);

  // The selector and the export stay available whatever the selected year contains,
  // so an empty year can always be swapped for one with data.
  const years = [...new Set([thisYear, year, ...arr(tax.data?.available_years)])].sort((a, b) => b - a);

  return (
    <Section first title="Tax Overview"
      subtitle="Abgeltungsteuer (flat tax on capital income, 26,375 % with solidarity surcharge, more with church tax) after the Teilfreistellung (partial exemption) for funds, the two loss pots and the saver’s allowance. Crypto and physical gold are shown separately."
      actions={
        <>
          <SelectField label="Tax year" className="w-32" value={year} onChange={e => setYear(parseInt(e.target.value, 10))}>
            {years.map(y => <option key={y} value={y}>{y}</option>)}
          </SelectField>
          <SelectField label="Church tax" className="w-44" value={churchTax} onChange={e => onChurchTaxChange(parseFloat(e.target.value))}>
            <option value={0}>None</option>
            <option value={0.08}>8 % (BY, BW)</option>
            <option value={0.09}>9 % (other states)</option>
          </SelectField>
          <a href={`/api/analysis/export-tax?year=${year}${joint ? '&joint=1' : ''}${churchTax > 0 ? `&church_tax=${churchTax}` : ''}`} download className="apple-btn-secondary min-h-[44px] self-end">Export CSV</a>
        </>
      }>
      <Loaded state={tax} lines={6}>
        {data => {
          const s = data?.summary;
          const vp = arr(data?.vorabpauschale);
          if (!s || !hasTaxableEvents(s, vp.length)) {
            return (
              <EmptyState
                title={`No taxable events in ${year}`}
                message="No realised gains, losses, dividends, interest or Vorabpauschale are recorded for this year. Choose another year above, or preview the tax on a sale you are considering."
                action={{ label: 'Open the sell simulator', onClick: onOpenTools }}
              />
            );
          }
          const used = num(s.freistellung_used);
          const remaining = num(s.freistellung_remaining);
          const allowance = num(s.allowance) || used + remaining;
          const hints = arr(s.tax_loss_hints);
          const vpIncome = num(s.vorabpauschale_income);
          const tfs = num(s.teilfreistellung_amt);
          const rate = num(s.tax_rate);
          return (
            <>
              <MetricGrid>
                <MetricCard label="Realised gains" value={fmtEUR(s.realized_gains)} tone={num(s.realized_gains) > 0 ? 'positive' : 'neutral'} />
                <MetricCard label="Realised losses" value={fmtEUR(s.realized_losses)} tone={num(s.realized_losses) < 0 ? 'negative' : 'neutral'} />
                {num(s.dividend_income) !== 0 && <MetricCard label="Dividend income" value={fmtEUR(s.dividend_income)} />}
                {num(s.interest_income) > 0 && <MetricCard label="Interest income" value={fmtEUR(s.interest_income)} />}
                {vpIncome > 0 && (
                  <MetricCard label={`Vorabpauschale ${num(s.vorabpauschale_year)}`} value={fmtEUR(vpIncome)}
                    sublabel={`Deemed received in January ${year}`} />
                )}
                <MetricCard label="Net taxable" value={fmtEUR(s.taxable_gain)}
                  sublabel={tfs !== 0
                    ? `After ${fmtEUR(Math.abs(tfs))} partial exemption${tfs < 0 ? ' on losses' : ''} and the loss pots`
                    : 'After the loss pots, before the allowance'} />
                <MetricCard label="Estimated tax" value={fmtEUR(s.estimated_tax)}
                  sublabel={`Effective rate ${fmtPct(s.effective_rate, { digits: 2 })}${rate > 0 ? ` · ${fmtPct(rate * 100, { digits: 3 })} on taxable income` : ''}`} />
              </MetricGrid>

              <LossPotLines s={s} year={year} />

              <SubHeadingRow>Sparer-Pauschbetrag (saver’s allowance){s.joint ? ', joint filing' : ''}</SubHeadingRow>
              <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 text-[13px]">
                <span className="text-ink"><Money value={used} /> of <Money value={allowance} /> used</span>
                <span className="text-ink-muted">
                  {remaining > 0 ? <><Money value={remaining} /> still tax-free in {year}</> : 'Fully used. Further gains are taxed.'}
                </span>
              </div>
              <ProgressBar className="mt-2" value={used} max={allowance} label="Saver’s allowance used" />

              {vp.length > 0 && (
                <VorabpauschaleTable rows={vp} year={num(data.vorabpauschale_year) || year} estimate={!!data.vorabpauschale_estimate} />
              )}

              {s.private_sales && <PrivateSales ps={s.private_sales} />}

              {hints.length > 0 && <HarvestHints hints={hints} />}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/** Share losses (Aktienverlusttopf) only offset share gains; other losses offset any capital income. */
function LossPotLines({ s, year }: { s: TaxSummary; year: number }) {
  const shareLoss = num(s.share_losses), generalLoss = num(s.general_losses);
  const inShare = num(s.loss_carry_in_share), inGeneral = num(s.loss_carry_in_general);
  const outShare = num(s.loss_carry_out_share), outGeneral = num(s.loss_carry_out_general);
  if (shareLoss === 0 && generalLoss === 0 && inShare === 0 && inGeneral === 0 && outShare === 0 && outGeneral === 0) return null;
  const rows: { label: string; value: ReactNode }[] = [
    { label: 'Share gains', value: <Money value={s.share_gains} /> },
    { label: 'Share losses', value: <Money value={shareLoss} tone={shareLoss < 0 ? 'negative' : undefined} /> },
    { label: 'Other income, after partial exemption', value: <Money value={s.general_gains} /> },
    { label: 'Other losses, after partial exemption', value: <Money value={generalLoss} tone={generalLoss < 0 ? 'negative' : undefined} /> },
  ];
  if (inShare < 0 || inGeneral < 0) {
    rows.push({ label: `Losses carried in from ${year - 1}`, value: <><Money value={inShare} /> shares · <Money value={inGeneral} /> other</> });
  }
  if (outShare < 0 || outGeneral < 0) {
    rows.push({ label: `Losses carried into ${year + 1}`, value: <><Money value={outShare} /> shares · <Money value={outGeneral} /> other</> });
  }
  return (
    <>
      <SubHeadingRow>Loss pots (Verlusttöpfe)</SubHeadingRow>
      <div className="max-w-xl"><KeyValues rows={rows} /></div>
      <p className="mt-2 text-[12px] text-ink-muted">Losses on shares only offset gains on shares. Other losses, including fund losses, offset any capital income. Unused losses carry forward.</p>
    </>
  );
}

function VorabpauschaleTable({ rows, year, estimate }: { rows: NonNullable<TaxData['vorabpauschale']>; year: number; estimate: boolean }) {
  return (
    <>
      <SubHeadingRow>Vorabpauschale {year}, taxed in January {year + 1}</SubHeadingRow>
      {estimate && <p className="mb-2 text-[13px] text-ink-muted">Estimate: today’s price stands in for the year-end price.</p>}
      <ul className="md:hidden flex flex-col gap-3">
        {rows.map(v => (
          <li key={v.isin} className="rounded-apple border border-divider px-4 py-3">
            <p className="mb-2 text-[14px] font-medium text-ink">{v.name}</p>
            <KeyValues rows={[
              { label: 'Year-end units at the 1 January price', value: <Money value={v.jan1_value} /> },
              { label: 'Basiszins (base rate)', value: fmtPct(v.basiszins, { digits: 2 }) },
              { label: 'Basisertrag (base return)', value: <Money value={v.basisertrag} /> },
              { label: 'Vorabpauschale', value: <Money value={v.vorabpauschale} /> },
              { label: 'Tax', value: <Money value={v.tax_on_vp} cents /> },
            ]} />
          </li>
        ))}
      </ul>
      <TableRegion label={`Vorabpauschale ${year} by fund`} className="hidden md:block">
        <table className="apple-table">
          <thead>
            <tr>
              <th scope="col">Fund</th>
              <th scope="col" className="num">Value at 1 Jan price</th>
              <th scope="col" className="num">Basiszins</th>
              <th scope="col" className="num">Basisertrag</th>
              <th scope="col" className="num">Vorabpauschale</th>
              <th scope="col" className="num">Tax</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(v => (
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
      <p className="mt-2 text-[12px] text-ink-muted">
        Units held at year end, valued at the 1 January price. The Basisertrag (70 % of the Basiszins, the base rate set each year) is capped at the
        year’s price gain plus distributions, less distributions, and reduced by 1/12 for each full month before a purchase made during the year.
        It is taxed after the partial exemption and deducted from the gain when you sell.
      </p>
    </>
  );
}

function PrivateSales({ ps }: { ps: PrivateSaleSummary }) {
  const net = num(ps.net_taxable);
  let verdict: string;
  if (net < 0) verdict = 'A net loss on short-term sales can be offset against private-sale gains of the previous or following years in your tax return.';
  else if (ps.below_freigrenze) verdict = `Below the ${fmtEUR(ps.freigrenze)} Freigrenze (exemption limit), so these gains are tax-free.`;
  else verdict = `At or above the ${fmtEUR(ps.freigrenze)} Freigrenze, so the whole ${fmtEUR(net)} is taxable. The limit is not a deduction.`;
  return (
    <>
      <SubHeadingRow>Private sales (§23 EStG): crypto and physical gold</SubHeadingRow>
      <div className="max-w-xl">
        <KeyValues rows={[
          { label: 'Gains, held one year or less', value: <Money value={ps.taxable_gains} /> },
          { label: 'Losses, held one year or less', value: <Money value={ps.taxable_losses} tone={num(ps.taxable_losses) < 0 ? 'negative' : undefined} /> },
          { label: 'Taxable at your income-tax rate', value: <Money value={ps.taxable_amount} className="font-medium" /> },
          { label: 'Held more than one year, tax-free', value: <Money value={ps.tax_free_pl} sign /> },
        ]} />
      </div>
      <p className="mt-2 text-[13px] text-ink-body">{verdict}</p>
      <p className="mt-1 text-[12px] text-ink-muted">
        Taxed at your personal income-tax rate, not the flat tax, and declared in Anlage SO. These sales are not part of the figures above.
        {num(ps.other_income) > 0 && <> Staking or lending income of <Money value={ps.other_income} /> is taxed separately (§22 Nr. 3 EStG, tax-free below 256 € a year).</>}
      </p>
    </>
  );
}

function HarvestHints({ hints }: { hints: TaxLossHint[] }) {
  return (
    <>
      <SubHeadingRow>Tax-Loss Harvesting Opportunities</SubHeadingRow>
      <ul className="divide-y divide-divider border-y border-divider">
        {hints.map(h => (
          <li key={`${h.account_id ?? ''}-${h.isin}`} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-2 text-[13px]">
            <span className="min-w-0">
              <span className="block text-ink">{h.name}{h.account ? <span className="text-ink-muted"> · {h.account}</span> : null}</span>
              <span className="block text-ink-muted">Unrealised loss <Money value={h.unrealized_pl} tone="negative" />, selling the whole position in this depot (oldest lots first)</span>
            </span>
            {num(h.potential_saving) > 0
              ? <span className="text-ink-body">Selling now would cut this year’s tax by about <Money value={h.potential_saving} className="font-medium text-ink" /></span>
              : <span className="text-ink-muted">
                  No saving this year: {h.pot === 'share' ? 'no share gains left to offset' : 'no taxable income left to offset'}. The loss would carry forward.
                </span>}
          </li>
        ))}
      </ul>
    </>
  );
}

/* ───────────── Freistellungsauftrag by account ───────────── */

function ExemptionOrders({ joint, onJointChange, churchTax }: { joint: boolean; onJointChange: (v: boolean) => void; churchTax: number }) {
  const fsa = useLoad(() => getJson<FsaStatus>(`/api/analysis/fsa-status${joint ? '?joint=1' : ''}`), [joint]);

  return (
    <Section title="Freistellungsauftrag by Bank"
      subtitle="The Freistellungsauftrag (tax exemption order) tells each German bank how much of your saver’s allowance it may apply. Each bank first nets your gains and losses, so this shows the income left at each bank this year."
      actions={
        <label className="flex min-h-[44px] cursor-pointer items-center gap-2 text-[14px] text-ink">
          <input type="checkbox" checked={joint} onChange={e => onJointChange(e.target.checked)} className="h-5 w-5 accent-forest" />
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
                    <Money value={data.remaining} /> unused, worth up to <Money value={num(data.remaining) * capitalIncomeTaxRate(churchTax)} /> in tax
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
