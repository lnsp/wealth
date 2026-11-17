import { useMemo, useState } from 'react';
import { api, type Loan, type PhysicalAsset } from '../../api/client';
import EChartWrapper from '../charts/EChartWrapper';
import { useThemeColors } from '../../hooks/useThemeColors';
import LoanFields, { loanFormFrom, loanInputFrom } from './LoanFields';
import { AddChip, Field, MetricCard, MetricGrid, ProgressBar, useConfirm } from '../ui';
import { fmtEUR, fmtPct, fmtDate, fmtDateLong, fmtMonth, fmtMonthShort, fmtNum, fmtAxis } from '../../utils/format';
import { fmtKm, today, KIND_LABEL, isProperty, cardCls, cardTitleCls, linkBtnCls, formGrid } from './format';

interface Props {
  asset: PhysicalAsset;
  loans: Loan[];
  onBack: () => void;
  onEdit: () => void;
  onOpenLoan: (id: string) => void;
  onChanged: () => void; // reload everything
  onDeleted: () => void;
}

export default function AssetDetail({ asset: a, loans, onBack, onEdit, onOpenLoan, onChanged, onDeleted }: Props) {
  const tc = useThemeColors();
  const [confirm, confirmDialog] = useConfirm();
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
    const labels = pts.map(p => fmtMonthShort(p.date));
    const appraisals = a.observations.flatMap(o => {
      const i = o.appraised_value == null ? -1 : pts.findIndex(p => p.date >= o.date);
      return i < 0 ? [] : [{ name: `Appraisal ${fmtDate(o.date)}`, coord: [i, o.appraised_value as number] as [number, number] }];
    });
    // Stacked bands start at zero: what is owed, and on top of it what is yours. The ink
    // line is the value; each series carries its colour so the legend matches.
    const series: object[] = [];
    if (hasLoan) {
      series.push(
        { name: 'Owed', type: 'line', stack: 'v', color: tc.claret, data: pts.map(p => Math.min(p.owed, p.value)), showSymbol: false, lineStyle: { width: 0 }, areaStyle: { color: tc.claret, opacity: 0.35 } },
        { name: 'Yours', type: 'line', stack: 'v', color: tc.forest, data: pts.map(p => Math.max(0, p.value - p.owed)), showSymbol: false, lineStyle: { width: 0 }, areaStyle: { color: tc.forest, opacity: 0.35 } },
      );
    }
    series.push({
      name: 'Value', type: 'line', color: hasLoan ? tc.walnut : tc.forest, data: pts.map(p => p.value), showSymbol: false,
      lineStyle: { width: 2 },
      markPoint: appraisals.length ? { symbol: 'circle', symbolSize: 9, label: { show: false }, itemStyle: { color: tc.gold }, data: appraisals } : undefined,
      markArea: seam > 0 ? { silent: true, itemStyle: { color: tc.inkMuted, opacity: 0.07 }, label: { show: true, position: 'insideTopLeft', color: tc.inkMuted, fontSize: 11, formatter: 'Forecast' }, data: [[{ xAxis: seam - 1 }, { xAxis: pts.length - 1 }]] } : undefined,
    });
    return {
      tooltip: {
        trigger: 'axis' as const,
        formatter: (params: unknown) => {
          const ps = params as { dataIndex: number }[];
          if (!ps.length) return '';
          const p = pts[ps[0].dataIndex];
          let html = `${fmtDate(p.date)}${p.projected ? ' · forecast' : ''}<br/>Worth <strong>${fmtEUR(p.value)}</strong>`;
          if (hasLoan) html += `<br/>Owed ${fmtEUR(p.owed)}<br/>Yours <strong>${fmtEUR(p.value - p.owed)}</strong>`;
          return html;
        },
      },
      legend: hasLoan ? { data: ['Value', 'Yours', 'Owed'], bottom: 0 } : undefined,
      grid: { top: 16, right: 12, bottom: hasLoan ? 46 : 28, left: 52 },
      xAxis: { type: 'category' as const, data: labels, boundaryGap: false },
      yAxis: { type: 'value' as const, min: 0, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
      series,
    };
  }, [a, hasLoan, tc]);

  const last = a.curve[a.curve.length - 1];
  const remove = async () => {
    const ok = await confirm({
      title: `Delete ${a.name}${hasLoan ? ' and its loan' : ''}?`,
      body: 'It will be removed from your net-worth history. This cannot be undone.',
      confirmLabel: 'Delete asset',
    });
    if (!ok) return;
    setBusy(true);
    api.deleteAsset(a.id).then(onDeleted).catch(err => { setError(err.message); setBusy(false); });
  };
  const removeEntry = async (what: string, fn: () => Promise<unknown>) => {
    if (await confirm({ title: `Remove ${what}?`, body: 'The value curve is recalculated without it.', confirmLabel: 'Remove' })) run(fn);
  };
  const landPct = a.property && a.current_value > 0 ? (a.property.land_value_now / a.current_value) * 100 : 0;
  const tone = (n: number) => (n > 0 ? 'text-sage' : n < 0 ? 'text-claret' : 'text-ink');

  return (
    <div className="space-y-5 mt-2">
      <button type="button" onClick={onBack} className={linkBtnCls}>← Assets</button>

      <section className="flex flex-wrap justify-between items-end gap-x-8 gap-y-4 pb-5 border-b border-divider">
        <div className="min-w-0">
          <p className="label-caps">{KIND_LABEL[a.kind]}{property ? (a.self_used ? ' · self-used' : ' · rented out') : ''}</p>
          <h2 className="font-serif text-[26px] md:text-[30px] leading-tight text-ink break-words">{a.name}</h2>
          <p className="text-[13px] text-ink-muted mt-1">
            {[a.details, a.living_area_m2 ? `${fmtNum(a.living_area_m2, a.living_area_m2 % 1 === 0 ? 0 : 1)} m²` : '', a.year_built ? `built ${a.year_built}` : '',
              a.first_registration ? `registered ${fmtDate(a.first_registration)}` : '', `bought ${fmtDate(a.purchase_date)}`].filter(Boolean).join(' · ')}
          </p>
        </div>
        <div className="md:text-right">
          <p className="label-caps">Worth</p>
          <p className="font-serif text-[36px] md:text-[40px] leading-none text-ink mt-1" data-privacy="blur">{fmtEUR(a.current_value)}</p>
          <p className={`text-[13px] mt-1.5 ${tone(a.value_change)}`}><span data-privacy="blur">{fmtEUR(a.value_change, { sign: true })}</span> ({fmtPct(a.value_change_pct, { sign: true })}) vs. purchase price</p>
          {a.cost_basis > a.purchase_price && (
            <p className="text-[13px] text-ink-muted"><span data-privacy="blur">{fmtEUR(a.vs_cost_basis, { sign: true })}</span> vs. everything paid</p>
          )}
        </div>
      </section>

      <MetricGrid>
        <MetricCard label="Yours" value={fmtEUR(a.yours)}
          sublabel={hasLoan ? <>{fmtPct(a.yours_pct, { digits: 0 })} · <span data-privacy="blur">{fmtEUR(a.owed)}</span> owed</> : 'Nothing owed'} />
        <MetricCard label="Value change" value={`${fmtEUR(a.monthly_change, { sign: true })} / month`}
          tone={a.monthly_change > 0 ? 'positive' : a.monthly_change < 0 ? 'negative' : 'neutral'}
          sublabel={a.property
            ? <span data-privacy="blur">{`Prices ${fmtEUR(a.property.price_effect_per_year, { sign: true })} / year${a.property.has_building_aging ? `, ageing ${fmtEUR(a.property.aging_effect_per_year, { sign: true })} / year` : ''}`}</span>
            : a.car ? `Loses ${fmtPct(a.car.effective_annual_rate * 100)} / year at ${fmtKm(a.expected_km_per_year)} / year` : undefined} />
        {a.index && (
          <MetricCard money={false} label="House price index" value={fmtNum(a.index.latest_value, 1)}
            sublabel={`${a.index.latest_quarter}${a.index.year_over_year != null ? ` · ${fmtPct(a.index.year_over_year * 100, { sign: true })} in a year` : ''} · Destatis via Eurostat`} />
        )}
        {a.car && <MetricCard money={false} label="Odometer (estimate)" value={fmtKm(a.car.odometer_estimate_km)} sublabel={a.pct_of_list != null ? `${fmtPct(a.pct_of_list, { digits: 0 })} of list price` : undefined} />}
        {hasLoan && a.current_value > 0 && <MetricCard money={false} label="Loan-to-value" value={fmtPct((a.owed / a.current_value) * 100, { digits: 0 })} sublabel="Owed as a share of the value" />}
        <MetricCard money={false} label="Tax on a sale" value={a.tax_free_from ? (a.tax_free_from <= today() ? 'None' : `Until ${fmtDate(a.tax_free_from)}`) : 'None'} sublabel={a.tax_note} />
      </MetricGrid>

      <section aria-labelledby="d-chart" className={cardCls}>
        <h3 id="d-chart" className={cardTitleCls}>{hasLoan ? 'Worth, owed and yours' : 'Value over time'}</h3>
        {a.curve.length > 1 ? (
          <EChartWrapper option={chart} height="260px" />
        ) : (
          <p className="text-[13px] text-ink-muted py-3">The value curve appears once there is more than one valuation date.</p>
        )}
        <p className="text-[12px] text-ink-muted mt-1">
          {a.car
            ? `Forecast at ${fmtKm(a.expected_km_per_year)} / year.`
            : a.index
              ? `Prices follow the house price index (Destatis via Eurostat) up to ${a.index.latest_quarter}, then ${fmtPct(a.price_drift * 100, { sign: true })} / year${a.property?.has_building_aging ? '; the building ages in a straight line' : ''}${hasLoan ? '; the loan continues as agreed' : ''}.`
              : `Forecast: prices ${fmtPct(a.price_drift * 100, { sign: true })} / year${a.property?.has_building_aging ? ', building ages in a straight line' : ''}${hasLoan ? ', loan continues as agreed' : ''}.`}
          {last && <> In {new Date(last.date).getFullYear()}: worth <span data-privacy="blur">{fmtEUR(last.value)}</span>{hasLoan ? <>, owed <span data-privacy="blur">{fmtEUR(last.owed)}</span></> : ''}.</>}
        </p>
      </section>

      <div className="grid gap-4" style={formGrid(340)}>
        {a.property && a.property.has_building_aging && (
          <section aria-labelledby="d-split" className={cardCls}>
            <h3 id="d-split" className={`${cardTitleCls} mb-3`}>Land and building</h3>
            <ProgressBar value={landPct} label="Land as a share of the value" className="h-2.5" />
            <p className="text-[12px] text-ink-muted mt-1.5">Land {fmtPct(landPct, { digits: 0 })} · building {fmtPct(100 - landPct, { digits: 0 })}</p>
            <div className="grid grid-cols-2 gap-4 mt-3.5">
              <div>
                <p className="label-caps">Land</p>
                <p className="font-serif text-[20px] leading-7 text-ink" data-privacy="blur">{fmtEUR(a.property.land_value_now)}</p>
                <p className="text-[12px] text-ink-muted">Follows prices, never ages.</p>
              </div>
              <div>
                <p className="label-caps">Building</p>
                <p className="font-serif text-[20px] leading-7 text-ink" data-privacy="blur">{fmtEUR(a.property.building_value_now)}</p>
                <p className="text-[12px] text-ink-muted">{fmtNum(a.property.remaining_life_years, a.property.remaining_life_years % 1 === 0 ? 0 : 1)} of {a.useful_life_years} years left.</p>
              </div>
            </div>
          </section>
        )}

        <section aria-labelledby="d-cost" className={cardCls}>
          <h3 id="d-cost" className={`${cardTitleCls} mb-2`}>Purchase &amp; cost basis</h3>
          <table className="w-full text-[14px] border-collapse">
            <tbody>
              <tr className="border-b border-divider"><th scope="row" className="py-2 text-left font-normal text-ink-body">Purchase price</th><td className="py-2 text-right text-ink" data-privacy="blur">{fmtEUR(a.purchase_price)}</td></tr>
              {a.purchase_costs > 0 && <tr className="border-b border-divider"><th scope="row" className="py-2 text-left font-normal text-ink-body">Purchase costs</th><td className="py-2 text-right text-ink" data-privacy="blur">{fmtEUR(a.purchase_costs)}</td></tr>}
              {a.improvements.length > 0 && <tr className="border-b border-divider"><th scope="row" className="py-2 text-left font-normal text-ink-body">Improvements</th><td className="py-2 text-right text-ink" data-privacy="blur">{fmtEUR(a.improvements.reduce((s, i) => s + i.amount, 0))}</td></tr>}
              <tr><th scope="row" className="pt-2.5 text-left font-semibold text-ink">Cost basis</th><td className="pt-2.5 text-right font-semibold text-ink" data-privacy="blur">{fmtEUR(a.cost_basis)}</td></tr>
            </tbody>
          </table>
          {a.purchase_costs > 0 && <p className="text-[12px] text-ink-muted mt-2.5">Purchase costs lowered net worth on {fmtDateLong(a.purchase_date)} (“Purchase costs” in attribution); the cost basis is kept for tax.</p>}
          {a.notarized_date && <p className="text-[12px] text-ink-muted mt-1">Notarised {fmtDateLong(a.notarized_date)}.</p>}
          <div className="mt-4">
            <h4 className="label-caps mb-1">Linked payments</h4>
            {a.linked_transactions.length === 0 ? (
              <p className="text-[13px] text-ink-muted">None — the purchase counts as money from untracked accounts. <button type="button" onClick={onEdit} className={linkBtnCls}>Link payments</button></p>
            ) : (
              <ul className="text-[13px] divide-y divide-divider">
                {a.linked_transactions.map(t => (
                  <li key={t.id} className="flex justify-between gap-3 py-2">
                    <span className="min-w-0 truncate text-ink-body"><span className="text-ink-muted">{fmtDate(t.date)}</span> · <span data-privacy="blur">{t.counterparty || (t.purpose === 'improvement' ? 'Improvement' : 'Payment')}</span></span>
                    <span className="shrink-0 text-ink" data-privacy="blur">{fmtEUR(Math.abs(t.amount), { cents: true })}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>

        <section aria-labelledby="d-obs" className={cardCls}>
          <h3 id="d-obs" className={`${cardTitleCls} mb-2`}>{a.car ? 'Readings & appraisals' : 'Appraisals & improvements'}</h3>
          <ul className="text-[13px] divide-y divide-divider mb-4">
            <li className="flex justify-between gap-3 py-2"><span className="text-ink-body"><span className="text-ink-muted">{fmtDate(a.purchase_date)}</span> · Purchase</span><span className="text-ink" data-privacy="blur">{fmtEUR(a.purchase_price)}</span></li>
            {a.observations.map(o => (
              <li key={`o-${o.date}`} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 py-2">
                <span className="min-w-0 text-ink-body"><span className="text-ink-muted">{fmtDate(o.date)}</span> · {[o.odometer_km != null && fmtKm(o.odometer_km), o.appraised_value != null && `valued at ${fmtEUR(o.appraised_value)}`, o.note].filter(Boolean).join(' · ')}</span>
                <button type="button" disabled={busy} onClick={() => removeEntry(`the entry from ${fmtDate(o.date)}`, () => api.deleteAssetObservation(a.id, o.date))} className="apple-btn-danger apple-btn-sm shrink-0" aria-label={`Remove entry from ${fmtDate(o.date)}…`}>Remove…</button>
              </li>
            ))}
            {a.improvements.map(i => (
              <li key={`i-${i.id}`} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 py-2">
                <span className="min-w-0 text-ink-body"><span className="text-ink-muted">{fmtDate(i.date)}</span> · {i.description || 'Improvement'} · <span data-privacy="blur">{fmtEUR(i.amount)}</span></span>
                <button type="button" disabled={busy} onClick={() => removeEntry(`the improvement from ${fmtDate(i.date)}`, () => api.deleteAssetImprovement(a.id, i.id))} className="apple-btn-danger apple-btn-sm shrink-0" aria-label={`Remove improvement from ${fmtDate(i.date)}…`}>Remove…</button>
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
            className="grid gap-3 items-end"
            style={formGrid(140)}
          >
            <Field label="Date" type="date" required min={a.purchase_date} value={obs.date} onChange={e => setObs({ ...obs, date: e.target.value })} />
            {a.car && <Field label="Odometer" unit="km" type="number" inputMode="numeric" min="0" value={obs.odometer} onChange={e => setObs({ ...obs, odometer: e.target.value })} />}
            <Field label={a.car ? 'Market value (optional)' : 'Market value'} unit="€" type="number" inputMode="decimal" min="1" value={obs.appraisal} onChange={e => setObs({ ...obs, appraisal: e.target.value })} />
            <Field label="Note (optional)" value={obs.note} onChange={e => setObs({ ...obs, note: e.target.value })} />
            <button type="submit" disabled={busy} className="apple-btn-primary min-h-[44px]">{a.car ? 'Add reading' : 'Add appraisal'}</button>
          </form>
          <p className="text-[12px] text-ink-muted mt-2">A market value re-anchors the curve from that date{a.car ? '; odometer readings adjust the per-km loss' : ''}.</p>
          {a.kind !== 'car' && (
            <form
              onSubmit={e => {
                e.preventDefault();
                run(() => api.createAssetImprovement(a.id, { date: imp.date, amount: parseFloat(imp.amount), description: imp.description })
                  .then(() => setImp({ date: today(), amount: '', description: '' })));
              }}
              className="grid gap-3 items-end mt-4 pt-4 border-t border-divider"
              style={formGrid(140)}
            >
              <Field label="Improvement date" type="date" required min={a.purchase_date} value={imp.date} onChange={e => setImp({ ...imp, date: e.target.value })} />
              <Field label="Amount" unit="€" type="number" inputMode="decimal" min="1" required value={imp.amount} onChange={e => setImp({ ...imp, amount: e.target.value })} />
              <Field label="What was done" value={imp.description} onChange={e => setImp({ ...imp, description: e.target.value })} />
              <button type="submit" disabled={busy} className="apple-btn-secondary min-h-[44px]">Add improvement</button>
            </form>
          )}
        </section>
      </div>

      <section aria-labelledby="d-fin" className={cardCls}>
        <div className="flex flex-wrap justify-between items-center gap-3 mb-3">
          <h3 id="d-fin" className={cardTitleCls}>Financing</h3>
          {!addingLoan && <AddChip onClick={() => setAddingLoan(true)}>Add loan</AddChip>}
        </div>
        {loans.length === 0 && !addingLoan && <p className="text-[13px] text-ink-muted">No loan — {a.kind === 'car' ? 'paid in cash' : 'fully owned'}.</p>}
        <div className="space-y-2">
          {loans.map(l => (
            <button key={l.id} type="button" onClick={() => onOpenLoan(l.id)} className="w-full text-left flex flex-wrap justify-between items-center gap-4 p-3 min-h-[44px] rounded-apple border border-divider hover:border-forest transition-colors">
              <span className="min-w-0">
                <span className="block text-[15px] font-semibold text-ink">{l.name}{l.lender ? ` · ${l.lender}` : ''}</span>
                <span className="block text-[12px] text-ink-muted">{fmtPct(l.nominal_rate * 100, { digits: 2 })}{l.fixed_until ? ` fixed until ${fmtMonth(l.fixed_until)}` : ''} · {l.matched_count} of {l.due_count} payments matched</span>
              </span>
              <span className="flex gap-6">
                <span><span className="block text-[12px] text-ink-muted">Owed</span><span className={`text-[15px] font-semibold ${l.balance > 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">{fmtEUR(l.balance)}</span></span>
                <span><span className="block text-[12px] text-ink-muted">Monthly</span><span className="text-[15px] font-semibold text-ink" data-privacy="blur">{fmtEUR(l.monthly_payment, { cents: true })}</span></span>
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
            className="mt-3"
          >
            <LoanFields idPrefix="d-loan" value={loanForm} onChange={setLoanForm} />
            <div className="flex flex-wrap gap-2 mt-4">
              <button type="submit" disabled={busy} className="apple-btn-primary min-h-[44px]">{busy ? 'Saving…' : 'Add loan'}</button>
              <button type="button" onClick={() => setAddingLoan(false)} className="apple-btn-secondary min-h-[44px]">Cancel</button>
            </div>
          </form>
        )}
      </section>

      {error && <p role="alert" className="text-[13px] text-claret">{error}</p>}
      <div className="flex flex-wrap gap-2.5 pt-1">
        <button type="button" onClick={onEdit} className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">Edit details</button>
        <button type="button" onClick={remove} disabled={busy} className="apple-btn-danger min-h-[44px] md:min-h-[40px]">Delete asset…</button>
      </div>
      {confirmDialog}
    </div>
  );
}
