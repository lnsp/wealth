import { useMemo, useState } from 'react';
import { api, type Loan, type PhysicalAsset } from '../../api/client';
import EChartWrapper from '../charts/EChartWrapper';
import { useThemeColors } from '../../hooks/useThemeColors';
import LoanFields, { loanFormFrom, loanInputFrom } from './LoanFields';
import { fmt, fmtSigned, fmtKm, fmtPct, fmtDate, fmtMonth, today, KIND_LABEL, isProperty, fieldCls, labelCls, cardCls } from './format';

interface Props {
  asset: PhysicalAsset;
  loans: Loan[];
  onBack: () => void;
  onEdit: () => void;
  onOpenLoan: (id: string) => void;
  onChanged: () => void; // reload everything
  onDeleted: () => void;
}

function Kpi({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: 'good' | 'bad' | 'warn' | 'brand' }) {
  const color = tone === 'good' ? 'text-sage' : tone === 'bad' ? 'text-claret' : tone === 'warn' ? 'text-amber' : tone === 'brand' ? 'text-forest' : 'text-ink';
  return (
    <div>
      <dt className="text-[12px] text-ink-muted">{label}</dt>
      <dd className={`text-[19px] font-semibold tabular-nums ${color}`}>{value}</dd>
      {sub && <dd className="text-[12px] text-ink-muted">{sub}</dd>}
    </div>
  );
}

