import { useState, useEffect, useCallback, useMemo } from 'react';
import { api, type PhysicalAsset, type AssetInput, type TransactionRow } from '../api/client';
import EChartWrapper from './charts/EChartWrapper';
import { useThemeColors } from '../hooks/useThemeColors';

// Physical assets (cars). Values come from the backend's depreciation model:
// V = V_anchor · (1 − annual)^years · (1 − per_10k_km)^(km / 10,000), anchored
// on the purchase or the latest appraisal.

const fmt = (n: number) =>
  new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(n);
const fmtKm = (n: number) => `${new Intl.NumberFormat('de-DE', { maximumFractionDigits: 0 }).format(n)} km`;
const fmtPct = (n: number, digits = 1) => `${n.toLocaleString('de-DE', { minimumFractionDigits: digits, maximumFractionDigits: digits })} %`;
const fmtDate = (d: string) => new Date(d).toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' });
const today = () => new Date().toISOString().slice(0, 10);

const inputCls = 'flex-1 min-w-0 py-3 text-[16px] text-ink placeholder-ink-muted/40 bg-transparent outline-none';
const labelCls = 'w-36 shrink-0 pr-3 text-[15px] text-ink';

type Defaults = { annual_depreciation: number; km_depreciation: number; expected_km_per_year: number };

interface FormState {
  name: string;
  purchase_price: string;
  purchase_date: string;
  list_price: string;
  first_registration: string;
  purchase_odometer_km: string;
  annual_pct: string;
  km_pct: string;
  expected_km_per_year: string;
  purchase_transaction_id: string;
}

function toForm(a: PhysicalAsset | null, d: Defaults): FormState {
  return {
    name: a?.name ?? '',
    purchase_price: a ? String(a.purchase_price) : '',
    purchase_date: a?.purchase_date ?? '',
    list_price: a?.list_price != null ? String(a.list_price) : '',
    first_registration: a?.first_registration ?? '',
    purchase_odometer_km: a ? String(a.purchase_odometer_km) : '0',
    annual_pct: String(+((a?.annual_depreciation ?? d.annual_depreciation) * 100).toFixed(2)),
    km_pct: String(+((a?.km_depreciation ?? d.km_depreciation) * 100).toFixed(2)),
    expected_km_per_year: String(a?.expected_km_per_year ?? d.expected_km_per_year),
    purchase_transaction_id: a?.purchase_transaction_id ?? '',
  };
}

function fromForm(f: FormState): AssetInput {
  return {
    name: f.name.trim(),
    purchase_price: parseFloat(f.purchase_price),
    purchase_date: f.purchase_date,
    list_price: f.list_price ? parseFloat(f.list_price) : null,
    first_registration: f.first_registration || undefined,
    purchase_odometer_km: parseInt(f.purchase_odometer_km || '0', 10),
    annual_depreciation: parseFloat(f.annual_pct) / 100,
    km_depreciation: parseFloat(f.km_pct) / 100,
    expected_km_per_year: parseInt(f.expected_km_per_year, 10),
    purchase_transaction_id: f.purchase_transaction_id || undefined,
  };
}

/** Withdrawals within ±45 days of the purchase date, closest amount first. */
function usePaymentCandidates(purchaseDate: string, price: number) {
  const [candidates, setCandidates] = useState<TransactionRow[]>([]);
  useEffect(() => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(purchaseDate)) { setCandidates([]); return; }
    const d = new Date(purchaseDate);
    const from = new Date(d); from.setDate(from.getDate() - 45);
    const to = new Date(d); to.setDate(to.getDate() + 45);
    api.listTransactions(200, 0, 'withdrawal', '', from.toISOString().slice(0, 10), to.toISOString().slice(0, 10))
      .then(r => {
        const rows = (r.transactions || []).filter(t => Math.abs(t.amount) >= 1000);
        rows.sort((a, b) => price > 0
          ? Math.abs(Math.abs(a.amount) - price) - Math.abs(Math.abs(b.amount) - price)
          : Math.abs(b.amount) - Math.abs(a.amount));
        setCandidates(rows.slice(0, 15));
      })
      .catch(() => setCandidates([]));
  }, [purchaseDate, price]);
  return candidates;
}

