import type { AssetsOverview as Overview, PhysicalAsset } from '../../api/client';
import { fmt, fmtSigned, fmtPct, fmtDate, fmtMonth, KIND_LABEL, PART_COLORS, partColor, isProperty, cardCls } from './format';

interface Props {
  data: Overview;
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
      <div
        role="img"
        aria-label={`${fmtPct(pct, 0)} yours, ${asset.owed > 0 ? `${fmt(asset.owed)} owed` : 'nothing owed'}`}
        className="h-2.5 rounded-full overflow-hidden bg-[#EFE9E0] dark:bg-divider"
      >
        <div className="h-full bg-forest" style={{ width: `${pct}%` }} />
      </div>
      <div className="flex justify-between gap-3 mt-1.5 text-[12px] tabular-nums">
        <span className="text-forest font-semibold">Yours {fmt(asset.yours)} · {fmtPct(pct, 0)}</span>
        <span className="text-ink-muted">{asset.owed > 0 ? `Owed ${fmt(asset.owed)}` : 'Nothing owed'}</span>
      </div>
    </div>
  );
}

function subtitle(a: PhysicalAsset): string {
  const bits: string[] = [];
  if (a.details) bits.push(a.details);
  if (a.living_area_m2) bits.push(`${a.living_area_m2.toLocaleString('de-DE')} m²`);
  if (a.car) bits.push(`~${new Intl.NumberFormat('de-DE').format(Math.round(a.car.odometer_estimate_km / 100) * 100)} km`);
  bits.push(`bought ${fmtDate(a.purchase_date)}`);
  return bits.join(' · ');
}

