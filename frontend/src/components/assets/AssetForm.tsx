import { useEffect, useMemo, useState } from 'react';
import { api, type AssetInput, type AssetKind, type AssetsOverview, type PhysicalAsset, type TransactionRow } from '../../api/client';
import LoanFields, { loanFormFrom, loanInputFrom, annuity, type LoanFormState } from './LoanFields';
import { Field, SelectField } from '../ui';
import { fmtEUR, fmtDate, fmtDateLong, fmtNum, fmtPct } from '../../utils/format';
import { KIND_LABEL, isProperty, cardCls, cardTitleCls, linkBtnCls, formGrid } from './format';

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
  price_index: string;
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
    price_index: a ? a.price_index : isProperty(kind) ? d.price_index : '',
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
    price_index: isProperty(s.kind) ? s.price_index : '',
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

function FormCard({ id, title, hint, children }: { id: string; title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section aria-labelledby={id} className={cardCls}>
      <h3 id={id} className={cardTitleCls}>{title}</h3>
      {hint && <p className="text-[13px] text-ink-muted mt-0.5">{hint}</p>}
      <div className="mt-4">{children}</div>
    </section>
  );
}

const grid = formGrid(190);
const legendCls = 'text-[13px] font-medium text-ink mb-1.5';
const choiceCls = 'flex items-start gap-2.5 min-h-[44px] md:min-h-[32px] py-1 text-[14px] text-ink cursor-pointer';
const tickCls = 'mt-0.5 w-[18px] h-[18px] shrink-0 accent-forest';

