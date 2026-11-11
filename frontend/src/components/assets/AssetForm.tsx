import { useEffect, useMemo, useState } from 'react';
import { api, type AssetInput, type AssetKind, type AssetsOverview, type PhysicalAsset, type TransactionRow } from '../../api/client';
import LoanFields, { loanFormFrom, loanInputFrom, annuity, type LoanFormState } from './LoanFields';
import { fmt, fmtDate, fmtPct, KIND_LABEL, isProperty, fieldCls, labelCls, cardCls } from './format';

// Grunderwerbsteuer by state (editable — rates change; check yours).
const TRANSFER_TAX: [string, number][] = [
  ['Baden-Württemberg', 5.0], ['Bayern', 3.5], ['Berlin', 6.0], ['Brandenburg', 6.5], ['Bremen', 5.0],
  ['Hamburg', 5.5], ['Hessen', 6.0], ['Mecklenburg-Vorpommern', 6.0], ['Niedersachsen', 5.0],
  ['Nordrhein-Westfalen', 6.5], ['Rheinland-Pfalz', 5.0], ['Saarland', 6.5], ['Sachsen', 5.5],
  ['Sachsen-Anhalt', 5.0], ['Schleswig-Holstein', 6.5], ['Thüringen', 5.0],
];

const KIND_HINT: Record<AssetKind, string> = {
  car: 'depreciates with age & km',
  apartment: 'land share + building',
  house: 'land + building',
  land: 'follows prices, no wear',
  other: 'art, watches … appraisals',
};

interface State {
  kind: AssetKind;
  name: string;
  details: string;
  purchase_price: string;
  purchase_costs: string;
  purchase_date: string;
  notarized_date: string;
  list_price: string;
  first_registration: string;
  purchase_odometer_km: string;
  annual_pct: string;
  km_pct: string;
  expected_km_per_year: string;
  living_area_m2: string;
  year_built: string;
  self_used: boolean;
  land_value: string;
  drift_pct: string;
  useful_life_years: string;
  transaction_ids: string[];
}

const num = (s: string) => parseFloat(String(s).replace(',', '.'));

function stateFrom(a: PhysicalAsset | null, kind: AssetKind, d: AssetsOverview['defaults']): State {
  const propertyLike = kind !== 'car' && kind !== 'other';
  return {
    kind,
    name: a?.name ?? '',
    details: a?.details ?? '',
    purchase_price: a ? String(a.purchase_price) : '',
    purchase_costs: a ? String(a.purchase_costs || '') : '',
    purchase_date: a?.purchase_date ?? '',
    notarized_date: a?.notarized_date ?? '',
    list_price: a?.list_price != null ? String(a.list_price) : '',
    first_registration: a?.first_registration ?? '',
    purchase_odometer_km: a ? String(a.purchase_odometer_km) : '0',
    annual_pct: String(+((a?.annual_depreciation ?? d.annual_depreciation) * 100).toFixed(2)),
    km_pct: String(+((a?.km_depreciation ?? d.km_depreciation) * 100).toFixed(2)),
    expected_km_per_year: String(a?.expected_km_per_year ?? d.expected_km_per_year),
    living_area_m2: a?.living_area_m2 != null ? String(a.living_area_m2) : '',
    year_built: a?.year_built != null ? String(a.year_built) : '',
    self_used: a?.self_used ?? true,
    land_value: a?.land_value != null ? String(a.land_value) : '',
    drift_pct: String(+((a ? a.price_drift : propertyLike ? d.property_drift : 0) * 100).toFixed(2)),
    useful_life_years: String(a?.useful_life_years ?? d.useful_life_years),
    transaction_ids: a ? a.linked_transactions.filter(t => t.purpose === 'purchase').map(t => t.id) : [],
  };
}