function AssetForm({ initial, defaults, submitLabel, onSubmit, onCancel }: {
  initial: PhysicalAsset | null;
  defaults: Defaults;
  submitLabel: string;
  onSubmit: (input: AssetInput) => Promise<void>;
  onCancel?: () => void;
}) {
  const [f, setF] = useState<FormState>(() => toForm(initial, defaults));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showModel, setShowModel] = useState(false);
  const set = (k: keyof FormState) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value });
  const candidates = usePaymentCandidates(f.purchase_date, parseFloat(f.purchase_price) || 0);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await onSubmit(fromForm(f));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setBusy(false);
    }
  };

  const effective = (() => {
    const a = parseFloat(f.annual_pct) / 100, k = parseFloat(f.km_pct) / 100, km = parseInt(f.expected_km_per_year, 10);
    if ([a, k, km].some(isNaN)) return null;
    return (1 - (1 - a) * Math.pow(1 - k, km / 10000)) * 100;
  })();

  return (
    <form onSubmit={submit} className="divide-y divide-divider">
      <div className="flex items-center">
        <label htmlFor="asset-name" className={labelCls}>Name</label>
        <input id="asset-name" required value={f.name} onChange={set('name')} placeholder="e.g. ID.3 Neo 79 kWh" className={inputCls} />
      </div>
      <div className="flex items-center">
        <label htmlFor="asset-price" className={labelCls}>Purchase price</label>
        <input id="asset-price" required type="number" min="1" step="0.01" value={f.purchase_price} onChange={set('purchase_price')} placeholder="EUR" className={inputCls} />
      </div>
      <div className="flex items-center">
        <label htmlFor="asset-date" className={labelCls}>Purchase date</label>
        <input id="asset-date" required type="date" value={f.purchase_date} onChange={set('purchase_date')} className={inputCls} />
      </div>
      <div className="flex items-center">
        <label htmlFor="asset-odo" className={labelCls}>Odometer at purchase</label>
        <input id="asset-odo" type="number" min="0" step="1" value={f.purchase_odometer_km} onChange={set('purchase_odometer_km')} placeholder="km" className={inputCls} />
      </div>
      <div className="flex items-center">
        <label htmlFor="asset-list" className={labelCls}>List price</label>
        <input id="asset-list" type="number" min="0" step="0.01" value={f.list_price} onChange={set('list_price')} placeholder="Optional (UVP)" className={inputCls} />
      </div>
      <div className="flex items-center">
        <label htmlFor="asset-reg" className={labelCls}>First registration</label>
        <input id="asset-reg" type="date" value={f.first_registration} onChange={set('first_registration')} className={inputCls} />
      </div>
      <div className="flex items-center">
        <label htmlFor="asset-txn" className={labelCls}>Payment</label>
        <select id="asset-txn" value={f.purchase_transaction_id} onChange={set('purchase_transaction_id')} className="flex-1 min-w-0 py-3 text-[15px] text-ink-body bg-transparent outline-none appearance-none truncate">
          <option value="">Not linked (paid from untracked money)</option>
          {f.purchase_transaction_id && !candidates.some(c => c.id === f.purchase_transaction_id) && (
            <option value={f.purchase_transaction_id}>Linked transaction</option>
          )}
          {candidates.map(c => (
            <option key={c.id} value={c.id}>
              {fmtDate(c.date)} · {fmt(Math.abs(c.amount))} · {c.counterparty || c.reference || c.account_name}
            </option>
          ))}
        </select>
      </div>
      <p className="py-2 text-[12px] text-ink-muted">
        Linking the payment marks it as an asset purchase, so it counts as cash turning into the car rather than spending.
      </p>

      <div className="py-2">
        <button type="button" onClick={() => setShowModel(s => !s)} aria-expanded={showModel} className="text-forest text-[13px] font-medium">
          {showModel ? 'Hide' : 'Show'} depreciation model{effective != null && ` (${fmtPct(effective)}/yr at ${fmtKm(parseInt(f.expected_km_per_year, 10))}/yr)`}
        </button>
      </div>
      {showModel && (<>
        <div className="flex items-center">
          <label htmlFor="asset-annual" className={labelCls}>Loss per year of age</label>
          <input id="asset-annual" type="number" min="0" max="99" step="0.1" value={f.annual_pct} onChange={set('annual_pct')} className={inputCls} />
          <span className="text-[14px] text-ink-muted ml-1">%</span>
        </div>
        <div className="flex items-center">
          <label htmlFor="asset-km" className={labelCls}>Loss per 10,000 km</label>
          <input id="asset-km" type="number" min="0" max="99" step="0.1" value={f.km_pct} onChange={set('km_pct')} className={inputCls} />
          <span className="text-[14px] text-ink-muted ml-1">%</span>
        </div>
        <div className="flex items-center">
          <label htmlFor="asset-kmyr" className={labelCls}>Expected km / year</label>
          <input id="asset-kmyr" type="number" min="0" step="500" value={f.expected_km_per_year} onChange={set('expected_km_per_year')} className={inputCls} />
        </div>
        <p className="py-2 text-[12px] text-ink-muted">
          Defaults ({fmtPct(defaults.annual_depreciation * 100)} per year, {fmtPct(defaults.km_depreciation * 100)} per 10,000 km) come from 296 used ID.3 Pro S listings on AutoScout24 (Nov 2026).
        </p>
      </>)}

      {error && <p role="alert" className="py-2 text-[13px] text-claret">{error}</p>}
      <div className="py-3 flex gap-2">
        <button type="submit" disabled={busy} className="apple-btn-primary flex-1">{busy ? 'Saving…' : submitLabel}</button>
        {onCancel && <button type="button" onClick={onCancel} className="apple-btn-secondary">Cancel</button>}
      </div>
    </form>
  );
}