export default function AssetForm({ asset, defaults, indexes, onSaved, onCancel }: {
  asset: PhysicalAsset | null; // null = create
  defaults: AssetsOverview['defaults'];
  indexes: AssetsOverview['price_indexes'];
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
    <div className="mt-2">
      <button type="button" onClick={onCancel} className={linkBtnCls}>← Assets</button>
      <h2 className="font-serif text-[26px] md:text-[30px] leading-tight text-ink mt-1 mb-4">{creating ? 'Add asset' : `Edit ${asset.name}`}</h2>

      {creating && (
        <div role="radiogroup" aria-label="Asset type" className="grid gap-2.5 mb-5" style={formGrid(140)}>
          {(Object.keys(KIND_LABEL) as AssetKind[]).map(k => (
            <button
              key={k}
              type="button"
              role="radio"
              aria-checked={k === kind}
              onClick={() => pickKind(k)}
              className={`text-left px-4 py-3 min-h-[44px] rounded-apple bg-parchment border ${k === kind ? 'border-forest outline outline-1 outline-forest' : 'border-divider hover:border-field'}`}
            >
              <span className="flex items-center gap-2 text-[15px] font-semibold text-ink">
                {KIND_LABEL[k]}
                {k === kind && <span className="text-[12px] font-medium text-forest">Selected</span>}
              </span>
              <span className="block text-[12px] text-ink-muted mt-0.5">{KIND_HINT[k]}</span>
            </button>
          ))}
        </div>
      )}

      <form onSubmit={submit} className="flex flex-wrap gap-5 items-start">
        <div className="flex flex-col gap-4 min-w-0" style={{ flex: '999 1 520px' }}>
          <FormCard id="f-basics" title="Basics">
            <div className="grid gap-x-5 gap-y-4" style={grid}>
              <Field id="f-name" label="Name" required value={s.name} onChange={set('name')}
                hint={kind === 'car' ? 'Model and trim' : kind === 'apartment' ? 'For example the city or street' : undefined} />
              <Field id="f-details" label="Details (optional)" value={s.details} onChange={set('details')} hint={property ? 'For example rooms and floor' : undefined} />
              {property && (<>
                <Field id="f-area" label="Living area" unit="m²" type="number" inputMode="decimal" min="1" step="0.1" value={s.living_area_m2} onChange={set('living_area_m2')} />
                <Field id="f-year" label="Year built" type="number" inputMode="numeric" min="1000" max="2200" required value={s.year_built} onChange={set('year_built')} />
                <fieldset className="flex flex-col min-w-0">
                  <legend className={legendCls}>Use</legend>
                  <label className={choiceCls}><input type="radio" name="use" className={tickCls} checked={s.self_used} onChange={() => setS({ ...s, self_used: true })} />Self-used</label>
                  <label className={choiceCls}><input type="radio" name="use" className={tickCls} checked={!s.self_used} onChange={() => setS({ ...s, self_used: false })} />Rented out</label>
                </fieldset>
              </>)}
              {kind === 'car' && (<>
                <Field id="f-odo" label="Odometer at purchase" unit="km" type="number" inputMode="numeric" min="0" step="1" value={s.purchase_odometer_km} onChange={set('purchase_odometer_km')} />
                <Field id="f-reg" label="First registration" type="date" value={s.first_registration} onChange={set('first_registration')} />
                <Field id="f-list" label="List price (optional)" unit="€" type="number" inputMode="decimal" min="0" step="0.01" value={s.list_price} onChange={set('list_price')} />
              </>)}
            </div>
          </FormCard>

          <FormCard id="f-purchase" title="Purchase">
            <div className="grid gap-x-5 gap-y-4" style={grid}>
              <Field id="f-price" label="Purchase price" unit="€" type="number" inputMode="decimal" min="1" step="0.01" required value={s.purchase_price} onChange={set('purchase_price')} />
              {registered && (
                <Field id="f-notar" label="Notarised on" type="date" value={s.notarized_date} onChange={set('notarized_date')} hint="Starts the 10-year tax period" />
              )}
              <Field id="f-date" label={registered ? 'Handover date' : 'Purchase date'} type="date" required value={s.purchase_date} onChange={set('purchase_date')}
                hint={registered ? 'Counts in net worth from this date' : undefined} />
              <Field id="f-costs" label={registered ? 'Purchase costs' : 'Extra costs'} unit="€" type="number" inputMode="decimal" min="0" step="0.01" value={s.purchase_costs} onChange={set('purchase_costs')}
                hint={registered ? 'Tax, notary, broker' : 'For example registration'} />
            </div>
            {registered && (
              <div className="mt-4 rounded-apple border border-divider bg-inset p-3.5">
                <p className="text-[13px] font-semibold text-ink mb-2.5">Calculate purchase costs</p>
                <div className="grid gap-x-4 gap-y-3 items-end" style={formGrid(150)}>
                  <SelectField id="f-state" label="State" value={costCalc.state}
                    onChange={e => { const rate = TRANSFER_TAX.find(([n]) => n === e.target.value)?.[1] ?? 5; setCostCalc({ ...costCalc, state: e.target.value, tax: String(rate) }); }}>
                    {TRANSFER_TAX.map(([n]) => <option key={n} value={n}>{n}</option>)}
                  </SelectField>
                  <Field id="f-tax" label="Transfer tax" unit="%" type="number" inputMode="decimal" min="0" step="0.1" value={costCalc.tax} onChange={e => setCostCalc({ ...costCalc, tax: e.target.value })} />
                  <Field id="f-notary" label="Notary and registry" unit="%" type="number" inputMode="decimal" min="0" step="0.1" value={costCalc.notary} onChange={e => setCostCalc({ ...costCalc, notary: e.target.value })} />
                  <Field id="f-broker" label="Broker" unit="%" type="number" inputMode="decimal" min="0" step="0.01" value={costCalc.broker} onChange={e => setCostCalc({ ...costCalc, broker: e.target.value })} />
                  <button type="button" disabled={price <= 0} onClick={() => setS({ ...s, purchase_costs: String(computedCosts) })}
                    className="apple-btn-secondary min-h-[44px]">Use <span data-privacy="blur" className="ml-1">{fmtEUR(computedCosts)}</span></button>
                </div>
              </div>
            )}
            <fieldset className="mt-5 min-w-0">
              <legend className={legendCls}>Payments for it (own funds)</legend>
              <p className="text-[12px] text-ink-muted mb-2">Linked payments count as cash turning into the asset, not as spending.</p>
              {candidates.length === 0 ? (
                <p className="text-[13px] text-ink-muted">{s.purchase_date ? 'No withdrawals of 500 € or more around this date.' : 'Enter the date to see matching withdrawals.'}</p>
              ) : (
                <ul className="divide-y divide-divider border border-divider rounded-apple max-h-64 overflow-y-auto">
                  {candidates.map(c => (
                    <li key={c.id}>
                      <label className="flex items-center gap-3 px-3 min-h-[44px] py-2 text-[13px] cursor-pointer">
                        <input type="checkbox" className="w-[18px] h-[18px] shrink-0 accent-forest" checked={s.transaction_ids.includes(c.id)} onChange={() => toggleTxn(c.id)} />
                        <span className="text-ink-muted shrink-0">{fmtDate(c.date)}</span>
                        <span className="flex-1 min-w-0 truncate text-ink" data-privacy="blur" title={c.counterparty || c.reference || c.account_name}>{c.counterparty || c.reference || c.account_name}</span>
                        <span className="font-medium shrink-0" data-privacy="blur">{fmtEUR(Math.abs(c.amount), { cents: true })}</span>
                      </label>
                    </li>
                  ))}
                </ul>
              )}
              {s.transaction_ids.length > 0 && Math.abs(linkedSum - ownFunds) > 1 && price > 0 && (
                <p className="text-[12px] text-ink-body mt-2">
                  <span className="apple-chip border-amber text-amber mr-1.5">Check</span>
                  Linked <span data-privacy="blur">{fmtEUR(linkedSum)}</span>, expected own funds <span data-privacy="blur">{fmtEUR(ownFunds)}</span> (price + costs − loan). The difference counts as money from untracked accounts.
                </p>
              )}
            </fieldset>
          </FormCard>

          {property && (
            <FormCard id="f-land" title="Land value" hint="Land follows prices; only the building ages. The Bodenrichtwert (official standard land value) is on your state's BORIS portal.">
              <div className="grid gap-x-4 gap-y-3 items-end" style={formGrid(160)}>
                <Field id="f-plot" label="Plot area" unit="m²" type="number" inputMode="decimal" min="0" value={land.area} onChange={e => setLand({ ...land, area: e.target.value })} />
                <Field id="f-share" label="Your share (parts)" type="number" inputMode="numeric" min="0" value={land.share} onChange={e => setLand({ ...land, share: e.target.value })} />
                <Field id="f-share-of" label="Out of (total parts)" type="number" inputMode="numeric" min="1" value={land.denom} onChange={e => setLand({ ...land, denom: e.target.value })} />
                <Field id="f-brw" label="Bodenrichtwert" unit="€ / m²" type="number" inputMode="decimal" min="0" value={land.brw} onChange={e => setLand({ ...land, brw: e.target.value })} />
                <button type="button" disabled={computedLand == null} onClick={() => setS({ ...s, land_value: String(computedLand) })}
                  className="apple-btn-secondary min-h-[44px]">{computedLand != null ? <>Use <span data-privacy="blur" className="ml-1">{fmtEUR(computedLand)}</span></> : 'Calculate'}</button>
              </div>
              <Field id="f-landval" className="mt-4 max-w-xs" label="Land value" unit="€" type="number" inputMode="decimal" min="0" step="1" value={s.land_value} onChange={set('land_value')}
                hint="Leave empty to assume 25 % of the price" />
              {price > 0 && (
                <p className="text-[13px] text-ink-body mt-2" data-privacy="blur">
                  = {fmtEUR(num(s.land_value) || price * 0.25)} land · {fmtEUR(price - (num(s.land_value) || price * 0.25))} building
                </p>
              )}
            </FormCard>
          )}

          {creating && (
            <FormCard id="f-loan" title="Financing">
              <label className={`${choiceCls} items-center mb-3`}>
                <input type="checkbox" checked={withLoan} onChange={e => setWithLoan(e.target.checked)} className="w-[18px] h-[18px] shrink-0 accent-forest" />
                Financed with a loan
              </label>
              {withLoan && <LoanFields idPrefix="f-loanf" value={loan} onChange={setLoan} />}
            </FormCard>
          )}

          <FormCard id="f-val" title="Valuation">
            {kind === 'car' ? (
              <div className="grid gap-x-5 gap-y-4" style={grid}>
                <Field id="f-annual" label="Loss per year of age" unit="%" type="number" inputMode="decimal" min="0" max="99" step="0.1" value={s.annual_pct} onChange={set('annual_pct')} />
                <Field id="f-km" label="Loss per 10.000 km" unit="%" type="number" inputMode="decimal" min="0" max="99" step="0.1" value={s.km_pct} onChange={set('km_pct')} />
                <Field id="f-kmyr" label="Expected distance" unit="km / year" type="number" inputMode="numeric" min="0" step="500" value={s.expected_km_per_year} onChange={set('expected_km_per_year')} />
              </div>
            ) : (
              <>
              {property && (
                <fieldset className="flex flex-col mb-4 min-w-0">
                  <legend className={legendCls}>How prices move</legend>
                  {indexes.map(ix => (
                    <label key={ix.id} className={choiceCls}>
                      <input type="radio" name="price-model" className={tickCls} checked={s.price_index === ix.id} onChange={() => setS({ ...s, price_index: ix.id })} />
                      <span>
                        Follow the {ix.label.charAt(0).toLowerCase() + ix.label.slice(1)}
                        <span className="block text-[12px] text-ink-muted">
                          Quarterly, latest {ix.latest_quarter}: {fmtNum(ix.latest_value, 1)}
                          {ix.year_over_year != null && ` (${fmtPct(ix.year_over_year * 100, { sign: true })} in a year)`}
                        </span>
                      </span>
                    </label>
                  ))}
                  {indexes.length === 0 && s.price_index !== '' && (
                    <p className="text-[12px] text-ink-body py-1"><span className="apple-chip border-amber text-amber mr-1.5">Note</span>The house price index hasn't been loaded yet; the fixed change below applies until it is.</p>
                  )}
                  <label className={choiceCls}>
                    <input type="radio" name="price-model" className={tickCls} checked={s.price_index === ''} onChange={() => setS({ ...s, price_index: '' })} />
                    <span>Fixed yearly change<span className="block text-[12px] text-ink-muted">0 % = value only changes with appraisals and building age</span></span>
                  </label>
                </fieldset>
              )}
              <div className="grid gap-x-5 gap-y-4" style={grid}>
                <Field id="f-drift" label={property && s.price_index ? 'Change after the last published quarter' : 'Price change'} unit="% / year"
                  type="number" inputMode="decimal" min="-49" max="49" step="0.1" value={s.drift_pct} onChange={set('drift_pct')}
                  hint={property && s.price_index ? undefined : '0 = appraisals only'} />
                {property && (
                  <Field id="f-life" label="Building useful life" unit="years" type="number" inputMode="numeric" min="1" max="200" step="1" value={s.useful_life_years} onChange={set('useful_life_years')} />
                )}
              </div>
              </>
            )}
            <p className="text-[12px] text-ink-muted mt-3">
              {kind === 'car'
                ? 'Defaults come from 296 used ID.3 Pro S listings (AutoScout24, Nov 2026).'
                : property
                  ? `The building loses value in a straight line over its useful life (80 years is standard for houses and apartments); land and building follow ${s.price_index ? 'the index (it holds building age constant, so the two don’t double-count)' : 'the yearly change'}. Appraisals re-anchor the value.`
                  : 'Appraisals re-anchor the value.'}
            </p>
          </FormCard>
        </div>

        <aside aria-labelledby="f-sum" className="rounded-apple border border-divider bg-inset p-4 md:p-5 min-w-0" style={{ flex: '1 1 280px' }}>
          <h3 id="f-sum" className={cardTitleCls}>{s.purchase_date ? `On ${fmtDateLong(s.purchase_date)}` : 'On purchase'}</h3>
          <p className="text-[13px] text-ink-muted mb-3">What this does to your net worth</p>
          <table className="w-full text-[14px] border-collapse">
            <tbody>
              <tr className="border-b border-divider"><th scope="row" className="py-2 text-left font-normal text-ink-body">Market value</th><td className="py-2 text-right text-ink" data-privacy="blur">{fmtEUR(price, { sign: true })}</td></tr>
              <tr className="border-b border-divider"><th scope="row" className="py-2 text-left font-normal text-ink-body">Own funds spent</th><td className={`py-2 text-right ${ownFunds > 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">{fmtEUR(-Math.max(0, ownFunds))}</td></tr>
              {withLoan && <tr className="border-b border-divider"><th scope="row" className="py-2 text-left font-normal text-ink-body">New debt</th><td className={`py-2 text-right ${loanAmount > 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">{fmtEUR(-loanAmount)}</td></tr>}
              <tr><th scope="row" className="pt-2.5 text-left font-semibold text-ink">Net worth change</th><td className={`pt-2.5 text-right font-semibold ${costs > 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">{fmtEUR(costs > 0 ? -costs : 0)}</td></tr>
            </tbody>
          </table>
          <p className="text-[12px] text-ink-muted mt-3">
            {costs > 0 ? <>Own funds include <span data-privacy="blur">{fmtEUR(costs)}</span> of purchase costs; that part is lost, the rest turns into the asset.</> : 'Cash turns into the asset. From then on its value change shows as “Property and vehicles”.'}
            {withLoan && price > 0 && <> Yours at handover: <span data-privacy="blur">{fmtEUR(price - loanAmount)}</span> ({fmtPct(((price - loanAmount) / price) * 100, { digits: 0 })}).</>}
          </p>
          {withLoan && annuity(loan) != null && !loan.monthly_payment && (
            <p className="text-[12px] text-ink-muted mt-1.5">Monthly payment <span data-privacy="blur">{fmtEUR(annuity(loan) as number, { cents: true })}</span>.</p>
          )}
          {error && <p role="alert" className="text-[13px] text-claret mt-3">{error}</p>}
          <div className="flex flex-wrap gap-2 mt-4">
            <button type="submit" disabled={busy} className="apple-btn-primary flex-1 min-h-[44px]">{busy ? 'Saving…' : creating ? `Add ${KIND_LABEL[kind].toLowerCase()}` : 'Save'}</button>
            <button type="button" onClick={onCancel} className="apple-btn-secondary min-h-[44px]">Cancel</button>
          </div>
        </aside>
      </form>
    </div>
  );
}