function toInput(s: State): AssetInput {
  const opt = (v: string) => (v.trim() === '' || isNaN(num(v)) ? undefined : num(v));
  return {
    kind: s.kind,
    name: s.name.trim(),
    details: s.details.trim(),
    purchase_price: num(s.purchase_price),
    purchase_costs: opt(s.purchase_costs) ?? 0,
    purchase_date: s.purchase_date,
    notarized_date: s.notarized_date || undefined,
    list_price: opt(s.list_price) ?? null,
    first_registration: s.first_registration || undefined,
    purchase_odometer_km: Math.round(opt(s.purchase_odometer_km) ?? 0),
    annual_depreciation: num(s.annual_pct) / 100,
    km_depreciation: num(s.km_pct) / 100,
    expected_km_per_year: Math.round(num(s.expected_km_per_year)),
    living_area_m2: opt(s.living_area_m2),
    year_built: opt(s.year_built),
    self_used: s.self_used,
    land_value: opt(s.land_value),
    price_drift: num(s.drift_pct) / 100,
    useful_life_years: Math.round(num(s.useful_life_years)),
    transaction_ids: s.transaction_ids,
  };
}

/** Withdrawals around the purchase date that could be the payments for it. */
function usePaymentCandidates(date: string, selected: string[]) {
  const [rows, setRows] = useState<TransactionRow[]>([]);
  useEffect(() => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) { setRows([]); return; }
    const d = new Date(date);
    const from = new Date(d); from.setDate(from.getDate() - 90);
    const to = new Date(d); to.setDate(to.getDate() + 45);
    api.listTransactions(300, 0, 'withdrawal', '', from.toISOString().slice(0, 10), to.toISOString().slice(0, 10))
      .then(r => {
        const big = (r.transactions || []).filter(t => Math.abs(t.amount) >= 500 || selected.includes(t.id));
        big.sort((a, b) => Math.abs(b.amount) - Math.abs(a.amount));
        setRows(big.slice(0, 20));
      })
      .catch(() => setRows([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [date]);
  return rows;
}

function Section({ id, title, hint, children }: { id: string; title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section aria-labelledby={id} className={cardCls}>
      <h3 id={id} className="font-serif text-[21px] text-ink">{title}</h3>
      {hint && <p className="text-[12px] text-ink-muted mt-0.5">{hint}</p>}
      <div className="mt-3.5">{children}</div>
    </section>
  );
}

const grid = { gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 190px), 1fr))' };

