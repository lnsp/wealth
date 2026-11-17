import { useEffect, useMemo, useState } from 'react';
import { api, type Loan, type LoanScenarioResult } from '../../api/client';
import EChartWrapper from '../charts/EChartWrapper';
import { useThemeColors } from '../../hooks/useThemeColors';
import LoanFields, { loanFormFrom, loanInputFrom } from './LoanFields';
import { Field, MetricCard, MetricGrid, ProgressBar, LoadingState, ErrorState, useConfirm } from '../ui';
import { fmtEUR, fmtPct, fmtDate, fmtDateLong, fmtMonth, fmtAxis } from '../../utils/format';
import { today, cardCls, cardTitleCls, linkBtnCls, formGrid } from './format';

interface Props {
  loanId: string;
  onBack: () => void;
  onOpenAsset: (id: string) => void;
  onChanged: () => void;
  onDeleted: () => void;
}

function Pills<T>({ label, options, value, onPick }: { label: string; options: { value: T; label: string }[]; value: T; onPick: (v: T) => void }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-1.5">
      {options.map(o => {
        const active = o.value === value;
        return (
          <button
            key={o.label}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onPick(o.value)}
            className={`min-h-[44px] md:min-h-[36px] min-w-[64px] px-3 rounded-[4px] border text-[13px] transition-colors ${
              active ? 'bg-forest border-forest text-parchment font-semibold' : 'border-field text-ink-body font-medium hover:border-forest hover:text-ink'
            }`}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-[12px] text-ink-muted">{label}</dt>
      <dd className="text-[16px] font-semibold text-ink">{children}</dd>
    </div>
  );
}

export default function LoanDetail({ loanId, onBack, onOpenAsset, onChanged, onDeleted }: Props) {
  const tc = useThemeColors();
  const [confirm, confirmDialog] = useConfirm();
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
    return {
      tooltip: {
        trigger: 'axis' as const,
        axisPointer: { type: 'shadow' as const },
        formatter: (params: unknown) => {
          const ps = params as { dataIndex: number }[];
          if (!ps.length) return '';
          const r = rows[ps[0].dataIndex];
          return `${r.year}<br/>Interest ${fmtEUR(r.interest)}<br/>Principal ${fmtEUR(r.principal)}<br/>Owed at year end <strong>${fmtEUR(r.balance)}</strong>`;
        },
      },
      legend: { data: ['Principal', 'Interest', 'Owed at year end'], bottom: 0 },
      grid: { top: 30, right: 52, bottom: 46, left: 52 },
      xAxis: { type: 'category' as const, data: rows.map(r => String(r.year)) },
      yAxis: [
        { type: 'value' as const, min: 0, name: 'Paid per year', nameTextStyle: { align: 'left' as const }, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
        { type: 'value' as const, min: 0, name: 'Owed', nameTextStyle: { align: 'right' as const }, axisLabel: { formatter: (v: number) => fmtAxis(v) }, splitLine: { show: false } },
      ],
      series: [
        { name: 'Principal', type: 'bar' as const, stack: 'p', color: tc.forest, data: rows.map(r => r.principal), barMaxWidth: 28 },
        { name: 'Interest', type: 'bar' as const, stack: 'p', color: tc.claret, data: rows.map(r => r.interest), barMaxWidth: 28 },
        { name: 'Owed at year end', type: 'line' as const, yAxisIndex: 1, color: tc.ink, data: rows.map(r => r.balance), showSymbol: false, lineStyle: { width: 2 } },
      ],
    };
  }, [loan, tc]);

  if (!loan) {
    return (
      <div className="mt-2">
        <button type="button" onClick={onBack} className={linkBtnCls}>← Assets</button>
        {error ? <ErrorState message="This loan couldn’t be loaded." details={error} onRetry={() => { setError(null); load(); }} /> : <LoadingState message="Loading loan" />}
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
  const cents = { cents: true };

  const remove = async () => {
    const ok = await confirm({
      title: `Delete ${loan.name}?`,
      body: 'Its balance is removed from net worth and its payments are untagged. This cannot be undone.',
      confirmLabel: 'Delete loan',
    });
    if (!ok) return;
    setBusy(true);
    api.deleteLoan(loan.id).then(onDeleted).catch(err => { setError(err.message); setBusy(false); });
  };
  const removeExtra = async (id: string, date: string) => {
    if (await confirm({ title: `Remove the extra repayment from ${fmtDate(date)}?`, body: 'The repayment plan is recalculated without it.', confirmLabel: 'Remove' })) {
      run(() => api.deleteExtraRepayment(loan.id, id));
    }
  };

  return (
    <div className="space-y-5 mt-2">
      <nav aria-label="Breadcrumb" className="text-[13px] flex flex-wrap items-center gap-x-1.5">
        <button type="button" onClick={onBack} className={linkBtnCls}>Assets</button>
        <span aria-hidden="true" className="text-ink-muted">/</span>
        <button type="button" onClick={() => onOpenAsset(loan.asset_id)} className={linkBtnCls}>{loan.asset_name}</button>
        <span aria-hidden="true" className="text-ink-muted">/</span>
        <span className="text-ink-muted" aria-current="page">{loan.name}</span>
      </nav>

      <section className="flex flex-wrap justify-between items-end gap-x-8 gap-y-4 pb-5 border-b border-divider">
        <div className="min-w-0">
          <p className="label-caps">Annuity loan{loan.lender ? ` · ${loan.lender}` : ''}</p>
          <h2 className="font-serif text-[26px] md:text-[30px] leading-tight text-ink break-words">{loan.name} for {loan.asset_name}</h2>
          <p className="text-[13px] text-ink-muted mt-1"><span data-privacy="blur">{fmtEUR(loan.principal)}</span> paid out {fmtDateLong(loan.payout_date)} · payments from {fmtDateLong(loan.first_payment_date)}</p>
        </div>
        <div className="md:text-right w-full md:w-auto">
          <p className="label-caps">Still owed</p>
          <p className={`font-serif text-[36px] md:text-[40px] leading-none mt-1 ${loan.balance > 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">{fmtEUR(loan.balance)}</p>
          <ProgressBar value={Math.min(100, loan.repaid_pct)} label="Share of the loan repaid" className="mt-2.5 md:ml-auto w-56 max-w-full h-1.5" />
          <p className="text-[12px] text-ink-muted mt-1"><span data-privacy="blur">{fmtEUR(loan.repaid)}</span> repaid ({fmtPct(loan.repaid_pct)})</p>
        </div>
      </section>

      <MetricGrid>
        <MetricCard money={false} label="Interest rate" value={`${fmtPct(loan.nominal_rate * 100, { digits: 2 })}${loan.fixed_until ? ' fixed' : ''}`}
          sublabel={loan.fixed_until ? `Until ${fmtDate(loan.fixed_until)}${loan.months_left_fixed != null ? ` · ${Math.floor(loan.months_left_fixed / 12)} y ${loan.months_left_fixed % 12} m left` : ''}` : undefined} />
        <MetricCard label="Monthly payment" value={fmtEUR(loan.monthly_payment, cents)}
          sublabel={next ? <span data-privacy="blur">Next: {fmtEUR(next.interest, cents)} interest · {fmtEUR(next.principal, cents)} principal</span> : undefined} />
        {loan.remaining_at_fixed_end != null && (
          <MetricCard label="Left when the fixed rate ends" value={fmtEUR(loan.remaining_at_fixed_end)}
            sublabel={loan.remaining_at_fixed_end > 0 ? `Needs follow-up financing in ${fmtMonth(loan.fixed_until as string)}` : 'Repaid by then'} />
        )}
        <MetricCard label="Interest paid so far" value={fmtEUR(loan.interest_paid)}
          sublabel={loan.fixed_until ? <>of <span data-privacy="blur">{fmtEUR(loan.interest_fixed_period)}</span> over the fixed period</> : undefined} />
      </MetricGrid>

      <section aria-labelledby="l-plan" className={cardCls}>
        <h3 id="l-plan" className={cardTitleCls}>Repayment plan{loan.fixed_until ? ` to ${new Date(loan.fixed_until).getFullYear()}` : ''}</h3>
        {(loan.yearly ?? []).length > 0 ? (
          <EChartWrapper option={chart} height="280px" />
        ) : (
          <p className="text-[13px] text-ink-muted py-3">No repayment plan available for this loan yet.</p>
        )}
        <p className="text-[12px] text-ink-muted mt-1">First and last years are part years. The same payment shifts from interest to principal every month. Paid off {fmtMonth(loan.payoff_date)} if nothing changes.</p>
      </section>

      <div className="grid gap-4" style={formGrid(340)}>
        <section aria-labelledby="l-extra" className={cardCls}>
          <h3 id="l-extra" className={cardTitleCls}>What if I repay extra?</h3>
          <p className="text-[13px] text-ink-muted mt-0.5 mb-3">
            {allowance > 0
              ? <>Allowed: {fmtPct(loan.extra_allowance_pct * 100, { digits: 0 })} = <span data-privacy="blur">{fmtEUR(allowance)}</span> per year · used in {new Date().getFullYear()}: <span data-privacy="blur">{fmtEUR(loan.extra_used_this_year)}</span></>
              : 'Paid each December until the fixed rate ends'}
          </p>
          <Pills label="Extra repayment per year" value={extra} onPick={setExtra}
            options={extraOptions.map(v => ({ value: v, label: v === 0 ? 'None' : `${fmtEUR(v)} / year` }))} />
          {extraScenario && (
            <dl className="grid grid-cols-2 gap-x-5 gap-y-3 mt-4">
              {loan.fixed_until && <Fact label={`Left in ${fmtMonth(loan.fixed_until)}`}><span data-privacy="blur">{fmtEUR(extraScenario.scenario.remaining_at_fixed_end)}</span></Fact>}
              <Fact label="Paid off">{fmtMonth(extraScenario.scenario.payoff_date)}</Fact>
              <Fact label="Total interest"><span data-privacy="blur">{fmtEUR(extraScenario.scenario.total_interest)}</span></Fact>
              <Fact label="Saved vs. no extra">{extraScenario.interest_saved > 0 ? <span className="text-sage" data-privacy="blur">{fmtEUR(extraScenario.interest_saved)}</span> : '—'}</Fact>
            </dl>
          )}
          <p className="text-[12px] text-ink-muted mt-3">Each December; the rate after the fixed period stays as assumed.</p>
        </section>

        {loan.fixed_until && followup != null && (
          <section aria-labelledby="l-rate" className={cardCls}>
            <h3 id="l-rate" className={cardTitleCls}>What if rates change by {new Date(loan.fixed_until).getFullYear()}?</h3>
            <p className="text-[13px] text-ink-muted mt-0.5 mb-3">Follow-up rate on what's left, same monthly payment</p>
            <Pills label="Follow-up interest rate" value={followup} onPick={setFollowup}
              options={rateOptions.map(v => ({ value: v, label: fmtPct(v * 100, { digits: 2 }) }))} />
            {rateScenario && (
              <dl className="grid grid-cols-2 gap-x-5 gap-y-3 mt-4">
                <Fact label="Paid off">{fmtMonth(rateScenario.payoff_date)}</Fact>
                <Fact label={`Interest after ${new Date(loan.fixed_until).getFullYear()}`}><span data-privacy="blur">{fmtEUR(rateScenario.interest_after_fixed)}</span></Fact>
              </dl>
            )}
            <p className="text-[12px] text-ink-muted mt-3">A forward loan can lock a rate up to five years before the fixed period ends.</p>
          </section>
        )}
      </div>

      <div className="grid gap-4" style={formGrid(340)}>
        <section aria-labelledby="l-pay" className={`${cardCls} min-w-0`}>
          <div className="flex flex-wrap justify-between items-center gap-3 mb-3">
            <h3 id="l-pay" className={cardTitleCls}>Payments</h3>
            <span className={`apple-chip ${loan.matched_count >= loan.due_count ? 'border-sage text-sage' : 'border-amber text-amber'}`}>{loan.matched_count} of {loan.due_count} matched</span>
          </div>
          {payments.length === 0 ? (
            <p className="text-[13px] text-ink-muted">No matching bank payments yet. Instalments are matched by exact amount within a week of the due date{loan.payment_account_id ? ' on the chosen account' : ''}; unmatched ones count as paid from untracked money.</p>
          ) : (
            <div className="overflow-x-auto rounded-apple border border-divider" role="region" tabIndex={0} aria-label="Matched loan payments">
              <table className="apple-table min-w-[380px]">
                <thead>
                  <tr>
                    <th scope="col">Date</th>
                    <th scope="col" className="num">Paid</th>
                    <th scope="col" className="num">Interest</th>
                    <th scope="col" className="num">Principal</th>
                  </tr>
                </thead>
                <tbody>
                  {shownPayments.map(p => (
                    <tr key={p.transaction_id}>
                      <td>{fmtDate(p.date)}</td>
                      <td className="num text-ink" data-privacy="blur">{fmtEUR(p.amount, cents)}</td>
                      <td className="num" data-privacy="blur">{fmtEUR(p.interest, cents)}</td>
                      <td className="num" data-privacy="blur">{fmtEUR(p.principal, cents)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className="flex flex-wrap gap-x-5 mt-2">
            {payments.length > 6 && (
              <button type="button" onClick={() => setShowAllPayments(v => !v)} aria-expanded={showAllPayments} className={linkBtnCls}>{showAllPayments ? 'Show fewer' : `Show all ${payments.length}`}</button>
            )}
            <button type="button" disabled={busy} onClick={() => run(() => api.matchLoanPayments())} className={linkBtnCls}>Match payments again</button>
          </div>
        </section>

        {next && (
          <section aria-labelledby="l-count" className={cardCls}>
            <h3 id="l-count" className={`${cardTitleCls} mb-3`}>How each payment counts</h3>
            <div role="img" aria-label={`Interest ${fmtPct(interestShare, { digits: 0 })} and principal ${fmtPct(100 - interestShare, { digits: 0 })} of the next payment`} className="flex h-3 rounded-[2px] overflow-hidden gap-0.5 bg-divider">
              <div className="bg-claret" style={{ width: `${interestShare}%` }} />
              <div className="bg-forest" style={{ width: `${100 - interestShare}%` }} />
            </div>
            <div className="flex flex-wrap justify-between gap-x-4 gap-y-1 mt-2 text-[13px] text-ink">
              <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="w-2.5 h-2.5 rounded-[2px] bg-claret" /><span data-privacy="blur">{fmtEUR(next.interest, cents)}</span> interest ({fmtPct(interestShare, { digits: 0 })})</span>
              <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="w-2.5 h-2.5 rounded-[2px] bg-forest" /><span data-privacy="blur">{fmtEUR(next.principal, cents)}</span> principal ({fmtPct(100 - interestShare, { digits: 0 })})</span>
            </div>
            <ul className="mt-4 pl-4 list-disc text-[13px] text-ink-body space-y-1.5">
              <li><strong className="text-ink">Interest</strong> is a cost: Cashflow books it under housing, and net worth goes down by it.</li>
              <li><strong className="text-ink">Principal</strong> is saving: cash turns into what's yours, net worth is unchanged.</li>
              <li>Savings rate counts the principal as saved.</li>
            </ul>
          </section>
        )}
      </div>

      <section aria-labelledby="l-extras" className={cardCls}>
        <h3 id="l-extras" className={`${cardTitleCls} mb-2`}>Extra repayments</h3>
        {(loan.extras ?? []).length === 0 ? (
          <p className="text-[13px] text-ink-muted mb-4">None yet.</p>
        ) : (
          <ul className="text-[13px] divide-y divide-divider mb-4">
            {(loan.extras ?? []).map(x => (
              <li key={x.id} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 py-2">
                <span className="text-ink-body"><span className="text-ink-muted">{fmtDate(x.date)}</span> · <span data-privacy="blur">{fmtEUR(x.amount, cents)}</span>{x.transaction_id ? ' · linked' : ''}</span>
                <button type="button" disabled={busy} onClick={() => removeExtra(x.id, x.date)} className="apple-btn-danger apple-btn-sm" aria-label={`Remove extra repayment from ${fmtDate(x.date)}…`}>Remove…</button>
              </li>
            ))}
          </ul>
        )}
        <form
          onSubmit={e => { e.preventDefault(); run(() => api.createExtraRepayment(loan.id, { date: newExtra.date, amount: parseFloat(newExtra.amount) }).then(() => setNewExtra({ date: today(), amount: '' }))); }}
          className="grid gap-3 items-end max-w-xl"
          style={formGrid(150)}
        >
          <Field label="Date" type="date" required min={loan.payout_date} value={newExtra.date} onChange={e => setNewExtra({ ...newExtra, date: e.target.value })} />
          <Field label="Amount" unit="€" type="number" inputMode="decimal" min="1" required value={newExtra.amount} onChange={e => setNewExtra({ ...newExtra, amount: e.target.value })} />
          <button type="submit" disabled={busy} className="apple-btn-secondary min-h-[44px]">Add extra repayment</button>
        </form>
      </section>

      <section aria-labelledby="l-terms" className={cardCls}>
        <div className="flex flex-wrap justify-between items-center gap-3 mb-3">
          <h3 id="l-terms" className={cardTitleCls}>Loan terms</h3>
          {!editing && <button type="button" onClick={() => { setForm(loanFormFrom(loan)); setEditing(true); }} className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]">Edit terms</button>}
        </div>
        {editing ? (
          <form onSubmit={e => { e.preventDefault(); run(() => api.updateLoan(loan.id, loanInputFrom(form)).then(() => setEditing(false))); }}>
            <LoanFields idPrefix="l-edit" value={form} onChange={setForm} />
            <div className="flex flex-wrap gap-2 mt-4">
              <button type="submit" disabled={busy} className="apple-btn-primary min-h-[44px]">{busy ? 'Saving…' : 'Save terms'}</button>
              <button type="button" onClick={() => setEditing(false)} className="apple-btn-secondary min-h-[44px]">Cancel</button>
            </div>
          </form>
        ) : (
          <dl className="grid gap-x-8 gap-y-3 text-[14px] text-ink" style={formGrid(190)}>
            <div><dt className="text-[12px] text-ink-muted">Amount</dt><dd><span data-privacy="blur">{fmtEUR(loan.principal)}</span> · paid out {fmtDate(loan.payout_date)}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Nominal rate</dt><dd>{fmtPct(loan.nominal_rate * 100, { digits: 2 })}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Initial repayment</dt><dd>{fmtPct(loan.initial_repayment * 100, { digits: 2 })} per year</dd></div>
            <div><dt className="text-[12px] text-ink-muted">First payment</dt><dd>{fmtDate(loan.first_payment_date)}, monthly</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Fixed until</dt><dd>{loan.fixed_until ? fmtDate(loan.fixed_until) : 'Whole term'}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Rate after that</dt><dd>{loan.followup_rate != null ? fmtPct(loan.followup_rate * 100, { digits: 2 }) : 'Assumed unchanged'}</dd></div>
            <div><dt className="text-[12px] text-ink-muted">Extra repayment allowed</dt><dd>{fmtPct(loan.extra_allowance_pct * 100, { digits: 0 })} per year</dd></div>
          </dl>
        )}
      </section>

      {error && <p role="alert" className="text-[13px] text-claret">{error}</p>}
      <div className="flex flex-wrap gap-2.5 pt-1">
        <button type="button" onClick={remove} disabled={busy} className="apple-btn-danger min-h-[44px] md:min-h-[40px]">Delete loan…</button>
      </div>
      {confirmDialog}
    </div>
  );
}