function ObservationForm({ asset, onSaved }: { asset: PhysicalAsset; onSaved: (a: PhysicalAsset) => void }) {
  const [date, setDate] = useState(today());
  const [odometer, setOdometer] = useState('');
  const [appraisal, setAppraisal] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!odometer && !appraisal) { setError('Enter an odometer reading, a market value, or both.'); return; }
    setBusy(true);
    setError(null);
    try {
      const updated = await api.upsertAssetObservation(asset.id, {
        date,
        odometer_km: odometer ? parseInt(odometer, 10) : undefined,
        appraised_value: appraisal ? parseFloat(appraisal) : undefined,
        note: note || undefined,
      });
      setOdometer(''); setAppraisal(''); setNote('');
      onSaved(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="grid grid-cols-2 md:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.4fr)_auto] gap-2 items-end">
      <label className="flex flex-col min-w-0 text-[12px] text-ink-muted gap-1">
        Date
        <input type="date" required min={asset.purchase_date} value={date} onChange={e => setDate(e.target.value)} className="w-full min-w-0 py-1.5 px-2 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink" />
      </label>
      <label className="flex flex-col min-w-0 text-[12px] text-ink-muted gap-1">
        Odometer (km)
        <input type="number" min="0" step="1" value={odometer} onChange={e => setOdometer(e.target.value)} placeholder={String(Math.round(asset.odometer_estimate_km))} className="w-full min-w-0 py-1.5 px-2 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink" />
      </label>
      <label className="flex flex-col min-w-0 text-[12px] text-ink-muted gap-1">
        Market value (EUR)
        <input type="number" min="1" step="1" value={appraisal} onChange={e => setAppraisal(e.target.value)} placeholder="Optional" className="w-full min-w-0 py-1.5 px-2 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink" />
      </label>
      <label className="flex flex-col min-w-0 text-[12px] text-ink-muted gap-1 col-span-2 md:col-span-1">
        Note
        <input type="text" value={note} onChange={e => setNote(e.target.value)} placeholder="e.g. DAT estimate" className="w-full min-w-0 py-1.5 px-2 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink" />
      </label>
      <button type="submit" disabled={busy} className="apple-btn-primary text-[14px] px-4 py-2 col-span-2 md:col-span-1">{busy ? 'Saving…' : 'Add'}</button>
      {error && <p role="alert" className="col-span-full text-[13px] text-claret">{error}</p>}
    </form>
  );
}

