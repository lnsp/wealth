import { useEffect, useMemo, useState } from 'react';
import { api, type Loan, type LoanScenarioResult } from '../../api/client';
import EChartWrapper from '../charts/EChartWrapper';
import { useThemeColors } from '../../hooks/useThemeColors';
import LoanFields, { loanFormFrom, loanInputFrom } from './LoanFields';
import { fmt, fmtCents, fmtPct, fmtDate, fmtMonth, today, fieldCls, labelCls, cardCls } from './format';

interface Props {
  loanId: string;
  onBack: () => void;
  onOpenAsset: (id: string) => void;
  onChanged: () => void;
  onDeleted: () => void;
}

function Pills<T>({ label, options, value, onPick }: { label: string; options: { value: T; label: string }[]; value: T; onPick: (v: T) => void }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-1 p-[3px] rounded-lg bg-parchment-deep">
      {options.map(o => (
        <button
          key={o.label}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          onClick={() => onPick(o.value)}
          className={`flex-1 min-w-[72px] text-[13px] rounded-md px-2.5 py-2 ${o.value === value ? 'bg-white dark:bg-parchment text-forest font-semibold shadow-sm' : 'text-ink-body'}`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export default function LoanDetail({ loanId, onBack, onOpenAsset, onChanged, onDeleted }: Props) {
  const tc = useThemeColors();
  const [loan, setLoan] = useState<Loan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState(() => loanFormFrom(null));
  const [extra, setExtra] = useState(0);
  const [followup, setFollowup] = useState<number | null>(null);
  const [extraScenario, setExtraScenario] = useState<{ base: LoanScenarioResult; scenario: LoanScenarioResult; interest_saved: number } | null>(null);
  const [rateScenario, setRateScenario] = useState<LoanScenarioResult | null>(null);
  const [newExtra, setNewExtra] = useState({ date: today(), amount: '' });
  const [showAllPayments, setShowAllPayments] = useState(false);

  const load = () => api.getLoan(loanId).then(l => { setLoan(l); setFollowup(f => f ?? l.followup_rate ?? l.nominal_rate); }).catch(e => setError(e.message));
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { load(); }, [loanId]);
  useEffect(() => {
    api.getLoanScenario(loanId, extra).then(setExtraScenario).catch(() => setExtraScenario(null));
  }, [loanId, extra, loan?.balance]);
  useEffect(() => {
    if (followup == null) return;
    api.getLoanScenario(loanId, 0, followup).then(r => setRateScenario(r.scenario)).catch(() => setRateScenario(null));
  }, [loanId, followup, loan?.balance]);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      await load();
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  };

  const chart = useMemo(() => {
    const rows = loan?.yearly ?? [];
    const year = new Date().getFullYear();
    return {
      tooltip: {
        trigger: 'axis' as const,
        axisPointer: { type: 'shadow' as const },
        formatter: (params: unknown) => {
          const ps = params as { dataIndex: number }[];
          if (!ps.length) return '';
          const r = rows[ps[0].dataIndex];
          return `${r.year}<br/>Interest ${fmt(r.interest)}<br/>Principal ${fmt(r.principal)}<br/>Owed at year end <strong>${fmt(r.balance)}</strong>`;
        },
      },
      legend: { data: ['Principal', 'Interest', 'Owed'], bottom: 0, icon: 'circle', itemWidth: 8, itemHeight: 8, textStyle: { fontSize: 11, color: tc.inkBody } },
      grid: { top: 16, right: 52, bottom: 46, left: 52 },
      xAxis: {
        type: 'category' as const,
        data: rows.map(r => String(r.year)),
        axisLabel: { fontSize: 10, color: tc.inkMuted, interval: rows.length > 12 ? 1 : 0, fontWeight: (v: string) => (v === String(year) ? 'bold' : 'normal') } as object,
        axisTick: { show: false },
        axisLine: { lineStyle: { color: tc.divider } },
      },
      yAxis: [
        { type: 'value' as const, axisLabel: { fontSize: 10, color: tc.inkMuted, formatter: (v: number) => `${Math.round(v / 1000)}k` }, splitLine: { lineStyle: { color: tc.divider, type: 'dashed' as const } } },
        { type: 'value' as const, min: 0, axisLabel: { fontSize: 10, color: tc.claret, formatter: (v: number) => `${Math.round(v / 1000)}k` }, splitLine: { show: false } },
      ],
      series: [
        { name: 'Principal', type: 'bar' as const, stack: 'p', data: rows.map(r => r.principal), itemStyle: { color: tc.forest }, barMaxWidth: 28 },
        { name: 'Interest', type: 'bar' as const, stack: 'p', data: rows.map(r => r.interest), itemStyle: { color: '#D8B4BB' }, barMaxWidth: 28 },
        { name: 'Owed', type: 'line' as const, yAxisIndex: 1, data: rows.map(r => r.balance), showSymbol: false, lineStyle: { width: 2, color: tc.claret }, itemStyle: { color: tc.claret } },
      ],
    };
  }, [loan, tc]);

  if (!loan) {
    return (
      <div>
        <button type="button" onClick={onBack} className="text-forest text-[13px] font-medium">← Assets</button>
        <p className="text-[13px] text-ink-muted mt-4" role={error ? 'alert' : 'status'}>{error ?? 'Loading…'}</p>
      </div>
    );
  }

  const allowance = loan.principal * loan.extra_allowance_pct;
  const extraOptions = [0, 2500, 5000, 10000].filter(v => allowance <= 0 || v <= allowance);
  if (allowance > 0 && !extraOptions.includes(Math.round(allowance))) extraOptions.push(Math.round(allowance));
  const r = loan.nominal_rate;
  const rateOptions = [Math.max(0, r - 0.01), r, r + 0.015].map(v => Math.round(v * 10000) / 10000);
  const next = loan.next_payment;
  const payments = loan.payments ?? [];
  const shownPayments = showAllPayments ? payments : payments.slice(0, 6);
  const interestShare = next ? (next.interest / (next.interest + next.principal)) * 100 : 0;

  const remove = () => {
    if (!window.confirm(`Delete ${loan.name}? Its balance is removed from net worth and its payments are untagged.`)) return;
    setBusy(true);
    api.deleteLoan(loan.id).then(onDeleted).catch(err => { setError(err.message); setBusy(false); });
  };

  return (
    <div className="space-y-4">
      <nav aria-label="Breadcrumb" className="text-[13px] flex flex-wrap gap-1.5">
        <button type="button" onClick={onBack} className="text-forest font-medium">Assets</button>
        <span aria-hidden="true" className="text-ink-muted">/</span>
        <button type="button" onClick={() => onOpenAsset(loan.asset_id)} className="text-forest font-medium">{loan.asset_name}</button>
        <span aria-hidden="true" className="text-ink-muted">/</span>
        <span className="text-ink-muted">{loan.name}</span>
      </nav>

      <section className="flex flex-wrap justify-between items-end gap-x-8 gap-y-4 pb-5 border-b border-divider">
        <div className="min-w-0">
          <p className="text-[11px] tracking-[0.08em] uppercase text-ink-muted">Annuity loan{loan.lender ? ` · ${loan.lender}` : ''}</p>
          <h2 className="font-serif text-[30px] md:text-[36px] leading-tight text-ink">{loan.name} for {loan.asset_name}</h2>
          <p className="text-[13px] text-ink-muted mt-1">{fmt(loan.principal)} paid out {fmtDate(loan.payout_date)} · payments from {fmtDate(loan.first_payment_date)}</p>
        </div>
        <div className="text-right">
          <p className="text-[12px] text-ink-muted">Still owed</p>
          <p className="font-serif text-[38px] md:text-[44px] leading-none tabular-nums text-claret">{fmt(loan.balance)}</p>
          <div role="img" aria-label={`${fmtPct(loan.repaid_pct)} repaid`} className="mt-2.5 ml-auto w-56 max-w-full h-1.5 rounded-full bg-[#EFE9E0] dark:bg-divider overflow-hidden">
            <div className="h-full bg-forest" style={{ width: `${Math.min(100, loan.repaid_pct)}%` }} />
          </div>
          <p className="text-[12px] text-ink-muted mt-1 tabular-nums">{fmt(loan.repaid)} repaid ({fmtPct(loan.repaid_pct)})</p>
        </div>
      </section>

      <dl className="grid gap-x-8 gap-y-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 180px), 1fr))' }}>
        <div>
          <dt className="text-[12px] text-ink-muted">Interest rate</dt>
          <dd className="text-[19px] font-semibold tabular-nums">{fmtPct(loan.nominal_rate * 100, 2)}{loan.fixed_until ? ' fixed' : ''}</dd>
          {loan.fixed_until && <dd className="text-[12px] text-ink-muted">until {fmtDate(loan.fixed_until)}{loan.months_left_fixed != null ? ` · ${Math.floor(loan.months_left_fixed / 12)} y ${loan.months_left_fixed % 12} m left` : ''}</dd>}
        </div>
        <div>
          <dt className="text-[12px] text-ink-muted">Monthly payment</dt>
          <dd className="text-[19px] font-semibold tabular-nums">{fmtCents(loan.monthly_payment)}</dd>
          {next && <dd className="text-[12px] text-ink-muted tabular-nums">next: {fmtCents(next.interest)} interest · {fmtCents(next.principal)} principal</dd>}
        </div>
        {loan.remaining_at_fixed_end != null && (
          <div>
            <dt className="text-[12px] text-ink-muted">Left when the fixed rate ends</dt>
            <dd className="text-[19px] font-semibold tabular-nums text-amber">{fmt(loan.remaining_at_fixed_end)}</dd>
            <dd className="text-[12px] text-ink-muted">{loan.remaining_at_fixed_end > 0 ? `needs follow-up financing in ${fmtMonth(loan.fixed_until as string)}` : 'repaid by then'}</dd>
          </div>
        )}
        <div>
          <dt className="text-[12px] text-ink-muted">Interest paid so far</dt>
          <dd className="text-[19px] font-semibold tabular-nums">{fmt(loan.interest_paid)}</dd>
          {loan.fixed_until && <dd className="text-[12px] text-ink-muted">of {fmt(loan.interest_fixed_period)} over the fixed period</dd>}
        </div>
      </dl>

      <section aria-labelledby="l-plan" className={cardCls}>
        <h3 id="l-plan" className="font-serif text-[21px] text-ink">Repayment plan{loan.fixed_until ? ` to ${new Date(loan.fixed_until).getFullYear()}` : ''}</h3>
        <EChartWrapper option={chart} height="260px" />
        <p className="text-[12px] text-ink-muted mt-1">First and last years are part years. The same payment shifts from interest to principal every month. Paid off {fmtMonth(loan.payoff_date)} if nothing changes.</p>
      </section>

      <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 340px), 1fr))' }}>
        <section aria-labelledby="l-extra" className={cardCls}>
          <h3 id="l-extra" className="font-serif text-[21px] text-ink">What if I repay extra?</h3>
          <p className="text-[12px] text-ink-muted mt-0.5 mb-3">
            {allowance > 0 ? `Allowed: ${fmtPct(loan.extra_allowance_pct * 100, 0)} = ${fmt(allowance)} per year · used in ${new Date().getFullYear()}: ${fmt(loan.extra_used_this_year)}` : 'Paid each December until the fixed rate ends'}
          </p>
          <Pills label="Extra repayment per year" value={extra} onPick={setExtra}
            options={extraOptions.map(v => ({ value: v, label: v === 0 ? 'None' : `${fmt(v)}/yr` }))} />
          {extraScenario && (
            <dl className="grid grid-cols-2 gap-x-5 gap-y-3 mt-4 tabular-nums">
              {loan.fixed_until && <div><dt className="text-[12px] text-ink-muted">Left in {fmtMonth(loan.fixed_until)}</dt><dd className="text-[17px] font-semibold">{fmt(extraScenario.scenario.remaining_at_fixed_end)}</dd></div>}
              <div><dt className="text-[12px] text-ink-muted">Paid off</dt><dd className="text-[17px] font-semibold">{fmtMonth(extraScenario.scenario.payoff_date)}</dd></div>
              <div><dt className="text-[12px] text-ink-muted">Total interest</dt><dd className="text-[17px] font-semibold">{fmt(extraScenario.scenario.total_interest)}</dd></div>
              <div><dt className="text-[12px] text-ink-muted">Saved vs. no extra</dt><dd className="text-[17px] font-semibold text-sage">{extraScenario.interest_saved > 0 ? fmt(extraScenario.interest_saved) : '—'}</dd></div>
            </dl>
          )}
          <p className="text-[12px] text-ink-muted mt-3">Each December; the rate after the fixed period stays as assumed.</p>
        </section>

        {loan.fixed_until && followup != null && (
          <section aria-labelledby="l-rate" className={cardCls}>
            <h3 id="l-rate" className="font-serif text-[21px] text-ink">What if rates change by {new Date(loan.fixed_until).getFullYear()}?</h3>
            <p className="text-[12px] text-ink-muted mt-0.5 mb-3">Follow-up rate on what's left, same monthly payment</p>
            <Pills label="Follow-up interest rate" value={followup} onPick={setFollowup}
              options={rateOptions.map(v => ({ value: v, label: fmtPct(v * 100, 2) }))} />
            {rateScenario && (
              <dl className="grid grid-cols-2 gap-x-5 gap-y-3 mt-4 tabular-nums">
                <div><dt className="text-[12px] text-ink-muted">Paid off</dt><dd className="text-[17px] font-semibold">{fmtMonth(rateScenario.payoff_date)}</dd></div>
                <div><dt className="text-[12px] text-ink-muted">Interest after {new Date(loan.fixed_until).getFullYear()}</dt><dd className="text-[17px] font-semibold">{fmt(rateScenario.interest_after_fixed)}</dd></div>
              </dl>
            )}
            <p className="text-[12px] text-ink-muted mt-3">A forward loan can lock a rate up to five years before the fixed period ends.</p>
          </section>
        )}
      </div>

      <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 340px), 1fr))' }}>
        <section aria-labelledby="l-pay" className={cardCls}>
          <div className="flex flex-wrap justify-between items-baseline gap-3 mb-2">
            <h3 id="l-pay" className="font-serif text-[21px] text-ink">Payments</h3>
            <span className={`text-[12px] font-medium ${loan.matched_count >= loan.due_count ? 'text-sage' : 'text-amber'}`}>{loan.matched_count} of {loan.due_count} matched</span>
          </div>
          {payments.length === 0 ? (
            <p className="text-[13px] text-ink-muted">No matching bank payments yet. Instalments are matched by exact amount within a week of the due date{loan.payment_account_id ? ' on the chosen account' : ''}; unmatched ones count as paid from untracked money.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-[13px] tabular-nums border-collapse min-w-[360px]">
                <thead>
                  <tr className="text-ink-muted text-left border-b border-divider">
                    <th scope="col" className="py-1.5 font-medium">Date</th>
                    <th scope="col" className="py-1.5 font-medium text-right">Paid</th>
                    <th scope="col" className="py-1.5 font-medium text-right">Interest</th>
                    <th scope="col" className="py-1.5 font-medium text-right">Principal</th>
                  </tr>
                </thead>
                <tbody>
                  {shownPayments.map(p => (
                    <tr key={p.transaction_id} className="border-b border-divider">
                      <td className="py-1.5">{fmtDate(p.date)}</td>
                      <td className="py-1.5 text-right">{fmtCents(p.amount)}</td>
                      <td className="py-1.5 text-right text-claret">{fmtCents(p.interest)}</td>
                      <td className="py-1.5 text-right text-forest">{fmtCents(p.principal)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {payments.length > 6 && (
            <button type="button" onClick={() => setShowAllPayments(v => !v)} className="text-forest text-[12px] font-medium mt-2">{showAllPayments ? 'Show fewer' : `Show all ${payments.length}`}</button>
          )}
          <button type="button" disabled={busy} onClick={() => run(() => api.matchLoanPayments())} className="block text-forest text-[12px] font-medium mt-2">Match payments again</button>
        </section>

        {next && (
          <section aria-labelledby="l-count" className={cardCls}>
            <h3 id="l-count" className="font-serif text-[21px] text-ink mb-3">How each payment counts</h3>
            <div role="img" aria-label={`Of ${fmtCents(loan.monthly_payment)}, ${fmtCents(next.interest)} interest and ${fmtCents(next.principal)} principal`} className="flex h-7 rounded-md overflow-hidden gap-0.5 text-[12px] font-semibold">
              <div className="flex items-center pl-2.5 bg-[#D8B4BB] text-ink whitespace-nowrap overflow-hidden" style={{ width: `${interestShare}%` }}>{fmt(next.interest)} interest</div>
              <div className="flex items-center pl-2.5 bg-forest text-parchment whitespace-nowrap overflow-hidden" style={{ width: `${100 - interestShare}%` }}>{fmt(next.principal)} principal</div>
            </div>
            <ul className="mt-3.5 pl-4 list-disc text-[13px] text-ink-body space-y-1.5">
              <li><strong className="text-ink">Interest</strong> is a cost: Cashflow books it under housing, and net worth goes down by it.</li>
              <li><strong className="text-ink">Principal</strong> is saving: cash turns into what's yours, net worth is unchanged.</li>
              <li>Savings rate counts the principal as saved.</li>
            </ul>
          </section>
        )}
      </div>

      <section aria-labelledby="l-extras" className={cardCls}>
        <h3 id="l-extras" className="font-serif text-[21px] text-ink mb-2">Extra repayments</h3>
        {(loan.extras ?? []).length === 0 ? (
          <p className="text-[13px] text-ink-muted mb-3">None yet.</p>
        ) : (
          <ul className="text-[13px] divide-y divide-divider/60 mb-3">
            {(loan.extras ?? []).map(x => (
              <li key={x.id} className="flex items-center justify-between gap-3 py-1.5">
                <span><span className="text-ink-muted tabular-nums">{fmtDate(x.date)}</span> · {fmt(x.amount)}{x.transaction_id ? ' · linked' : ''}</span>
                <button type="button" disabled={busy} onClick={() => run(() => api.deleteExtraRepayment(loan.id, x.id))} className="text-claret text-[12px] font-medium">Remove</button>
              </li>
            ))}
          </ul>
        )}
        <form
          onSubmit={e => { e.preventDefault(); run(() => api.createExtraRepayment(loan.id, { date: newExtra.date, amount: parseFloat(newExtra.amount) }).then(() => setNewExtra({ date: today(), amount: '' }))); }}
          className="grid gap-2 items-end max-w-xl"
          style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 140px), 1fr))' }}
        >
          <label className={labelCls}>Date<input type="date" required min={loan.payout_date} className={fieldCls} value={newExtra.date} onChange={e => setNewExtra({ ...newExtra, date: e.target.value })} /></label>
          <label className={labelCls}>Amount (€)<input type="number" min="1" required className={fieldCls} value={newExtra.amount} onChange={e => setNewExtra({ ...newExtra, amount: e.target.value })} /></label>
          <button type="submit" disabled={busy} className="apple-btn-secondary text-[13px] px-3 py-2">+ Extra repayment</button>
        </form>
      </section>

      <section aria-labelledby="l-terms" className={cardCls}>
        <div className="flex flex-wrap justify-between items-baseline gap-3 mb-3">
          <h3 id="l-terms" className="font-serif text-[21px] text-ink">Loan terms</h3>
          {!editing && <button type="button" onClick={() => { setForm(loanFormFrom(loan)); setEditing(true); }} className="text-forest text-[13px] font-medium">Edit terms</button>}
        </div>
        {editing ? (
          <form onSubmit={e => { e.preventDefault(); run(() => api.updateLoan(loan.id, loanInputFrom(form)).then(() => setEditing(false))); }}>
            <LoanFields idPrefix="l-edit" value={form} onChange={setForm} />
            <div className="flex gap-2 mt-4">
              <button type="submit" disabled={busy} className="apple-btn-primary text-[14px]">{busy ? 'Saving…' : 'Save terms'}</button>
              <button type="button" onClick={() => setEditing(false)} className="apple-btn-secondary text-[14px]">Cancel</button>
            </div>
          </form>
        ) : (
          <dl className="grid gap-x-8 gap-y-3 text-[14px] tabular-nums" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 190px), 1fr))' }}>
            <div><dt className="text-[12px] text-ink-muted">Amount</dt><dd>{fmt(loan.principal)} · paid out {fmtDate(loan.payout_date)}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Nominal rate</dt><dd>{fmtPct(loan.nominal_rate * 100, 2)}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Initial repayment</dt><dd>{fmtPct(loan.initial_repayment * 100, 2)} per year</dd></div>
            <div><dt className="text-[12px] text-ink-muted">First payment</dt><dd>{fmtDate(loan.first_payment_date)}, monthly</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Fixed until</dt><dd>{loan.fixed_until ? fmtDate(loan.fixed_until) : 'whole term'}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Rate after that</dt><dd>{loan.followup_rate != null ? fmtPct(loan.followup_rate * 100, 2) : 'assumed unchanged'}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Extra repayment allowed</dt><dd>{fmtPct(loan.extra_allowance_pct * 100, 0)} per year</dd></div>
          </dl>
        )}
      </section>

      {error && <p role="alert" className="text-[13px] text-claret">{error}</p>}
      <div className="flex gap-5 text-[13px] pt-1">
        <button type="button" onClick={remove} disabled={busy} className="text-claret font-medium">Delete loan</button>
      </div>
    </div>
  );
}
