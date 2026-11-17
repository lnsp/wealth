import type { AssetsOverview as Overview, PhysicalAsset } from '../../api/client';
import { Section, AddChip, Chip, EmptyState, ProgressBar } from '../ui';
import { fmtEUR, fmtPct, fmtDate, fmtDateLong, fmtMonth, fmtNum } from '../../utils/format';
import { KIND_LABEL, PART_COLORS, partColor, isProperty, cardCls } from './format';

interface Props {
  data: Overview;
  /** Net worth as shown in the page hero (latest snapshot), to explain any gap to the live total here. */
  snapshotNetWorth?: number;
  onOpenAsset: (id: string) => void;
  onOpenLoan: (id: string) => void;
  onAdd: () => void;
}

interface Part {
  key: string;
  label: string;
  color: string;
  worth: number;
  owed: number;
  yours: number;
}

function YoursBar({ asset }: { asset: PhysicalAsset }) {
  const pct = Math.max(0, Math.min(100, asset.yours_pct));
  return (
    <div>
      <ProgressBar value={pct} label={`Share of ${asset.name} that is yours`} className="h-2.5" />
      <div className="flex flex-wrap justify-between gap-x-3 gap-y-0.5 mt-1.5 text-[12px]">
        <span className="text-ink font-semibold">Yours <span data-privacy="blur">{fmtEUR(asset.yours)}</span> · {fmtPct(pct, { digits: 0 })}</span>
        <span className="text-ink-muted">{asset.owed > 0 ? <>Owed <span data-privacy="blur">{fmtEUR(asset.owed)}</span></> : 'Nothing owed'}</span>
      </div>
    </div>
  );
}

function subtitle(a: PhysicalAsset): string {
  const bits: string[] = [];
  if (a.details) bits.push(a.details);
  if (a.living_area_m2) bits.push(`${fmtNum(a.living_area_m2, a.living_area_m2 % 1 === 0 ? 0 : 1)} m²`);
  if (a.car) bits.push(`about ${fmtNum(Math.round(a.car.odometer_estimate_km / 100) * 100)} km`);
  bits.push(`bought ${fmtDate(a.purchase_date)}`);
  return bits.join(' · ');
}