export default function AssetForm({ asset, defaults, onSaved, onCancel }: {
  asset: PhysicalAsset | null; // null = create
  defaults: AssetsOverview['defaults'];
  onSaved: (a: PhysicalAsset) => void;
  onCancel: () => void;
}) {
  const [s, setS] = useState<State>(() => stateFrom(asset, asset?.kind ?? 'apartment', defaults));
  const [withLoan, setWithLoan] = useState(false);
  const [loan, setLoan] = useState<LoanFormState>(() => loanFormFrom(null));
  const [costCalc, setCostCalc] = useState({ state: 'Sachsen', tax: '5.5', notary: '2.0', broker: '3.57' });
  const [land, setLand] = useState({ area: '', share: '', denom: '1000', brw: '' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const creating = asset === null;
  const kind = s.kind;
  const property = isProperty(kind);
  const registered = property || kind === 'land';
  const set = (k: keyof State) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
    setS({ ...s, [k]: e.target.value });
  const candidates = usePaymentCandidates(s.purchase_date, s.transaction_ids);

  const pickKind = (k: AssetKind) => {
    setS(prev => ({ ...stateFrom(null, k, defaults), name: prev.name, purchase_price: prev.purchase_price, purchase_date: prev.purchase_date }));
    setWithLoan(isProperty(k));
  };

  // Loan payout defaults to the handover date.
  useEffect(() => {
    if (withLoan && !loan.payout_date && s.purchase_date) setLoan(l => ({ ...l, payout_date: s.purchase_date }));
  }, [withLoan, s.purchase_date, loan.payout_date]);

  const price = num(s.purchase_price) || 0;
  const costs = num(s.purchase_costs) || 0;
  const loanAmount = withLoan ? num(loan.principal) || 0 : 0;
  const ownFunds = price + costs - loanAmount;
  const linkedSum = candidates.filter(c => s.transaction_ids.includes(c.id)).reduce((t, c) => t + Math.abs(c.amount), 0);

  const computedCosts = useMemo(() => {
    const pct = (num(costCalc.tax) || 0) + (num(costCalc.notary) || 0) + (num(costCalc.broker) || 0);
    return Math.round(price * pct) / 100;
  }, [costCalc, price]);
  const computedLand = useMemo(() => {
    const area = num(land.area), share = num(land.share), denom = num(land.denom), brw = num(land.brw);
    if ([area, share, denom, brw].some(isNaN) || denom <= 0) return null;
    return Math.round(area * (share / denom) * brw);
  }, [land]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const input = toInput(s);
      if (creating && withLoan) input.loan = loanInputFrom(loan);
      const saved = creating ? await api.createAsset(input) : await api.updateAsset(asset.id, input);
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setBusy(false);
    }
  };

  const toggleTxn = (id: string) =>
    setS(prev => ({ ...prev, transaction_ids: prev.transaction_ids.includes(id) ? prev.transaction_ids.filter(x => x !== id) : [...prev.transaction_ids, id] }));

  return (
    <div>
      <button type="button" onClick={onCancel} className="text-forest text-[13px] font-medium">← Assets</button>
      <h2 className="font-serif text-[30px] md:text-[34px] text-ink mt-2 mb-4">{creating ? 'Add asset' : `Edit ${asset.name}`}</h2>

      {creating && (
        <div role="radiogroup" aria-label="Asset type" className="grid gap-2.5 mb-5" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 140px), 1fr))' }}>
          {(Object.keys(KIND_LABEL) as AssetKind[]).map(k => (
            <button
              key={k}
              type="button"
              role="radio"
              aria-checked={k === kind}
              onClick={() => pickKind(k)}
              className={`text-left px-4 py-3 rounded-xl bg-white dark:bg-parchment-deep ${k === kind ? 'border-2 border-forest' : 'border border-divider'}`}
            >
              <span className="block text-[15px] font-semibold text-ink">{KIND_LABEL[k]}</span>
              <span className="block text-[12px] text-ink-muted mt-0.5">{KIND_HINT[k]}</span>
            </button>
          ))}
        </div>
      )}

      <form onSubmit={submit} className="flex flex-wrap gap-5 items-start">
        <div className="flex flex-col gap-4 min-w-0" style={{ flex: '999 1 520px' }}>
          <Section id="f-basics" title="Basics">
            <div className="grid gap-x-5 gap-y-3.5" style={grid}>
              <label className={labelCls} htmlFor="f-name">Name
                <input id="f-name" className={fieldCls} required value={s.name} onChange={set('name')}
                  placeholder={kind === 'car' ? 'e.g. ID.3 Neo 79 kWh' : kind === 'apartment' ? 'e.g. Wohnung Leipzig' : ''} />
              </label>
              <label className={labelCls} htmlFor="f-details">Details (optional)
                <input id="f-details" className={fieldCls} value={s.details} onChange={set('details')} placeholder={property ? 'e.g. 3 rooms, 2nd floor' : ''} />
              </label>
              {property && (<>
                <label className={labelCls} htmlFor="f-area">Living area (m²)
                  <input id="f-area" className={fieldCls} type="number" min="1" step="0.1" value={s.living_area_m2} onChange={set('living_area_m2')} />
                </label>
                <label className={labelCls} htmlFor="f-year">Year built
                  <input id="f-year" className={fieldCls} type="number" min="1000" max="2200" required value={s.year_built} onChange={set('year_built')} />
                </label>
                <fieldset className="flex flex-col gap-1.5 text-[12px] text-ink-muted">
                  <legend className="mb-1">Use</legend>
                  <label className="flex items-center gap-2 text-[15px] text-ink"><input type="radio" name="use" checked={s.self_used} onChange={() => setS({ ...s, self_used: true })} />Self-used</label>
                  <label className="flex items-center gap-2 text-[15px] text-ink"><input type="radio" name="use" checked={!s.self_used} onChange={() => setS({ ...s, self_used: false })} />Rented out</label>
                </fieldset>
              </>)}
              {kind === 'car' && (<>
                <label className={labelCls} htmlFor="f-odo">Odometer at purchase (km)
                  <input id="f-odo" className={fieldCls} type="number" min="0" step="1" value={s.purchase_odometer_km} onChange={set('purchase_odometer_km')} />
                </label>
                <label className={labelCls} htmlFor="f-reg">First registration
                  <input id="f-reg" className={fieldCls} type="date" value={s.first_registration} onChange={set('first_registration')} />
                </label>
                <label className={labelCls} htmlFor="f-list">List price (optional, €)
                  <input id="f-list" className={fieldCls} type="number" min="0" step="0.01" value={s.list_price} onChange={set('list_price')} />
                </label>
              </>)}
            </div>
          </Section>

          <Section id="f-purchase" title="Purchase">
            <div className="grid gap-x-5 gap-y-3.5" style={grid}>
              <label className={labelCls} htmlFor="f-price">Purchase price (€)
                <input id="f-price" className={fieldCls} type="number" min="1" step="0.01" required value={s.purchase_price} onChange={set('purchase_price')} />
              </label>
              {registered && (
                <label className={labelCls} htmlFor="f-notar">Notarised (starts the 10-year tax period)
                  <input id="f-notar" className={fieldCls} type="date" value={s.notarized_date} onChange={set('notarized_date')} />
                </label>
              )}
              <label className={labelCls} htmlFor="f-date">{registered ? 'Handover (counts in net worth from)' : 'Purchase date'}
                <input id="f-date" className={fieldCls} type="date" required value={s.purchase_date} onChange={set('purchase_date')} />
              </label>
              <label className={labelCls} htmlFor="f-costs">{registered ? 'Purchase costs (€)' : 'Extra costs, e.g. registration (€)'}
                <input id="f-costs" className={fieldCls} type="number" min="0" step="0.01" value={s.purchase_costs} onChange={set('purchase_costs')} placeholder="0" />
              </label>
            </div>
            {registered && (
              <div className="mt-3.5 rounded-lg bg-parchment-deep p-3">
                <p className="text-[12px] text-ink-muted mb-2">Calculate purchase costs</p>
                <div className="grid gap-x-4 gap-y-2.5 items-end" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 130px), 1fr))' }}>
                  <label className={labelCls} htmlFor="f-state">State
                    <select id="f-state" className={fieldCls} value={costCalc.state}
                      onChange={e => { const rate = TRANSFER_TAX.find(([n]) => n === e.target.value)?.[1] ?? 5; setCostCalc({ ...costCalc, state: e.target.value, tax: String(rate) }); }}>
                      {TRANSFER_TAX.map(([n]) => <option key={n} value={n}>{n}</option>)}
                    </select>
                  </label>
                  <label className={labelCls} htmlFor="f-tax">Transfer tax %
                    <input id="f-tax" className={fieldCls} type="number" min="0" step="0.1" value={costCalc.tax} onChange={e => setCostCalc({ ...costCalc, tax: e.target.value })} />
                  </label>
                  <label className={labelCls} htmlFor="f-notary">Notary &amp; registry %
                    <input id="f-notary" className={fieldCls} type="number" min="0" step="0.1" value={costCalc.notary} onChange={e => setCostCalc({ ...costCalc, notary: e.target.value })} />
                  </label>
                  <label className={labelCls} htmlFor="f-broker">Broker %
                    <input id="f-broker" className={fieldCls} type="number" min="0" step="0.01" value={costCalc.broker} onChange={e => setCostCalc({ ...costCalc, broker: e.target.value })} />
                  </label>
                  <button type="button" disabled={price <= 0} onClick={() => setS({ ...s, purchase_costs: String(computedCosts) })}
                    className="apple-btn-secondary text-[13px] px-3 py-2">Use {fmt(computedCosts)}</button>
                </div>
              </div>
            )}
            <fieldset className="mt-4">
              <legend className="text-[12px] text-ink-muted mb-1.5">Payments for it (own funds) — linked payments count as cash turning into the asset, not spending</legend>
              {candidates.length === 0 ? (
                <p className="text-[13px] text-ink-muted">{s.purchase_date ? 'No withdrawals ≥ 500 € around this date.' : 'Enter the date to see matching withdrawals.'}</p>
              ) : (
                <ul className="divide-y divide-divider border border-divider rounded-lg max-h-56 overflow-y-auto">
                  {candidates.map(c => (
                    <li key={c.id}>
                      <label className="flex items-center gap-3 px-3 py-2 text-[13px] cursor-pointer">
                        <input type="checkbox" checked={s.transaction_ids.includes(c.id)} onChange={() => toggleTxn(c.id)} />
                        <span className="tabular-nums text-ink-muted shrink-0">{fmtDate(c.date)}</span>
                        <span className="flex-1 min-w-0 truncate text-ink">{c.counterparty || c.reference || c.account_name}</span>
                        <span className="tabular-nums font-medium shrink-0">{fmt(Math.abs(c.amount))}</span>
                      </label>
                    </li>
                  ))}
                </ul>
              )}
              {s.transaction_ids.length > 0 && Math.abs(linkedSum - ownFunds) > 1 && price > 0 && (
                <p className="text-[12px] text-amber mt-1.5">
                  Linked {fmt(linkedSum)}, expected own funds {fmt(ownFunds)} (price + costs − loan). The difference counts as money from untracked accounts.
                </p>
              )}
            </fieldset>
          </Section>

          {property && (
            <Section id="f-land" title="Land value" hint="Land follows prices; only the building ages. The Bodenrichtwert is on your state's BORIS portal.">
              <div className="grid gap-x-4 gap-y-3 items-end" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 160px), 1fr))' }}>
                <label className={labelCls} htmlFor="f-plot">Plot area (m²)
                  <input id="f-plot" className={fieldCls} type="number" min="0" value={land.area} onChange={e => setLand({ ...land, area: e.target.value })} />
                </label>
                <label className={labelCls} htmlFor="f-share">Your share
                  <span className="flex items-center gap-1">
                    <input id="f-share" className={fieldCls} type="number" min="0" value={land.share} onChange={e => setLand({ ...land, share: e.target.value })} placeholder={kind === 'house' ? '1' : '85'} aria-label="Share numerator" />
                    <span aria-hidden="true">/</span>
                    <input className={fieldCls} type="number" min="1" value={land.denom} onChange={e => setLand({ ...land, denom: e.target.value })} aria-label="Share denominator" />
                  </span>
                </label>
                <label className={labelCls} htmlFor="f-brw">Bodenrichtwert (€/m²)
                  <input id="f-brw" className={fieldCls} type="number" min="0" value={land.brw} onChange={e => setLand({ ...land, brw: e.target.value })} />
                </label>
                <button type="button" disabled={computedLand == null} onClick={() => setS({ ...s, land_value: String(computedLand) })}
                  className="apple-btn-secondary text-[13px] px-3 py-2">{computedLand != null ? `Use ${fmt(computedLand)}` : 'Calculate'}</button>
              </div>
              <label className={`${labelCls} mt-3.5 max-w-xs`} htmlFor="f-landval">Land value (€) — defaults to 25 % of the price
                <input id="f-landval" className={fieldCls} type="number" min="0" step="1" value={s.land_value} onChange={set('land_value')} placeholder={price ? String(Math.round(price * 0.25)) : ''} />
              </label>
              {price > 0 && (
                <p className="text-[13px] text-ink-body mt-2 tabular-nums">
                  = {fmt(num(s.land_value) || price * 0.25)} land · {fmt(price - (num(s.land_value) || price * 0.25))} building
                </p>
              )}
            </Section>
          )}

          {creating && (
            <Section id="f-loan" title="Financing">
              <label className="flex items-center gap-2 text-[14px] text-ink mb-3">
                <input type="checkbox" checked={withLoan} onChange={e => setWithLoan(e.target.checked)} className="w-[18px] h-[18px] accent-forest" />
                Financed with a loan
              </label>
              {withLoan && <LoanFields idPrefix="f-loanf" value={loan} onChange={setLoan} />}
            </Section>
          )}

          <Section id="f-val" title="Valuation">
            {kind === 'car' ? (
              <div className="grid gap-x-5 gap-y-3.5" style={grid}>
                <label className={labelCls} htmlFor="f-annual">Loss per year of age (%)
                  <input id="f-annual" className={fieldCls} type="number" min="0" max="99" step="0.1" value={s.annual_pct} onChange={set('annual_pct')} />
                </label>
                <label className={labelCls} htmlFor="f-km">Loss per 10,000 km (%)
                  <input id="f-km" className={fieldCls} type="number" min="0" max="99" step="0.1" value={s.km_pct} onChange={set('km_pct')} />
                </label>
                <label className={labelCls} htmlFor="f-kmyr">Expected km / year
                  <input id="f-kmyr" className={fieldCls} type="number" min="0" step="500" value={s.expected_km_per_year} onChange={set('expected_km_per_year')} />
                </label>
              </div>
            ) : (
              <div className="grid gap-x-5 gap-y-3.5" style={grid}>
                <label className={labelCls} htmlFor="f-drift">Price change per year (%) — 0 = appraisals only
                  <input id="f-drift" className={fieldCls} type="number" min="-49" max="49" step="0.1" value={s.drift_pct} onChange={set('drift_pct')} />
                </label>
                {property && (
                  <label className={labelCls} htmlFor="f-life">Building useful life (years)
                    <input id="f-life" className={fieldCls} type="number" min="1" max="200" step="1" value={s.useful_life_years} onChange={set('useful_life_years')} />
                  </label>
                )}
              </div>
            )}
            <p className="text-[12px] text-ink-muted mt-2.5">
              {kind === 'car'
                ? 'Defaults come from 296 used ID.3 Pro S listings (AutoScout24, Nov 2026).'
                : property
                  ? 'The building loses value in a straight line over its useful life (80 years is standard for houses and apartments); both parts follow the price change. Appraisals re-anchor the value.'
                  : 'Appraisals re-anchor the value.'}
            </p>
          </Section>
        </div>

        <aside aria-labelledby="f-sum" className="rounded-xl bg-parchment-deep p-4 md:p-5 min-w-0" style={{ flex: '1 1 280px' }}>
          <h3 id="f-sum" className="font-serif text-[21px] text-ink">On {s.purchase_date ? fmtDate(s.purchase_date) : 'purchase'}</h3>
          <p className="text-[12px] text-ink-muted mb-3">What this does to your net worth</p>
          <table className="w-full text-[14px] tabular-nums border-collapse">
            <tbody>
              <tr className="border-b border-divider"><td className="py-2 text-ink-body">Market value</td><td className="py-2 text-right">+{fmt(price)}</td></tr>
              <tr className="border-b border-divider"><td className="py-2 text-ink-body">Own funds spent</td><td className="py-2 text-right text-claret">−{fmt(Math.max(0, ownFunds))}</td></tr>
              {withLoan && <tr className="border-b border-divider"><td className="py-2 text-ink-body">New debt</td><td className="py-2 text-right text-claret">−{fmt(loanAmount)}</td></tr>}
              <tr><td className="pt-2.5 font-semibold">Net worth change</td><td className="pt-2.5 text-right font-semibold text-claret">{costs > 0 ? `−${fmt(costs)}` : fmt(0)}</td></tr>
            </tbody>
          </table>
          <p className="text-[12px] text-ink-muted mt-3">
            {costs > 0 ? `Own funds include ${fmt(costs)} of purchase costs; that part is lost, the rest turns into the asset.` : 'Cash turns into the asset. From then on its value change shows as “Physical assets”.'}
            {withLoan && price > 0 && ` Yours at handover: ${fmt(price - loanAmount)} (${fmtPct(((price - loanAmount) / price) * 100, 0)}).`}
          </p>
          {withLoan && annuity(loan) != null && !loan.monthly_payment && (
            <p className="text-[12px] text-ink-muted mt-1.5">Monthly payment {fmt(annuity(loan) as number)}.</p>
          )}
          {error && <p role="alert" className="text-[13px] text-claret mt-3">{error}</p>}
          <div className="flex gap-2 mt-4">
            <button type="submit" disabled={busy} className="apple-btn-primary flex-1">{busy ? 'Saving…' : creating ? `Add ${KIND_LABEL[kind].toLowerCase()}` : 'Save'}</button>
            <button type="button" onClick={onCancel} className="apple-btn-secondary">Cancel</button>
          </div>
        </aside>
      </form>
    </div>
  );
}
