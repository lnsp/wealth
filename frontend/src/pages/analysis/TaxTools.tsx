import { useState } from 'react';
import { api, type TaxLot } from '../../api/client';
import { EmptyState, ErrorState, MetricCard, MetricGrid, NumberField, Section, SectionBoundary, SelectField } from '../../components/ui';
import { fmtDate, fmtEUR, fmtPct, fmtQty } from '../../utils/format';
import { Chevron, KeyValues, Loaded, Money, Statement, SubHeadingRow, TableRegion, arr, getJson, num, plural, toneOf, useLoad } from './shared';
import type { LoadState } from './shared';

interface LossPotYear {
  year: number;
  equity_losses: number; equity_gains: number;
  general_losses: number; general_gains: number;
  carry_forward_equity: number; carry_forward_general: number;
}

type SellResult = Awaited<ReturnType<typeof api.simulateSell>>;
type LotsData = Awaited<ReturnType<typeof api.getTaxLots>>;

const CHURCH_TAX_KEY = 'church_tax_rate';

function readNumber(key: string): number {
  try { return parseFloat(localStorage.getItem(key) || '0') || 0; } catch { return 0; }
}

function readFlag(key: string): boolean {
  try { return localStorage.getItem(key) === '1'; } catch { return false; }
}

export default function TaxTools() {
  // Church tax is shared with the Overview tab; joint filing is set there (Freistellungsauftrag section).
  const [churchTax, setChurchTax] = useState(() => readNumber(CHURCH_TAX_KEY));
  const [joint] = useState(() => readFlag('fsa_joint'));
  const lots = useLoad(() => api.getTaxLots({ churchTax, joint }), [churchTax, joint]);
  const changeChurchTax = (v: number) => {
    setChurchTax(v);
    try { localStorage.setItem(CHURCH_TAX_KEY, String(v)); } catch { /* preference only */ }
  };
  return (
    <>
      <SectionBoundary><LossPots /></SectionBoundary>
      <SectionBoundary><LotInventory lots={lots} /></SectionBoundary>
      <SectionBoundary><SellSimulator lots={lots} churchTax={churchTax} onChurchTaxChange={changeChurchTax} joint={joint} /></SectionBoundary>
    </>
  );
}

const NoLots = () => (
  <EmptyState
    title="No open tax lots"
    message="A tax lot is one purchase of a security you still hold. Lots appear once buy transactions have been imported."
    action={{ label: 'Import transactions', href: '/transactions' }}
  />
);

/* ───────────── Loss pots ───────────── */