function AssetCard({ asset, defaults, onChanged, onDeleted }: {
  asset: PhysicalAsset;
  defaults: Defaults;
  onChanged: (a: PhysicalAsset) => void;
  onDeleted: () => void;
}) {
  const tc = useThemeColors();
  const [editing, setEditing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Split the curve into history (solid) and forecast (dashed); the seam
  // point (today) belongs to both so the lines join.
  const chartOption = useMemo(() => {
    const dates = asset.curve.map(p => p.date);
    const seam = asset.curve.findIndex(p => p.projected);
    const lastHist = seam === -1 ? asset.curve.length - 1 : seam - 1;
    const history = asset.curve.map((p, i) => (i <= lastHist ? p.value : null));
    const forecast = asset.curve.map((p, i) => (i >= Math.max(lastHist, 0) ? p.value : null));
    const appraisals = asset.observations.flatMap(o => {
      const idx = o.appraised_value == null ? -1 : dates.findIndex(d => d >= o.date);
      return idx === -1 ? [] : [{ name: `Appraisal ${o.date}`, coord: [idx, o.appraised_value as number], value: fmt(o.appraised_value as number) }];
    });
    return {
      tooltip: {
        trigger: 'axis' as const,
        formatter: (params: unknown) => {
          const ps = (params as { dataIndex: number; value: number | null; seriesName: string }[]).filter(p => p.value != null);
          if (ps.length === 0) return '';
          const pt = asset.curve[ps[0].dataIndex];
          return `${fmtDate(pt.date)}<br/>${pt.projected ? 'Forecast' : 'Modelled value'}: <strong>${fmt(pt.value)}</strong>`;
        },
      },
      grid: { top: 16, right: 12, bottom: 28, left: 52 },
      xAxis: {
        type: 'category' as const,
        data: dates.map(d => new Date(d).toLocaleDateString('de-DE', { month: 'short', year: '2-digit' })),
        axisLabel: { fontSize: 10, color: tc.inkMuted, interval: Math.max(Math.floor(dates.length / 6) - 1, 0) },
        axisLine: { lineStyle: { color: tc.divider } },
        axisTick: { show: false },
      },
      yAxis: {
        type: 'value' as const,
        min: 0,
        axisLabel: { fontSize: 10, color: tc.inkMuted, formatter: (v: number) => `${Math.round(v / 1000)}k` },
        splitLine: { lineStyle: { color: tc.divider, type: 'dashed' as const } },
      },
      series: [
        {
          name: 'Value', type: 'line' as const, data: history, showSymbol: false,
          lineStyle: { width: 2, color: tc.forest }, itemStyle: { color: tc.forest },
          areaStyle: { opacity: 0.12, color: tc.forest },
          markPoint: appraisals.length ? { symbol: 'circle', symbolSize: 8, label: { show: false }, itemStyle: { color: tc.gold }, data: appraisals } : undefined,
        },
        {
          name: 'Forecast', type: 'line' as const, data: forecast, showSymbol: false,
          lineStyle: { width: 2, type: 'dashed' as const, color: tc.inkMuted }, itemStyle: { color: tc.inkMuted },
        },
      ],
    };
  }, [asset, tc]);

  const remove = async () => {
    if (!window.confirm(`Delete ${asset.name}? Its value will be removed from your net-worth history.`)) return;
    try {
      await api.deleteAsset(asset.id);
      onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete');
    }
  };

  const removeObservation = async (date: string) => {
    try {
      onChanged(await api.deleteAssetObservation(asset.id, date));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete');
    }
  };

  const changePositive = asset.value_change >= 0;
  const inFiveYears = (() => {
    const target = new Date(asset.purchase_date);
    target.setFullYear(target.getFullYear() + 5);
    const t = target.toISOString().slice(0, 10);
    return asset.curve.find(p => p.date >= t);
  })();

  if (editing) {
    return (
      <div className="mb-6 px-1 md:px-0">
        <h3 className="text-[15px] font-medium text-ink mb-1">Edit {asset.name}</h3>
        <AssetForm
          initial={asset}
          defaults={defaults}
          submitLabel="Save"
          onCancel={() => setEditing(false)}
          onSubmit={async input => { onChanged(await api.updateAsset(asset.id, input)); setEditing(false); }}
        />
      </div>
    );
  }

  return (
    <div className="mb-8 px-1 md:px-0">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-[16px] font-medium text-ink truncate">{asset.name}</p>
          <p className="text-[12px] text-ink-muted">
            Bought {fmtDate(asset.purchase_date)} for {fmt(asset.purchase_price)}
            {asset.list_price != null && <> · list {fmt(asset.list_price)}</>}
            {asset.first_registration && <> · registered {fmtDate(asset.first_registration)}</>}
          </p>
        </div>
        <div className="text-right shrink-0">
          <p className="font-serif text-[22px] text-ink tabular-nums leading-tight">{fmt(asset.current_value)}</p>
          <p className={`text-[13px] tabular-nums ${changePositive ? 'text-sage' : 'text-claret'}`}>
            {changePositive ? '+' : ''}{fmt(asset.value_change)} ({changePositive ? '+' : ''}{fmtPct(asset.value_change_pct)})
          </p>
        </div>
      </div>

      <dl className="grid grid-cols-2 md:grid-cols-4 gap-x-4 gap-y-2 mt-4 text-[13px]">
        <div><dt className="text-ink-muted text-[12px]">Losing per month</dt><dd className="text-ink tabular-nums">{fmt(asset.monthly_depreciation)}</dd></div>
        <div><dt className="text-ink-muted text-[12px]">Effective rate</dt><dd className="text-ink tabular-nums">{fmtPct(asset.effective_annual_rate * 100)}/yr</dd></div>
        <div><dt className="text-ink-muted text-[12px]">Odometer (est.)</dt><dd className="text-ink tabular-nums">{fmtKm(asset.odometer_estimate_km)}</dd></div>
        <div>
          <dt className="text-ink-muted text-[12px]">{asset.pct_of_list != null ? 'Of list price' : 'In 5 years'}</dt>
          <dd className="text-ink tabular-nums">{asset.pct_of_list != null ? fmtPct(asset.pct_of_list, 0) : inFiveYears ? fmt(inFiveYears.value) : '—'}</dd>
        </div>
      </dl>

      <div className="mt-4">
        <EChartWrapper option={chartOption} height="200px" />
        <p className="text-[12px] text-ink-muted mt-1">
          Solid: modelled value · dashed: forecast at {fmtKm(asset.expected_km_per_year)}/yr
          {inFiveYears && <> · {fmt(inFiveYears.value)} in {new Date(inFiveYears.date).getFullYear()}</>}
          {asset.observations.some(o => o.appraised_value != null) && <> · dots: appraisals</>}
        </p>
      </div>

      <div className="mt-5">
        <h4 className="text-[13px] font-medium text-ink mb-2">Readings &amp; appraisals</h4>
        {asset.observations.length > 0 ? (
          <ul className="divide-y divide-divider mb-3">
            {asset.observations.map(o => (
              <li key={o.date} className="flex items-center justify-between gap-3 py-2 text-[13px]">
                <span className="text-ink-body tabular-nums shrink-0">{fmtDate(o.date)}</span>
                <span className="flex-1 min-w-0 text-ink truncate">
                  {[o.odometer_km != null && fmtKm(o.odometer_km), o.appraised_value != null && `valued at ${fmt(o.appraised_value)}`, o.note].filter(Boolean).join(' · ')}
                </span>
                <button onClick={() => removeObservation(o.date)} className="text-claret text-[12px] font-medium shrink-0" aria-label={`Delete observation from ${fmtDate(o.date)}`}>Remove</button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-[12px] text-ink-muted mb-3">No readings yet. The odometer is extrapolated at {fmtKm(asset.expected_km_per_year)}/yr until you add one.</p>
        )}
        <ObservationForm asset={asset} onSaved={onChanged} />
        <p className="text-[12px] text-ink-muted mt-2">A market value re-anchors the curve from that date; odometer readings adjust the per-km loss.</p>
      </div>

      {error && <p role="alert" className="mt-2 text-[13px] text-claret">{error}</p>}
      <div className="flex gap-4 mt-4">
        <button onClick={() => setEditing(true)} className="text-forest text-[13px] font-medium">Edit details</button>
        <button onClick={remove} className="text-claret text-[13px] font-medium">Delete</button>
        {asset.purchase_transaction_id == null && (
          <span className="text-[12px] text-ink-muted">Payment not linked</span>
        )}
      </div>
    </div>
  );
}

export default function AssetsPanel({ onChanged }: { onChanged?: () => void }) {
  const [assets, setAssets] = useState<PhysicalAsset[]>([]);
  const [defaults, setDefaults] = useState<Defaults>({ annual_depreciation: 0.078, km_depreciation: 0.03, expected_km_per_year: 15000 });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const load = useCallback(() => {
    setError(null);
    api.listAssets()
      .then(r => { setAssets(r.assets || []); setDefaults(r.defaults); })
      .catch(e => setError(e.message || 'Failed to load vehicles'))
      .finally(() => setLoading(false));
  }, []);
  useEffect(load, [load]);

  // Net-worth snapshots are rebuilt in the background after each change;
  // give the backfill a moment before asking the page to refetch them.
  const notify = () => { if (onChanged) setTimeout(onChanged, 2500); };

  if (loading) return null;
  const total = assets.reduce((s, a) => s + a.current_value, 0);

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <div className="flex items-center justify-between mb-3 md:mb-4 px-1 md:px-0">
        <div>
          <h2 className="font-serif text-heading text-ink">Vehicles</h2>
          <p className="text-[13px] text-ink-muted mt-0.5">Modelled market value, included in net worth</p>
        </div>
        {assets.length > 0 && <span className="font-serif text-[20px] md:text-[22px] text-ink tabular-nums">{fmt(total)}</span>}
      </div>

      {error && (
        <div className="flex items-center justify-between px-1 md:px-0 mb-3">
          <p role="alert" className="text-[13px] text-claret">{error}</p>
          <button onClick={load} className="text-forest text-[13px] font-medium">Retry</button>
        </div>
      )}

      {assets.map(a => (
        <AssetCard
          key={a.id}
          asset={a}
          defaults={defaults}
          onChanged={updated => { setAssets(as => as.map(x => (x.id === updated.id ? updated : x))); notify(); }}
          onDeleted={() => { setAssets(as => as.filter(x => x.id !== a.id)); notify(); }}
        />
      ))}

      {adding ? (
        <div className="px-1 md:px-0">
          <h3 className="text-[15px] font-medium text-ink mb-1">Add vehicle</h3>
          <AssetForm
            initial={null}
            defaults={defaults}
            submitLabel="Add vehicle"
            onCancel={() => setAdding(false)}
            onSubmit={async input => {
              const created = await api.createAsset(input);
              setAssets(as => [...as, created]);
              setAdding(false);
              notify();
            }}
          />
        </div>
      ) : (
        <div className="px-1 md:px-0">
          {assets.length === 0 && <p className="text-[13px] text-ink-muted mb-3">Track a car's depreciating value as part of your net worth.</p>}
          <button onClick={() => setAdding(true)} className="apple-btn-secondary text-[13px] px-3 py-2">+ Add vehicle</button>
        </div>
      )}
    </div>
  );
}