export default function AssetDetail({ asset: a, loans, onBack, onEdit, onOpenLoan, onChanged, onDeleted }: Props) {
  const tc = useThemeColors();
  const [error, setError] = useState<string | null>(null);
  const [obs, setObs] = useState({ date: today(), odometer: '', appraisal: '', note: '' });
  const [imp, setImp] = useState({ date: today(), amount: '', description: '' });
  const [addingLoan, setAddingLoan] = useState(false);
  const [loanForm, setLoanForm] = useState(() => loanFormFrom(null, { payout_date: a.purchase_date }));
  const [busy, setBusy] = useState(false);
  const property = isProperty(a.kind);
  const hasLoan = loans.length > 0;

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  };

  const chart = useMemo(() => {
    const pts = a.curve;
    const seam = pts.findIndex(p => p.projected);
    const labels = pts.map(p => new Date(p.date).toLocaleDateString('de-DE', { month: 'short', year: '2-digit' }));
    const appraisals = a.observations.flatMap(o => {
      const i = o.appraised_value == null ? -1 : pts.findIndex(p => p.date >= o.date);
      return i < 0 ? [] : [{ name: `Appraisal ${o.date}`, coord: [i, o.appraised_value as number] as [number, number] }];
    });
    const series: object[] = [];
    if (hasLoan) {
      series.push(
        { name: 'Owed', type: 'line', stack: 'v', data: pts.map(p => Math.min(p.owed, p.value)), showSymbol: false, lineStyle: { width: 0 }, areaStyle: { color: tc.claret, opacity: 0.22 }, itemStyle: { color: tc.claret } },
        { name: 'Yours', type: 'line', stack: 'v', data: pts.map(p => Math.max(0, p.value - p.owed)), showSymbol: false, lineStyle: { width: 0 }, areaStyle: { color: tc.forest, opacity: 0.22 }, itemStyle: { color: tc.forest } },
      );
    }
    series.push({
      name: 'Value', type: 'line', data: pts.map(p => p.value), showSymbol: false,
      lineStyle: { width: 2, color: tc.forest }, itemStyle: { color: tc.forest },
      areaStyle: hasLoan ? undefined : { color: tc.forest, opacity: 0.12 },
      markPoint: appraisals.length ? { symbol: 'circle', symbolSize: 9, label: { show: false }, itemStyle: { color: tc.gold }, data: appraisals } : undefined,
      markArea: seam > 0 ? { silent: true, itemStyle: { color: tc.inkMuted, opacity: 0.07 }, label: { show: true, position: 'insideTopLeft', color: tc.inkMuted, fontSize: 11, formatter: 'Forecast' }, data: [[{ xAxis: seam - 1 }, { xAxis: pts.length - 1 }]] } : undefined,
    });
    if (hasLoan) {
      series.push({ name: 'Owed to bank', type: 'line', data: pts.map(p => p.owed), showSymbol: false, lineStyle: { width: 1.5, color: tc.claret }, itemStyle: { color: tc.claret } });
    }
    return {
      tooltip: {
        trigger: 'axis' as const,
        formatter: (params: unknown) => {
          const ps = params as { dataIndex: number }[];
          if (!ps.length) return '';
          const p = pts[ps[0].dataIndex];
          let html = `${fmtDate(p.date)}${p.projected ? ' · forecast' : ''}<br/>Worth <strong>${fmt(p.value)}</strong>`;
          if (hasLoan) html += `<br/>Owed ${fmt(p.owed)}<br/>Yours <strong>${fmt(p.value - p.owed)}</strong>`;
          return html;
        },
      },
      legend: hasLoan ? { data: ['Value', 'Owed to bank', 'Yours'], bottom: 0, icon: 'circle', itemWidth: 8, itemHeight: 8, textStyle: { fontSize: 11, color: tc.inkBody } } : undefined,
      grid: { top: 16, right: 12, bottom: hasLoan ? 46 : 28, left: 52 },
      xAxis: { type: 'category' as const, data: labels, axisLabel: { fontSize: 10, color: tc.inkMuted, interval: Math.max(Math.floor(pts.length / 7) - 1, 0) }, axisLine: { lineStyle: { color: tc.divider } }, axisTick: { show: false } },
      yAxis: { type: 'value' as const, min: 0, axisLabel: { fontSize: 10, color: tc.inkMuted, formatter: (v: number) => `${Math.round(v / 1000)}k` }, splitLine: { lineStyle: { color: tc.divider, type: 'dashed' as const } } },
      series,
    };
  }, [a, hasLoan, tc]);

  const last = a.curve[a.curve.length - 1];
  const remove = () => {
    if (!window.confirm(`Delete ${a.name}${hasLoan ? ' and its loan' : ''}? It will be removed from your net-worth history.`)) return;
    setBusy(true);
    api.deleteAsset(a.id).then(onDeleted).catch(err => { setError(err.message); setBusy(false); });
  };

  return (
    <div className="space-y-4">
      <button type="button" onClick={onBack} className="text-forest text-[13px] font-medium">← Assets</button>

      <section className="flex flex-wrap justify-between items-end gap-x-8 gap-y-4 pb-5 border-b border-divider">
        <div className="min-w-0">
          <p className="text-[11px] tracking-[0.08em] uppercase text-ink-muted">{KIND_LABEL[a.kind]}{property ? (a.self_used ? ' · self-used' : ' · rented out') : ''}</p>
          <h2 className="font-serif text-[30px] md:text-[36px] leading-tight text-ink">{a.name}</h2>
          <p className="text-[13px] text-ink-muted mt-1">
            {[a.details, a.living_area_m2 ? `${a.living_area_m2.toLocaleString('de-DE')} m²` : '', a.year_built ? `built ${a.year_built}` : '',
              a.first_registration ? `registered ${fmtDate(a.first_registration)}` : '', `bought ${fmtDate(a.purchase_date)}`].filter(Boolean).join(' · ')}
          </p>
        </div>
        <div className="text-right">
          <p className="text-[12px] text-ink-muted">Worth</p>
          <p className="font-serif text-[38px] md:text-[44px] leading-none tabular-nums text-ink">{fmt(a.current_value)}</p>
          <p className={`text-[13px] mt-1.5 tabular-nums ${a.value_change >= 0 ? 'text-sage' : 'text-claret'}`}>{fmtSigned(a.value_change)} ({fmtPct(a.value_change_pct)}) vs. purchase price</p>
          {a.cost_basis > a.purchase_price && (
            <p className="text-[13px] text-ink-muted tabular-nums">{fmtSigned(a.vs_cost_basis)} vs. everything paid</p>
          )}
        </div>
      </section>

      <dl className="grid gap-x-8 gap-y-4 py-1" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 170px), 1fr))' }}>
        <Kpi label="Yours" value={fmt(a.yours)} sub={hasLoan ? `${fmtPct(a.yours_pct, 0)} · ${fmt(a.owed)} owed` : 'nothing owed'} tone="brand" />
        <Kpi label="Value change" value={`${fmtSigned(a.monthly_change)}/month`} tone={a.monthly_change >= 0 ? 'good' : 'bad'}
          sub={a.property ? `prices ${fmtSigned(a.property.price_effect_per_year)}/yr${a.property.has_building_aging ? `, aging ${fmtSigned(a.property.aging_effect_per_year)}/yr` : ''}` : a.car ? `${fmtPct(a.car.effective_annual_rate * 100)}/yr at ${fmtKm(a.expected_km_per_year)}/yr` : undefined} />
        {a.index && (
          <Kpi label="House price index" value={`${a.index.latest_value.toLocaleString('de-DE', { minimumFractionDigits: 1 })} (${a.index.latest_quarter})`}
            sub={a.index.year_over_year != null ? `${a.index.year_over_year >= 0 ? '+' : ''}${fmtPct(a.index.year_over_year * 100)} in a year · Destatis via Eurostat` : 'Destatis via Eurostat'} />
        )}
        {a.car && <Kpi label="Odometer (est.)" value={fmtKm(a.car.odometer_estimate_km)} sub={a.pct_of_list != null ? `${fmtPct(a.pct_of_list, 0)} of list price` : undefined} />}
        {hasLoan && a.current_value > 0 && <Kpi label="Loan-to-value" value={fmtPct((a.owed / a.current_value) * 100, 0)} />}
        <Kpi label="Tax on a sale" value={a.tax_free_from ? (a.tax_free_from <= today() ? 'None' : `Until ${fmtDate(a.tax_free_from)}`) : 'None'} sub={a.tax_note} />
      </dl>

      <section aria-labelledby="d-chart" className={cardCls}>
        <h3 id="d-chart" className="font-serif text-[21px] text-ink">{hasLoan ? 'Worth, owed and yours' : 'Value over time'}</h3>
        <EChartWrapper option={chart} height="260px" />
        <p className="text-[12px] text-ink-muted mt-1">
          {a.car
            ? `Forecast at ${fmtKm(a.expected_km_per_year)}/yr.`
            : a.index
              ? `Prices follow the house price index (Destatis via Eurostat) up to ${a.index.latest_quarter}, then ${fmtPct(a.price_drift * 100)}/yr${a.property?.has_building_aging ? '; the building ages in a straight line' : ''}${hasLoan ? '; the loan continues as agreed' : ''}.`
              : `Forecast: prices ${fmtPct(a.price_drift * 100)}/yr${a.property?.has_building_aging ? ', building ages in a straight line' : ''}${hasLoan ? ', loan continues as agreed' : ''}.`}
          {last && <> In {new Date(last.date).getFullYear()}: worth {fmt(last.value)}{hasLoan ? `, owed ${fmt(last.owed)}` : ''}.</>}
        </p>
      </section>

      <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 340px), 1fr))' }}>
        {a.property && a.property.has_building_aging && (
          <section aria-labelledby="d-split" className={cardCls}>
            <h3 id="d-split" className="font-serif text-[21px] text-ink mb-3">Land and building</h3>
            <div role="img" aria-label={`Land ${fmtPct((a.property.land_value_now / a.current_value) * 100, 0)}, building ${fmtPct((a.property.building_value_now / a.current_value) * 100, 0)}`} className="flex h-3 rounded-full overflow-hidden gap-0.5">
              <div className="bg-gold" style={{ width: `${(a.property.land_value_now / a.current_value) * 100}%` }} />
              <div className="bg-[#9DB59D]" style={{ width: `${(a.property.building_value_now / a.current_value) * 100}%` }} />
            </div>
            <div className="grid grid-cols-2 gap-4 mt-3.5">
              <div>
                <p className="text-[13px] text-ink-muted">Land</p>
                <p className="text-[17px] font-semibold tabular-nums">{fmt(a.property.land_value_now)}</p>
                <p className="text-[12px] text-ink-muted">Follows prices, never ages.</p>
              </div>
              <div>
                <p className="text-[13px] text-ink-muted">Building</p>
                <p className="text-[17px] font-semibold tabular-nums">{fmt(a.property.building_value_now)}</p>
                <p className="text-[12px] text-ink-muted">{a.property.remaining_life_years.toLocaleString('de-DE')} of {a.useful_life_years} years left.</p>
              </div>
            </div>
          </section>
        )}

        <section aria-labelledby="d-cost" className={cardCls}>
          <h3 id="d-cost" className="font-serif text-[21px] text-ink mb-2">Purchase &amp; cost basis</h3>
          <table className="w-full text-[14px] tabular-nums border-collapse">
            <tbody>
              <tr className="border-b border-divider"><td className="py-2">Purchase price</td><td className="py-2 text-right">{fmt(a.purchase_price)}</td></tr>
              {a.purchase_costs > 0 && <tr className="border-b border-divider"><td className="py-2 text-ink-body">Purchase costs</td><td className="py-2 text-right">{fmt(a.purchase_costs)}</td></tr>}
              {a.improvements.length > 0 && <tr className="border-b border-divider"><td className="py-2 text-ink-body">Improvements</td><td className="py-2 text-right">{fmt(a.improvements.reduce((s, i) => s + i.amount, 0))}</td></tr>}
              <tr><td className="pt-2.5 font-semibold">Cost basis</td><td className="pt-2.5 text-right font-semibold">{fmt(a.cost_basis)}</td></tr>
            </tbody>
          </table>
          {a.purchase_costs > 0 && <p className="text-[12px] text-ink-muted mt-2.5">Purchase costs lowered net worth on {fmtDate(a.purchase_date)} (“Purchase costs” in attribution); the cost basis is kept for tax.</p>}
          {a.notarized_date && <p className="text-[12px] text-ink-muted mt-1">Notarised {fmtDate(a.notarized_date)}.</p>}
          <div className="mt-3">
            <p className="text-[12px] text-ink-muted mb-1">Linked payments</p>
            {a.linked_transactions.length === 0 ? (
              <p className="text-[13px] text-ink-muted">None — the purchase counts as money from untracked accounts. <button type="button" onClick={onEdit} className="text-forest font-medium">Link payments</button></p>
            ) : (
              <ul className="text-[13px] divide-y divide-divider/60">
                {a.linked_transactions.map(t => (
                  <li key={t.id} className="flex justify-between gap-3 py-1.5">
                    <span className="min-w-0 truncate"><span className="text-ink-muted tabular-nums">{fmtDate(t.date)}</span> · {t.counterparty || (t.purpose === 'improvement' ? 'Improvement' : 'Payment')}</span>
                    <span className="tabular-nums shrink-0">{fmt(Math.abs(t.amount))}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>

        <section aria-labelledby="d-obs" className={cardCls}>
          <h3 id="d-obs" className="font-serif text-[21px] text-ink mb-2">{a.car ? 'Readings & appraisals' : 'Appraisals & improvements'}</h3>
          <ul className="text-[13px] divide-y divide-divider/60 mb-3">
            <li className="flex justify-between gap-3 py-1.5"><span><span className="text-ink-muted tabular-nums">{fmtDate(a.purchase_date)}</span> · Purchase</span><span className="tabular-nums">{fmt(a.purchase_price)}</span></li>
            {a.observations.map(o => (
              <li key={`o-${o.date}`} className="flex items-center justify-between gap-3 py-1.5">
                <span className="min-w-0 truncate"><span className="text-ink-muted tabular-nums">{fmtDate(o.date)}</span> · {[o.odometer_km != null && fmtKm(o.odometer_km), o.appraised_value != null && `valued at ${fmt(o.appraised_value)}`, o.note].filter(Boolean).join(' · ')}</span>
                <button type="button" disabled={busy} onClick={() => run(() => api.deleteAssetObservation(a.id, o.date))} className="text-claret text-[12px] font-medium shrink-0" aria-label={`Remove entry from ${fmtDate(o.date)}`}>Remove</button>
              </li>
            ))}
            {a.improvements.map(i => (
              <li key={`i-${i.id}`} className="flex items-center justify-between gap-3 py-1.5">
                <span className="min-w-0 truncate"><span className="text-ink-muted tabular-nums">{fmtDate(i.date)}</span> · {i.description || 'Improvement'} · {fmt(i.amount)}</span>
                <button type="button" disabled={busy} onClick={() => run(() => api.deleteAssetImprovement(a.id, i.id))} className="text-claret text-[12px] font-medium shrink-0" aria-label={`Remove improvement from ${fmtDate(i.date)}`}>Remove</button>
              </li>
            ))}
          </ul>
          <form
            onSubmit={e => {
              e.preventDefault();
              if (!obs.odometer && !obs.appraisal) { setError('Enter an odometer reading or a market value.'); return; }
              run(() => api.upsertAssetObservation(a.id, {
                date: obs.date,
                odometer_km: obs.odometer ? parseInt(obs.odometer, 10) : undefined,
                appraised_value: obs.appraisal ? parseFloat(obs.appraisal) : undefined,
                note: obs.note || undefined,
              }).then(() => setObs({ date: today(), odometer: '', appraisal: '', note: '' })));
            }}
            className="grid gap-2 items-end"
            style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 120px), 1fr))' }}
          >
            <label className={labelCls}>Date<input type="date" required min={a.purchase_date} className={fieldCls} value={obs.date} onChange={e => setObs({ ...obs, date: e.target.value })} /></label>
            {a.car && <label className={labelCls}>Odometer (km)<input type="number" min="0" className={fieldCls} value={obs.odometer} onChange={e => setObs({ ...obs, odometer: e.target.value })} placeholder={String(a.car.odometer_estimate_km)} /></label>}
            <label className={labelCls}>Market value (€)<input type="number" min="1" className={fieldCls} value={obs.appraisal} onChange={e => setObs({ ...obs, appraisal: e.target.value })} placeholder={a.car ? 'optional' : ''} /></label>
            <label className={labelCls}>Note<input className={fieldCls} value={obs.note} onChange={e => setObs({ ...obs, note: e.target.value })} placeholder={a.car ? 'e.g. DAT' : 'e.g. bank valuation'} /></label>
            <button type="submit" disabled={busy} className="apple-btn-primary text-[13px] px-3 py-2">Add</button>
          </form>
          <p className="text-[12px] text-ink-muted mt-2">A market value re-anchors the curve from that date{a.car ? '; odometer readings adjust the per-km loss' : ''}.</p>
          {a.kind !== 'car' && (
            <form
              onSubmit={e => {
                e.preventDefault();
                run(() => api.createAssetImprovement(a.id, { date: imp.date, amount: parseFloat(imp.amount), description: imp.description })
                  .then(() => setImp({ date: today(), amount: '', description: '' })));
              }}
              className="grid gap-2 items-end mt-4 pt-3 border-t border-divider"
              style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 120px), 1fr))' }}
            >
              <label className={labelCls}>Improvement date<input type="date" required min={a.purchase_date} className={fieldCls} value={imp.date} onChange={e => setImp({ ...imp, date: e.target.value })} /></label>
              <label className={labelCls}>Amount (€)<input type="number" min="1" required className={fieldCls} value={imp.amount} onChange={e => setImp({ ...imp, amount: e.target.value })} /></label>
              <label className={labelCls}>What<input className={fieldCls} value={imp.description} onChange={e => setImp({ ...imp, description: e.target.value })} placeholder="e.g. new bathroom" /></label>
              <button type="submit" disabled={busy} className="apple-btn-secondary text-[13px] px-3 py-2">+ Improvement</button>
            </form>
          )}
        </section>
      </div>

      <section aria-labelledby="d-fin" className={cardCls}>
        <div className="flex flex-wrap justify-between items-baseline gap-3 mb-3">
          <h3 id="d-fin" className="font-serif text-[21px] text-ink">Financing</h3>
          {!addingLoan && <button type="button" onClick={() => setAddingLoan(true)} className="apple-btn-secondary text-[13px] px-3 py-1.5">+ Add loan</button>}
        </div>
        {loans.length === 0 && !addingLoan && <p className="text-[13px] text-ink-muted">No loan — {a.kind === 'car' ? 'paid in cash' : 'fully owned'}.</p>}
        <div className="space-y-2">
          {loans.map(l => (
            <button key={l.id} type="button" onClick={() => onOpenLoan(l.id)} className="w-full text-left flex flex-wrap justify-between items-center gap-4 p-3 rounded-lg border border-divider hover:border-forest">
              <span className="min-w-0">
                <span className="block text-[15px] font-semibold text-ink">{l.name}{l.lender ? ` · ${l.lender}` : ''}</span>
                <span className="block text-[12px] text-ink-muted">{fmtPct(l.nominal_rate * 100, 2)}{l.fixed_until ? ` fixed until ${fmtMonth(l.fixed_until)}` : ''} · {l.matched_count} of {l.due_count} payments matched</span>
              </span>
              <span className="flex gap-6 tabular-nums">
                <span><span className="block text-[12px] text-ink-muted">Owed</span><span className="text-[15px] font-semibold text-claret">{fmt(l.balance)}</span></span>
                <span><span className="block text-[12px] text-ink-muted">Monthly</span><span className="text-[15px] font-semibold">{fmt(l.monthly_payment)}</span></span>
                <span aria-hidden="true" className="self-center text-forest">→</span>
              </span>
            </button>
          ))}
        </div>
        {addingLoan && (
          <form
            onSubmit={e => {
              e.preventDefault();
              run(() => api.createLoan({ ...loanInputFrom(loanForm), asset_id: a.id }).then(() => setAddingLoan(false)));
            }}
            className="mt-2"
          >
            <LoanFields idPrefix="d-loan" value={loanForm} onChange={setLoanForm} />
            <div className="flex gap-2 mt-4">
              <button type="submit" disabled={busy} className="apple-btn-primary text-[14px]">{busy ? 'Saving…' : 'Add loan'}</button>
              <button type="button" onClick={() => setAddingLoan(false)} className="apple-btn-secondary text-[14px]">Cancel</button>
            </div>
          </form>
        )}
      </section>

      {error && <p role="alert" className="text-[13px] text-claret">{error}</p>}
      <div className="flex gap-5 text-[13px] pt-1">
        <button type="button" onClick={onEdit} className="text-forest font-medium">Edit details</button>
        <button type="button" onClick={remove} disabled={busy} className="text-claret font-medium">Delete</button>
      </div>
    </div>
  );
}