function LossPots() {
  const pots = useLoad(() => getJson<{ years?: LossPotYear[] | null }>('/api/analysis/loss-pots'));
  // Signed amounts carry the meaning in text: "+" for gains, "−" for losses, "None" for zero.
  const gain = (v: number) => (num(v) > 0 ? <Money value={v} sign tone="positive" /> : <span className="text-ink-muted">None</span>);
  const loss = (v: number) => (num(v) < 0 ? <Money value={v} tone="negative" /> : <span className="text-ink-muted">None</span>);
  return (
    <Section first title="Loss Offset Pots (Verlusttöpfe)"
      subtitle="German tax law keeps two loss pots. Losses on shares (Aktienverlusttopf) can only offset gains on shares; all other losses, including fund and ETF losses after the partial exemption, offset any capital income. Unused losses carry forward.">
      <Loaded state={pots} lines={4}>
        {data => {
          const years = arr(data?.years);
          if (years.length === 0) return <Statement>No realised gains or losses yet, so both loss pots are empty.</Statement>;
          return (
            <>
              <ul className="md:hidden flex flex-col gap-3">
                {years.map(y => (
                  <li key={y.year} className="rounded-apple border border-divider px-4 py-3">
                    <p className="mb-2 text-[14px] font-semibold text-ink">{y.year}</p>
                    <KeyValues rows={[
                      { label: 'Share gains', value: gain(y.equity_gains) },
                      { label: 'Share losses', value: loss(y.equity_losses) },
                      { label: 'Share losses carried forward', value: loss(y.carry_forward_equity) },
                      { label: 'Other income', value: gain(y.general_gains) },
                      { label: 'Other losses', value: loss(y.general_losses) },
                      { label: 'Other losses carried forward', value: loss(y.carry_forward_general) },
                    ]} />
                  </li>
                ))}
              </ul>
              <TableRegion label="Loss offset pots by year" className="hidden md:block">
                <table className="apple-table">
                  <thead>
                    <tr>
                      <th scope="col">Year</th>
                      <th scope="col" className="num">Share gains</th>
                      <th scope="col" className="num">Share losses</th>
                      <th scope="col" className="num">Carried forward</th>
                      <th scope="col" className="num">Other income</th>
                      <th scope="col" className="num">Other losses</th>
                      <th scope="col" className="num">Carried forward</th>
                    </tr>
                  </thead>
                  <tbody>
                    {years.map(y => (
                      <tr key={y.year}>
                        <td className="font-medium text-ink">{y.year}</td>
                        <td className="num">{gain(y.equity_gains)}</td>
                        <td className="num">{loss(y.equity_losses)}</td>
                        <td className="num">{loss(y.carry_forward_equity)}</td>
                        <td className="num">{gain(y.general_gains)}</td>
                        <td className="num">{loss(y.general_losses)}</td>
                        <td className="num">{loss(y.carry_forward_general)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableRegion>
              <p className="mt-2 text-[12px] text-ink-muted">Other income is dividends, interest, the Vorabpauschale and gains on funds, bonds and other non-share securities, after the partial exemption. Crypto and physical gold are private sales and are not in these pots.</p>
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── FIFO tax lot inventory ───────────── */

interface LotGroup { isin: string; name: string; tfs: number; privateSale: boolean; lots: TaxLot[]; value: number; pl: number; tax: number }

/** Partial-exemption rate of a lot (0,3 for equity funds), falling back to the equity-fund flag. */
function lotTFS(lot: TaxLot): number {
  return lot.tfs_rate ?? (lot.is_equity_fund ? 0.3 : 0);
}

/**
 * Groups lots by security. The group's tax is the tax of selling the whole position: per depot,
 * the FIFO tax of its newest lot already includes every older lot (gains and losses netted).
 */
function groupLots(lots: TaxLot[]): LotGroup[] {
  const map = new Map<string, LotGroup>();
  const lastFifo = new Map<string, Map<string, number>>();
  for (const lot of lots) {
    let g = map.get(lot.isin);
    if (!g) {
      g = { isin: lot.isin, name: lot.name, tfs: lotTFS(lot), privateSale: !!lot.private_sale, lots: [], value: 0, pl: 0, tax: 0 };
      map.set(lot.isin, g);
      lastFifo.set(lot.isin, new Map());
    }
    g.lots.push(lot);
    g.value += num(lot.current_value);
    g.pl += num(lot.unrealized_pl);
    lastFifo.get(lot.isin)!.set(lot.account_id ?? '', num(lot.fifo_tax_if_sold ?? lot.tax_if_sold));
  }
  for (const [isin, byDepot] of lastFifo) {
    map.get(isin)!.tax = [...byDepot.values()].reduce((s, v) => s + v, 0);
  }
  return [...map.values()].sort((a, b) => b.value - a.value);
}

/** Tax cell for a lot: FIFO tax for securities, holding-period status for §23 assets (crypto, gold). */
function LotTax({ lot }: { lot: TaxLot }) {
  if (lot.private_sale) {
    const free = !!lot.tax_free_from && new Date(lot.tax_free_from) <= new Date();
    return <span className="text-ink-muted">{free ? 'Tax-free (held over a year)' : `Income tax until ${fmtDate(lot.tax_free_from)}`}</span>;
  }
  const t = num(lot.fifo_tax_if_sold ?? lot.tax_if_sold);
  return t > 0 ? <Money value={t} /> : <>None</>;
}

function LotInventory({ lots }: { lots: LoadState<LotsData> }) {
  const [open, setOpen] = useState<Record<string, boolean>>({});
  return (
    <Section title="FIFO Tax Lot Inventory"
      subtitle="Every purchase you still hold, with its cost basis and the tax due if it were sold today. Sales use the oldest lot in a depot first (FIFO, first in, first out).">
      <Loaded state={lots} lines={5}>
        {data => {
          const list = arr(data?.lots);
          if (list.length === 0) return <NoLots />;
          const groups = groupLots(list);
          const totalPL = groups.reduce((s, g) => s + g.pl, 0);
          const sa = data?.sell_all;
          const thisYear = num(sa?.tax_this_year);
          const privatePL = num(sa?.private_sale_taxable_pl);
          return (
            <>
              <MetricGrid>
                <MetricCard label="Unrealised gain or loss" value={fmtEUR(totalPL, { sign: true })} tone={toneOf(totalPL)} />
                <MetricCard label="Tax if everything were sold" value={fmtEUR(Math.max(0, thisYear))}
                  sublabel={thisYear < 0
                    ? `Would cut this year’s tax by ${fmtEUR(-thisYear)}`
                    : `This year, after allowance and loss pots · ${fmtEUR(sa?.gross_tax)} before them`} />
                <MetricCard money={false} label="Open lots" value={`${list.length}`} sublabel={`In ${plural(groups.length, 'security', 'securities')}`} />
              </MetricGrid>
              {privatePL !== 0 && (
                <p className="mt-3 text-[13px] text-ink-muted">
                  Crypto and gold held a year or less would add <Money value={privatePL} sign /> taxed at your personal income-tax rate (§23 EStG), not included above.
                </p>
              )}

              <ul className="mt-5 divide-y divide-divider border-y border-divider">
                {groups.map(g => {
                  const isOpen = !!open[g.isin];
                  const panelId = `lots-${g.isin}`;
                  const exemption = g.tfs > 0 ? ` · ${fmtPct(g.tfs * 100, { digits: 0 })} partial exemption` : '';
                  return (
                    <li key={g.isin}>
                      <button type="button" aria-expanded={isOpen} aria-controls={panelId}
                        onClick={() => setOpen(prev => ({ ...prev, [g.isin]: !isOpen }))}
                        className="flex w-full min-h-[56px] items-center gap-3 py-2 text-left hover:bg-inset transition-colors">
                        <Chevron open={isOpen} />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-[14px] font-medium text-ink" title={g.name}>{g.name}</span>
                          <span className="block text-[12px] text-ink-muted">
                            <span className="font-mono">{g.isin}</span> · {plural(g.lots.length, 'lot', 'lots')}{exemption}{g.privateSale ? ' · private sale (§23)' : ''}
                          </span>
                        </span>
                        <span className="shrink-0 text-right text-[13px]">
                          <Money value={g.pl} sign tone={toneOf(g.pl)} className="block font-medium" />
                          {g.tax > 0 && <span className="block text-[12px] text-ink-muted">Tax <Money value={g.tax} /></span>}
                        </span>
                      </button>
                      {isOpen && (
                        <div id={panelId} className="pb-3 pl-7">
                          <ul className="md:hidden flex flex-col gap-3">
                            {g.lots.map((lot, i) => (
                              <li key={i} className="rounded-apple border border-divider px-4 py-3">
                                <p className="mb-2 text-[13px] font-medium text-ink">Bought {fmtDate(lot.buy_date)}</p>
                                <KeyValues rows={[
                                  { label: 'Quantity', value: `${fmtQty(lot.quantity)} ${lot.quantity === 1 ? 'share' : 'shares'}` },
                                  { label: 'Cost basis', value: <Money value={lot.cost_basis} /> },
                                  { label: 'Value today', value: <Money value={lot.current_value} /> },
                                  { label: 'Gain or loss', value: <Money value={lot.unrealized_pl} sign tone={toneOf(num(lot.unrealized_pl))} /> },
                                  { label: 'Tax, selling up to this lot', value: <LotTax lot={lot} /> },
                                ]} />
                              </li>
                            ))}
                          </ul>
                          <TableRegion label={`Tax lots of ${g.name}`} className="hidden md:block">
                            <table className="apple-table">
                              <thead>
                                <tr>
                                  <th scope="col">Bought</th>
                                  <th scope="col" className="num">Shares</th>
                                  <th scope="col" className="num">Cost basis</th>
                                  <th scope="col" className="num">Value today</th>
                                  <th scope="col" className="num">Gain or loss</th>
                                  <th scope="col" className="num">Tax, selling up to this lot</th>
                                </tr>
                              </thead>
                              <tbody>
                                {g.lots.map((lot, i) => (
                                  <tr key={i}>
                                    <td>{fmtDate(lot.buy_date)}</td>
                                    <td className="num">{fmtQty(lot.quantity)}</td>
                                    <td className="num"><Money value={lot.cost_basis} /></td>
                                    <td className="num"><Money value={lot.current_value} /></td>
                                    <td className="num"><Money value={lot.unrealized_pl} sign tone={toneOf(num(lot.unrealized_pl))} /></td>
                                    <td className="num"><LotTax lot={lot} /></td>
                                  </tr>
                                ))}
                              </tbody>
                            </table>
                          </TableRegion>
                          <p className="mt-2 text-[12px] text-ink-muted">
                            A sale always uses the oldest lots in a depot first, so selling a newer lot means selling every older one too.
                            The tax shown is for selling this lot together with all older ones in the same depot, after the partial exemption and
                            before your allowance and loss pots.
                          </p>
                        </div>
                      )}
                    </li>
                  );
                })}
              </ul>
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── What-if sell simulator ───────────── */

function SellSimulator({ lots, churchTax, onChurchTaxChange, joint }: {
  lots: LoadState<LotsData>; churchTax: number; onChurchTaxChange: (v: number) => void; joint: boolean;
}) {
  const [amounts, setAmounts] = useState<Record<string, number>>({});
  const [result, setResult] = useState<SellResult | null>(null);
  const [running, setRunning] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);

  const simulate = (requests: { isin: string; amount_eur: number }[]) => {
    if (requests.length === 0) return;
    setRunning(true);
    setFailed(null);
    api.simulateSell(requests, churchTax, joint)
      .then(setResult)
      .catch((e: unknown) => { setResult(null); setFailed(e instanceof Error ? e.message : String(e)); })
      .finally(() => setRunning(false));
  };

  return (
    <Section title="What-If Sell Simulator"
      subtitle="Enter an amount to sell per security and preview the tax, using the oldest lots first. Nothing is traded.">
      <Loaded state={lots} lines={5}>
        {data => {
          const list = arr(data?.lots);
          if (list.length === 0) return <NoLots />;
          const groups = groupLots(list);
          const losing = groups.filter(g => g.pl < 0);
          const totalLoss = losing.reduce((s, g) => s + Math.abs(g.pl), 0);
          const requests = groups.filter(g => num(amounts[g.isin]) > 0).map(g => ({ isin: g.isin, amount_eur: amounts[g.isin] }));

          const harvest = () => {
            const next: Record<string, number> = {};
            losing.forEach(g => { next[g.isin] = Math.round(g.value); });
            setAmounts(next);
            simulate(losing.map(g => ({ isin: g.isin, amount_eur: Math.round(g.value) })));
          };

          const rows = arr(result?.results);
          const gross = rows.reduce((s, r) => s + num(r.sell_amount), 0);
          const thisYear = num(result?.tax_this_year ?? result?.total_tax);
          return (
            <>
              <div className="grid gap-4 md:grid-cols-2">
                <SelectField label="Kirchensteuer (church tax)" value={churchTax}
                  onChange={e => { setResult(null); onChurchTaxChange(parseFloat(e.target.value)); }}>
                  <option value={0}>None, rate applied 26,375 %</option>
                  <option value={0.08}>8 % (Bavaria, Baden-Württemberg), rate applied 27,82 %</option>
                  <option value={0.09}>9 % (other states), rate applied 28,00 %</option>
                </SelectField>
              </div>

              <SubHeadingRow actions={losing.length > 0
                ? <button type="button" onClick={harvest} disabled={running} className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]">Fill in losing positions</button>
                : undefined}>
                Amount to Sell
              </SubHeadingRow>
              {losing.length > 0 && (
                <p className="mb-3 text-[13px] text-ink-muted">
                  {plural(losing.length, 'position is', 'positions are')} below cost, with <Money value={totalLoss} /> of losses that could be realised.
                </p>
              )}
              <div className="grid gap-4 md:grid-cols-2">
                {groups.map(g => (
                  <NumberField key={g.isin} label={g.name} unit="€" value={num(amounts[g.isin])}
                    onValueChange={v => setAmounts(prev => ({ ...prev, [g.isin]: Math.max(0, v) }))}
                    hint={<>Worth <Money value={g.value} /> · unrealised <Money value={g.pl} sign /></>} />
                ))}
              </div>
              <button type="button" onClick={() => simulate(requests)} disabled={requests.length === 0 || running} className="apple-btn-primary mt-4 min-h-[44px]">
                {running ? 'Calculating…' : 'Preview tax impact'}
              </button>
              {requests.length === 0 && <p className="mt-2 text-[13px] text-ink-muted">Enter an amount for at least one security.</p>}

              {failed && <div className="mt-4"><ErrorState message="The simulation could not be run." details={failed} onRetry={() => simulate(requests)} /></div>}

              {rows.length > 0 && result && (
                <div className="mt-6" aria-live="polite">
                  <MetricGrid>
                    <MetricCard label="Net proceeds" value={fmtEUR(result.net_proceeds_this_year ?? result.total_proceeds)} sublabel="After this year’s tax" />
                    <MetricCard label="Tax this year" value={fmtEUR(Math.max(0, thisYear))}
                      sublabel={thisYear < 0
                        ? `Cuts this year’s tax by ${fmtEUR(-thisYear)}`
                        : `After your remaining allowance${num(result.allowance_remaining) > 0 ? ` (${fmtEUR(result.allowance_remaining)})` : ''} and loss pots`} />
                    <MetricCard label="Before allowance and loss pots" value={fmtEUR(result.total_tax)}
                      sublabel={num(result.effective_rate) > 0 ? `At ${fmtPct(num(result.effective_rate) * 100, { digits: 3 })} on taxable gains` : undefined} />
                    <MetricCard money={false} label="Tax as share of sale" value={fmtPct(gross > 0 ? (Math.max(0, thisYear) / gross) * 100 : 0)} />
                  </MetricGrid>
                  <ul className="mt-5 md:hidden flex flex-col gap-3">
                    {rows.map(r => (
                      <li key={r.isin} className="rounded-apple border border-divider px-4 py-3">
                        <p className="mb-2 text-[14px] font-medium text-ink">{r.name}</p>
                        <KeyValues rows={[
                          { label: 'Sold', value: <Money value={r.sell_amount} /> },
                          { label: 'Cost basis', value: <Money value={r.cost_basis} /> },
                          { label: 'Gain or loss', value: <Money value={r.realized_gain} sign tone={toneOf(num(r.realized_gain))} /> },
                          { label: 'Tax on this sale alone', value: r.private_sale ? 'Income tax (§23)' : <Money value={r.estimated_tax} cents /> },
                        ]} />
                        <SaleNote r={r} />
                      </li>
                    ))}
                  </ul>
                  <TableRegion label="Simulated sale by security" className="mt-5 hidden md:block">
                    <table className="apple-table">
                      <thead>
                        <tr>
                          <th scope="col">Security</th>
                          <th scope="col" className="num">Sold</th>
                          <th scope="col" className="num">Cost basis</th>
                          <th scope="col" className="num">Gain or loss</th>
                          <th scope="col" className="num">Tax on this sale alone</th>
                        </tr>
                      </thead>
                      <tbody>
                        {rows.map(r => (
                          <tr key={r.isin}>
                            <td>
                              <span className="block font-medium text-ink">{r.name}</span>
                              <SaleNote r={r} />
                            </td>
                            <td className="num"><Money value={r.sell_amount} /></td>
                            <td className="num"><Money value={r.cost_basis} /></td>
                            <td className="num"><Money value={r.realized_gain} sign tone={toneOf(num(r.realized_gain))} /></td>
                            <td className="num">{r.private_sale ? 'Income tax (§23)' : <Money value={r.estimated_tax} cents />}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TableRegion>
                  <p className="mt-2 text-[12px] text-ink-muted">
                    “Tax this year” prices the sale together with everything already realised this year: losses in one position offset gains in
                    another (share losses only against share gains), then carried-forward losses and the remaining saver’s allowance apply.
                  </p>
                </div>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

function SaleNote({ r }: { r: SellResult['results'][number] }) {
  if (r.private_sale) {
    return (
      <span className="block text-[12px] text-ink-muted">
        Private sale (§23): <Money value={r.private_sale_taxable_pl} sign /> held a year or less, taxed at your income-tax rate;{' '}
        <Money value={r.private_sale_tax_free_pl} sign /> tax-free
      </span>
    );
  }
  const tfs = num(r.tfs_rate ?? (r.is_equity_fund ? 0.3 : 0));
  const vp = num(r.vorabpauschale_deducted);
  if (tfs <= 0 && vp <= 0) return null;
  return (
    <span className="block text-[12px] text-ink-muted">
      {tfs > 0 && `${fmtPct(tfs * 100, { digits: 0 })} partial exemption applied`}
      {tfs > 0 && vp > 0 && ' · '}
      {vp > 0 && <>Vorabpauschale already taxed <Money value={vp} /> deducted</>}
    </span>
  );
}