export default function AssetsOverview({ data, onOpenAsset, onOpenLoan, onAdd }: Props) {
  const { assets, loans, totals, composition } = data;

  const parts: Part[] = [
    { key: 'investments', label: 'Investments', color: PART_COLORS.investments, worth: composition.investments, owed: 0, yours: composition.investments },
    { key: 'cash', label: 'Cash', color: PART_COLORS.cash, worth: composition.cash, owed: 0, yours: composition.cash },
    ...assets.map(a => ({ key: a.id, label: a.name, color: partColor(a.kind), worth: a.current_value, owed: a.owed, yours: a.yours })),
  ];
  const netWorth = parts.reduce((s, p) => s + p.yours, 0);
  const barTotal = parts.reduce((s, p) => s + Math.max(0, p.yours), 0);
  const worthTotal = parts.reduce((s, p) => s + p.worth, 0);

  return (
    <div className="space-y-2">
      {/* What's yours: each part at what you'd keep after paying off its loan */}
      <section aria-labelledby="yours-h" className="border-t border-divider pt-6 py-3 md:py-5">
        <h2 id="yours-h" className="font-serif text-heading text-ink px-1 md:px-0">What's yours</h2>
        <p className="text-[13px] text-ink-muted mt-1 mb-4 px-1 md:px-0">
          Each part counts with what you'd keep after paying off its loan, so the bar adds up to your net worth.
        </p>
        {barTotal > 0 && (
          <div
            role="img"
            aria-label={`Net worth ${fmt(netWorth)}: ${parts.filter(p => p.yours > 0).map(p => `${p.label} ${fmtPct(p.yours / barTotal * 100, 0)}`).join(', ')}`}
            className="flex h-7 rounded-md overflow-hidden gap-0.5"
          >
            {parts.filter(p => p.yours > 0).map(p => (
              <div key={p.key} style={{ width: `${(p.yours / barTotal) * 100}%`, background: p.color }} />
            ))}
          </div>
        )}
        {/* Phone: a list with the arithmetic as a sub-line. */}
        <ul className="md:hidden mt-4 text-[14px] tabular-nums">
          {parts.map(p => (
            <li key={p.key} className="flex items-start gap-2 py-2 border-b border-divider">
              <span aria-hidden="true" className="w-2.5 h-2.5 rounded-sm shrink-0 mt-1.5" style={{ background: p.color }} />
              <span className="flex-1 min-w-0">
                <span className="block truncate">{p.label}</span>
                {p.owed > 0 && <span className="block text-[12px] text-ink-muted">{fmt(p.worth)} worth − {fmt(p.owed)} owed</span>}
              </span>
              <span className="font-semibold shrink-0">{fmt(p.yours)}</span>
            </li>
          ))}
          <li className="flex justify-between pt-2.5 font-semibold"><span>Net worth</span><span className="text-forest">{fmt(netWorth)}</span></li>
        </ul>
        <div className="hidden md:block mt-4">
          <table className="w-full text-[14px] tabular-nums border-collapse">
            <thead>
              <tr className="text-[12px] text-ink-muted border-b border-divider">
                <th scope="col" className="py-1.5 font-medium text-left">Part</th>
                <th scope="col" className="py-1.5 font-medium text-right">Worth</th>
                <th scope="col" className="py-1.5 font-medium text-right">Owed</th>
                <th scope="col" className="py-1.5 font-semibold text-right text-ink">Yours</th>
              </tr>
            </thead>
            <tbody>
              {parts.map(p => (
                <tr key={p.key} className="border-b border-divider">
                  <th scope="row" className="py-2 font-normal text-left">
                    <span className="inline-flex items-center gap-2 min-w-0">
                      <span aria-hidden="true" className="w-2.5 h-2.5 rounded-sm shrink-0" style={{ background: p.color }} />
                      <span className="truncate">{p.label}</span>
                    </span>
                  </th>
                  <td className="py-2 text-right text-ink-body">{fmt(p.worth)}</td>
                  <td className={`py-2 text-right ${p.owed > 0 ? 'text-claret' : 'text-ink-muted'}`}>{p.owed > 0 ? `−${fmt(p.owed)}` : '—'}</td>
                  <td className="py-2 text-right font-semibold">{fmt(p.yours)}</td>
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr>
                <th scope="row" className="pt-2.5 font-semibold text-left">Net worth</th>
                <td className="pt-2.5 text-right text-ink-body">{fmt(worthTotal)}</td>
                <td className="pt-2.5 text-right text-claret">{totals.owed > 0 ? `−${fmt(totals.owed)}` : '—'}</td>
                <td className="pt-2.5 text-right font-semibold text-forest">{fmt(netWorth)}</td>
              </tr>
            </tfoot>
          </table>
        </div>
        {composition.as_of && (
          <p className="text-[11px] text-ink-muted mt-2">Investments and cash as of the {fmtDate(composition.as_of)} snapshot.</p>
        )}
      </section>

      <section aria-labelledby="pv-h" className="border-t border-divider pt-6 py-3 md:py-5">
        <div className="flex flex-wrap justify-between items-baseline gap-x-6 gap-y-1 mb-4 px-1 md:px-0">
          <h2 id="pv-h" className="font-serif text-heading text-ink">Property &amp; vehicles</h2>
          {assets.length > 0 && (
            <p className="text-[13px] text-ink-muted tabular-nums">
              Yours <strong className="text-forest">{fmt(totals.yours)}</strong> of <strong className="text-ink">{fmt(totals.worth)}</strong> worth
              {totals.owed > 0 && <> · {fmt(totals.owed)} owed</>}
            </p>
          )}
        </div>
        <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 320px), 1fr))' }}>
          {assets.map(a => (
            <button
              key={a.id}
              type="button"
              onClick={() => onOpenAsset(a.id)}
              className={`${cardCls} text-left flex flex-col gap-3.5 hover:border-forest transition-colors`}
            >
              <div className="flex justify-between gap-3 w-full">
                <div className="min-w-0">
                  <p className="text-[11px] tracking-[0.08em] uppercase text-ink-muted">
                    {KIND_LABEL[a.kind]}{isProperty(a.kind) && a.self_used ? ' · self-used' : ''}
                  </p>
                  <p className="text-[16px] font-semibold text-ink truncate">{a.name}</p>
                  <p className="text-[12px] text-ink-muted">{subtitle(a)}</p>
                </div>
                <div className="text-right shrink-0">
                  <p className="text-[11px] text-ink-muted">Worth</p>
                  <p className="font-serif text-[24px] leading-tight tabular-nums text-ink">{fmt(a.current_value)}</p>
                  <p className={`text-[12px] tabular-nums ${a.monthly_change >= 0 ? 'text-sage' : 'text-claret'}`}>
                    {fmtSigned(a.monthly_change)}/mo
                  </p>
                </div>
              </div>
              <div className="w-full"><YoursBar asset={a} /></div>
              <div className="flex flex-wrap gap-2 text-[12px]">
                {loans.filter(l => l.asset_id === a.id && l.fixed_until).map(l => (
                  <span key={l.id} className="px-2.5 py-0.5 rounded-full bg-parchment-deep text-ink-body">Fixed rate until {fmtMonth(l.fixed_until as string)}</span>
                ))}
                {a.car && <span className="px-2.5 py-0.5 rounded-full bg-parchment-deep text-ink-body">{fmtPct(a.car.effective_annual_rate * 100)}/yr at {new Intl.NumberFormat('de-DE').format(a.expected_km_per_year)} km</span>}
                {a.kind !== 'car' && <span className="px-2.5 py-0.5 rounded-full bg-parchment-deep text-ink-body">{a.tax_free_from ? `Sale tax-free from ${fmtDate(a.tax_free_from)}` : a.tax_note}</span>}
              </div>
            </button>
          ))}
          <button
            type="button"
            onClick={onAdd}
            className="flex flex-col items-center justify-center gap-1.5 min-h-[160px] p-5 rounded-xl border-[1.5px] border-dashed border-[#C9C2B6] text-forest text-center"
          >
            <span className="text-[15px] font-semibold">+ Add asset</span>
            <span className="text-[12px] text-ink-muted">Car · Apartment · House · Land · Other</span>
          </button>
        </div>
      </section>

      {loans.length > 0 && (
        <section aria-labelledby="loans-h" className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 id="loans-h" className="font-serif text-heading text-ink mb-4 px-1 md:px-0">Loans</h2>
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
                  <p className="text-[11px] tracking-[0.08em] uppercase text-ink-muted">{l.name}{l.lender ? ` · ${l.lender}` : ''}</p>
                  <p className="text-[15px] font-semibold text-ink">For {l.asset_name}</p>
                  <div role="img" aria-label={`${fmtPct(l.repaid_pct)} repaid`} className="mt-2.5 h-1.5 rounded-full bg-[#EFE9E0] dark:bg-divider overflow-hidden">
                    <div className="h-full bg-forest" style={{ width: `${Math.min(100, l.repaid_pct)}%` }} />
                  </div>
                  <p className="text-[12px] text-ink-muted mt-1.5 tabular-nums">{fmt(l.repaid)} of {fmt(l.principal)} repaid</p>
                </div>
                <div>
                  <p className="text-[12px] text-ink-muted">Still owed</p>
                  <p className="font-serif text-[22px] text-claret tabular-nums">{fmt(l.balance)}</p>
                </div>
                <div>
                  <p className="text-[12px] text-ink-muted">Monthly payment</p>
                  <p className="text-[15px] font-semibold tabular-nums">{fmt(l.monthly_payment)}</p>
                  {l.next_payment && (
                    <p className="text-[12px] text-ink-muted tabular-nums">{fmt(l.next_payment.interest)} interest · {fmt(l.next_payment.principal)} principal</p>
                  )}
                </div>
                <div>
                  <p className="text-[12px] text-ink-muted">{fmtPct(l.nominal_rate * 100, 2)}{l.fixed_until ? ` fixed until ${fmtDate(l.fixed_until)}` : ''}</p>
                  {l.remaining_at_fixed_end != null && (
                    <>
                      <p className="text-[15px] font-semibold tabular-nums">{fmt(l.remaining_at_fixed_end)} left then</p>
                      {l.remaining_at_fixed_end > 0 && <p className="text-[12px] text-ink-muted">needs follow-up financing</p>}
                    </>
                  )}
                </div>
              </button>
            ))}
          </div>
        </section>
      )}

      <section aria-labelledby="how-h" className="border-t border-divider pt-6 py-3 md:py-5">
        <h2 id="how-h" className="font-serif text-heading text-ink mb-4 px-1 md:px-0">How it counts</h2>
        <div className="grid gap-5 px-1 md:px-0" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 220px), 1fr))' }}>
          <div>
            <p className="text-[14px] font-semibold text-ink">Net worth</p>
            <p className="text-[13px] text-ink-body mt-1">Modelled market value of every asset minus what you still owe on its loans.</p>
          </div>
          <div>
            <p className="text-[14px] font-semibold text-ink">Returns &amp; projections</p>
            <p className="text-[13px] text-ink-body mt-1">TWR, projections and FIRE use investable wealth only — property, vehicles and their loans stay out.</p>
          </div>
          <div>
            <p className="text-[14px] font-semibold text-ink">Cashflow</p>
            <p className="text-[13px] text-ink-body mt-1">Loan interest counts as housing cost. The principal part counts as saving: it lowers your debt.</p>
          </div>
          <div>
            <p className="text-[14px] font-semibold text-ink">Attribution</p>
            <p className="text-[13px] text-ink-body mt-1">Value changes show as “Physical assets”; purchase costs and loan interest as their own bars.</p>
          </div>
        </div>
      </section>
    </div>
  );
}