export default function AssetsOverview({ data, snapshotNetWorth, onOpenAsset, onOpenLoan, onAdd }: Props) {
  const { assets, loans, totals, composition } = data;

  const parts: Part[] = [
    { key: 'investments', label: 'Investments', color: PART_COLORS.investments, worth: composition.investments, owed: 0, yours: composition.investments },
    { key: 'cash', label: 'Cash', color: PART_COLORS.cash, worth: composition.cash, owed: 0, yours: composition.cash },
    ...assets.map(a => ({ key: a.id, label: a.name, color: partColor(a.kind), worth: a.current_value, owed: a.owed, yours: a.yours })),
  ];
  const netWorth = parts.reduce((s, p) => s + p.yours, 0);
  const barTotal = parts.reduce((s, p) => s + Math.max(0, p.yours), 0);
  const worthTotal = parts.reduce((s, p) => s + p.worth, 0);
  // The hero shows the last stored snapshot; property, vehicles and loans on this tab are
  // valued at this moment. Name the gap instead of showing two unexplained totals.
  const gap = snapshotNetWorth != null ? netWorth - snapshotNetWorth : 0;
  const showGap = snapshotNetWorth != null && Math.abs(gap) >= 1;

  return (
    <div>
      {/* What's yours: each part at what you'd keep after paying off its loan */}
      <Section
        first
        title="What's yours"
        subtitle="Each part counts with what you'd keep after paying off its loan, so the bar adds up to your net worth."
      >
        {barTotal > 0 && (
          <div
            role="img"
            aria-label={`Net worth by part: ${parts.filter(p => p.yours > 0).map(p => `${p.label} ${fmtPct(p.yours / barTotal * 100, { digits: 0 })}`).join(', ')}`}
            className="flex h-6 rounded-[3px] overflow-hidden gap-0.5 bg-divider"
          >
            {parts.filter(p => p.yours > 0).map(p => (
              <div key={p.key} style={{ width: `${(p.yours / barTotal) * 100}%`, background: p.color }} />
            ))}
          </div>
        )}
        {/* Phone: a list with the arithmetic as a sub-line. */}
        <ul className="md:hidden mt-4 text-[14px]">
          {parts.map(p => (
            <li key={p.key} className="flex items-start gap-2.5 py-2.5 border-b border-divider">
              <span aria-hidden="true" className="w-2.5 h-2.5 rounded-[2px] shrink-0 mt-1.5" style={{ background: p.color }} />
              <span className="flex-1 min-w-0">
                <span className="block truncate text-ink" title={p.label}>{p.label}</span>
                {p.owed > 0 && <span className="block text-[12px] text-ink-muted" data-privacy="blur">{fmtEUR(p.worth)} worth − {fmtEUR(p.owed)} owed</span>}
              </span>
              <span className="font-semibold text-ink shrink-0" data-privacy="blur">{fmtEUR(p.yours)}</span>
            </li>
          ))}
          <li className="flex justify-between pt-3 font-semibold text-ink"><span>Net worth</span><span data-privacy="blur">{fmtEUR(netWorth)}</span></li>
        </ul>
        <div className="hidden md:block mt-4 apple-card overflow-x-auto" role="region" tabIndex={0} aria-label="What's yours, by part">
          <table className="apple-table">
            <thead>
              <tr>
                <th scope="col">Part</th>
                <th scope="col" className="num">Worth</th>
                <th scope="col" className="num">Owed</th>
                <th scope="col" className="num">Yours</th>
              </tr>
            </thead>
            <tbody>
              {parts.map(p => (
                <tr key={p.key}>
                  <td>
                    <span className="inline-flex items-center gap-2 min-w-0 text-ink">
                      <span aria-hidden="true" className="w-2.5 h-2.5 rounded-[2px] shrink-0" style={{ background: p.color }} />
                      <span className="truncate">{p.label}</span>
                    </span>
                  </td>
                  <td className="num" data-privacy="blur">{fmtEUR(p.worth)}</td>
                  <td className={`num ${p.owed > 0 ? 'text-claret' : 'text-ink-muted'}`} data-privacy={p.owed > 0 ? 'blur' : undefined}>{p.owed > 0 ? fmtEUR(-p.owed) : '—'}</td>
                  <td className="num font-semibold text-ink" data-privacy="blur">{fmtEUR(p.yours)}</td>
                </tr>
              ))}
              <tr>
                <td className="font-semibold text-ink">Net worth</td>
                <td className="num" data-privacy="blur">{fmtEUR(worthTotal)}</td>
                <td className={`num ${totals.owed > 0 ? 'text-claret' : 'text-ink-muted'}`} data-privacy={totals.owed > 0 ? 'blur' : undefined}>{totals.owed > 0 ? fmtEUR(-totals.owed) : '—'}</td>
                <td className="num font-semibold text-ink" data-privacy="blur">{fmtEUR(netWorth)}</td>
              </tr>
            </tbody>
          </table>
        </div>
        {(composition.as_of || showGap) && (
          <p className="text-[12px] text-ink-muted mt-2.5">
            {composition.as_of && <>Investments and cash are from the snapshot of {fmtDateLong(composition.as_of)}; property, vehicles and loans are valued as of today. </>}
            {showGap && (
              <>
                The total at the top of the page is that snapshot (<span data-privacy="blur">{fmtEUR(snapshotNetWorth)}</span>), so it differs by{' '}
                <span data-privacy="blur">{fmtEUR(gap, { sign: true })}</span> until the next snapshot.
              </>
            )}
          </p>
        )}
      </Section>

      <Section
        title="Property & vehicles"
        subtitle={assets.length > 0 ? (
          <>
            Yours <strong className="text-ink" data-privacy="blur">{fmtEUR(totals.yours)}</strong> of <strong className="text-ink" data-privacy="blur">{fmtEUR(totals.worth)}</strong> worth
            {totals.owed > 0 && <> · <span data-privacy="blur">{fmtEUR(totals.owed)}</span> owed</>}
          </>
        ) : undefined}
        actions={assets.length > 0 ? <AddChip onClick={onAdd}>Add asset</AddChip> : undefined}
      >
        {assets.length === 0 ? (
          <EmptyState
            title="No property or vehicles yet"
            message="Add a car, an apartment, a house, land or another asset to count it in your net worth, together with the loan that finances it."
            action={{ label: 'Add asset', onClick: onAdd }}
          />
        ) : (
          <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 320px), 1fr))' }}>
            {assets.map(a => (
              <button
                key={a.id}
                type="button"
                onClick={() => onOpenAsset(a.id)}
                className={`${cardCls} text-left flex flex-col gap-3.5 hover:border-forest transition-colors`}
              >
                <div className="flex flex-wrap justify-between gap-x-3 gap-y-2 w-full">
                  <div className="min-w-0">
                    <p className="label-caps">
                      {KIND_LABEL[a.kind]}{isProperty(a.kind) && a.self_used ? ' · self-used' : ''}
                    </p>
                    <p className="text-[16px] font-semibold text-ink truncate" title={a.name}>{a.name}</p>
                    <p className="text-[12px] text-ink-muted">{subtitle(a)}</p>
                  </div>
                  <div className="text-right shrink-0 ml-auto">
                    <p className="text-[12px] text-ink-muted">Worth</p>
                    <p className="font-serif text-[24px] leading-8 text-ink" data-privacy="blur">{fmtEUR(a.current_value)}</p>
                    <p className={`text-[12px] ${a.monthly_change > 0 ? 'text-sage' : a.monthly_change < 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">
                      {fmtEUR(a.monthly_change, { sign: true })} / month
                    </p>
                  </div>
                </div>
                <div className="w-full"><YoursBar asset={a} /></div>
                <div className="flex flex-wrap gap-2">
                  {loans.filter(l => l.asset_id === a.id && l.fixed_until).map(l => (
                    <Chip key={l.id} className="whitespace-normal">Fixed rate until {fmtMonth(l.fixed_until as string)}</Chip>
                  ))}
                  {a.car && <Chip className="whitespace-normal">Loses {fmtPct(a.car.effective_annual_rate * 100)} / year at {fmtNum(a.expected_km_per_year)} km / year</Chip>}
                  {a.kind !== 'car' && <Chip className="whitespace-normal">{a.tax_free_from ? `Sale tax-free from ${fmtDate(a.tax_free_from)}` : a.tax_note}</Chip>}
                </div>
              </button>
            ))}
          </div>
        )}
      </Section>

      {loans.length > 0 && (
        <Section title="Loans">
          <div className="space-y-3">
            {loans.map(l => (
              <button
                key={l.id}
                type="button"
                onClick={() => onOpenLoan(l.id)}
                className={`${cardCls} w-full text-left grid gap-4 hover:border-forest transition-colors`}
                style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 190px), 1fr))' }}
              >
                <div>
                  <p className="label-caps">{l.name}{l.lender ? ` · ${l.lender}` : ''}</p>
                  <p className="text-[15px] font-semibold text-ink">For {l.asset_name}</p>
                  <ProgressBar value={Math.min(100, l.repaid_pct)} label={`${l.name}: share repaid`} className="mt-2.5 h-1.5" />
                  <p className="text-[12px] text-ink-muted mt-1.5"><span data-privacy="blur">{fmtEUR(l.repaid)} of {fmtEUR(l.principal)}</span> repaid ({fmtPct(l.repaid_pct)})</p>
                </div>
                <div>
                  <p className="text-[12px] text-ink-muted">Still owed</p>
                  <p className={`font-serif text-[24px] leading-8 ${l.balance > 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">{fmtEUR(l.balance)}</p>
                </div>
                <div>
                  <p className="text-[12px] text-ink-muted">Monthly payment</p>
                  <p className="text-[15px] font-semibold text-ink" data-privacy="blur">{fmtEUR(l.monthly_payment, { cents: true })}</p>
                  {l.next_payment && (
                    <p className="text-[12px] text-ink-muted" data-privacy="blur">{fmtEUR(l.next_payment.interest)} interest · {fmtEUR(l.next_payment.principal)} principal</p>
                  )}
                </div>
                <div>
                  <p className="text-[12px] text-ink-muted">{fmtPct(l.nominal_rate * 100, { digits: 2 })}{l.fixed_until ? ` fixed until ${fmtDate(l.fixed_until)}` : ''}</p>
                  {l.remaining_at_fixed_end != null && (
                    <>
                      <p className="text-[15px] font-semibold text-ink"><span data-privacy="blur">{fmtEUR(l.remaining_at_fixed_end)}</span> left then</p>
                      {l.remaining_at_fixed_end > 0 && <p className="text-[12px] text-ink-muted">needs follow-up financing</p>}
                    </>
                  )}
                </div>
              </button>
            ))}
          </div>
        </Section>
      )}

      <Section title="How it counts">
        <div className="grid gap-5" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 220px), 1fr))' }}>
          <div>
            <h3 className="text-[14px] font-semibold text-ink">Net worth</h3>
            <p className="text-[13px] text-ink-body mt-1">Modelled market value of every asset minus what you still owe on its loans.</p>
          </div>
          <div>
            <h3 className="text-[14px] font-semibold text-ink">Returns &amp; projections</h3>
            <p className="text-[13px] text-ink-body mt-1">Time-weighted return, projections and FIRE use investable wealth only — property, vehicles and their loans stay out.</p>
          </div>
          <div>
            <h3 className="text-[14px] font-semibold text-ink">Cashflow</h3>
            <p className="text-[13px] text-ink-body mt-1">Loan interest counts as housing cost. The principal part counts as saving: it lowers your debt.</p>
          </div>
          <div>
            <h3 className="text-[14px] font-semibold text-ink">Attribution</h3>
            <p className="text-[13px] text-ink-body mt-1">Value changes show as “Property and vehicles”; purchase costs and loan interest as their own bars.</p>
          </div>
        </div>
      </Section>
    </div>
  );
}
