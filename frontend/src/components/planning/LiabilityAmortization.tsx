import { useCallback, useEffect, useState } from 'react';
import { api, type Loan } from '../../api/client';
import { usePlanning } from './PlanningContext';
import { Section, SubHeading, MetricCard, MetricGrid, NumberField, LoadingState, ErrorState, EmptyState } from '../ui';
import { fmtEUR, fmtPct, fmtNum, fmtMonth } from '../../utils/format';

/**
 * Interest until payoff at the current payment. After the fixed-rate period (`fixedMonths`),
 * the follow-up rate applies when one is recorded.
 */
function estimateRemainingInterest(
  balance: number, annualRate: number, monthlyPayment: number, fixedMonths?: number | null, followupRate?: number | null,
): { interest: number; payoffLabel: string; interestOnly: boolean } {
  if (balance <= 0) return { interest: 0, payoffLabel: 'Paid off', interestOnly: false };
  const monthlyRate = annualRate / 12;
  const rateAt = (m: number) => (followupRate != null && fixedMonths != null && m >= fixedMonths ? followupRate / 12 : monthlyRate);
  if (monthlyPayment <= balance * Math.max(monthlyRate, rateAt(600))) {
    return { interest: balance * Math.max(annualRate, followupRate ?? 0) * 25, payoffLabel: 'Never at this payment', interestOnly: true };
  }
  let rem = balance;
  let totalInt = 0;
  let months = 0;
  while (rem > 0.01 && months < 600) {
    const intPart = rem * rateAt(months);
    const pay = Math.min(monthlyPayment, rem + intPart);
    totalInt += intPart;
    rem -= (pay - intPart);
    months++;
  }
  const years = Math.round((months / 12) * 10) / 10;
  return { interest: totalInt, payoffLabel: months >= 600 ? 'over 50 years' : `${fmtNum(years, 1)} years`, interestOnly: false };
}

// Mortgage / loan amortization summary. Reads real loans from /api/assets
// (with their actual nominal rate, monthly annuity, remaining balance, and
// payoff date). Liability accounts without recorded loan terms fall back to
// an assumed rate and term, which are stated and editable (1.8 % / 25 years
// by default).
export default function LiabilityAmortization() {
  const { accounts } = usePlanning();
  const [loans, setLoans] = useState<Loan[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [assumedRate, setAssumedRate] = useState(1.8);
  const [assumedTerm, setAssumedTerm] = useState(25);

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    api.listAssets()
      .then(r => setLoans((Array.isArray(r?.loans) ? r.loans : []).filter(l => l.balance > 0)))
      .catch(e => setError(e instanceof Error ? e.message : 'Request failed'))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { load(); }, [load]);

  const liabilities = accounts.filter(a => a.type === 'liability' && (a.balance ?? 0) < 0);
  const currentYear = new Date().getFullYear();

  return (
    <Section
      id="liabilities"
      title="Liability Amortization"
      subtitle="What you still owe on each loan, what it will cost in interest and when it is paid off."
    >
      {loading && <LoadingState message="Loading loans" lines={3} />}
      {error && <ErrorState message="Your loans couldn’t be loaded." details={error} onRetry={load} />}

      {!loading && !error && loans.length === 0 && liabilities.length === 0 && (
        <EmptyState
          title="No loans or liabilities"
          message="Loans you record against a property or other asset appear here with their repayment schedule."
          action={{ label: 'Go to Net Worth', href: '/' }}
        />
      )}

      <div className="flex flex-col gap-7">
        {loans.map(loan => {
          const futureScheduleInterest = loan.yearly
            ? loan.yearly.filter(y => y.year >= currentYear).reduce((s, y) => s + y.interest, 0)
            : null;
          const est = estimateRemainingInterest(loan.balance, loan.nominal_rate, loan.monthly_payment, loan.months_left_fixed, loan.followup_rate);
          const fromSchedule = futureScheduleInterest != null && futureScheduleInterest > 0;
          const remainingInterest = fromSchedule ? futureScheduleInterest : est.interest;
          const payoffLabel = loan.payoff_date ? fmtMonth(loan.payoff_date) : est.payoffLabel;

          return (
            <div key={loan.id}>
              <SubHeading>
                {loan.name}{loan.asset_name ? ` · ${loan.asset_name}` : ''}
                <span className="ml-2 font-normal text-ink-muted">{fmtPct(loan.nominal_rate * 100, { digits: 2 })} p.a.</span>
              </SubHeading>
              <MetricGrid className="mt-3">
                <MetricCard label="Outstanding" value={fmtEUR(loan.balance)} />
                <MetricCard label="Monthly payment" value={fmtEUR(loan.monthly_payment, { cents: true })} />
                <MetricCard
                  label="Interest still to pay"
                  value={fmtEUR(remainingInterest)}
                  sublabel={fromSchedule
                    ? `From ${currentYear} on, per the loan’s repayment schedule`
                    : est.interestOnly
                      ? 'Over 25 years: the payment only covers interest'
                      : loan.followup_rate != null && loan.months_left_fixed != null
                        ? `At the current payment; ${fmtPct(loan.followup_rate * 100, { digits: 2 })} after the fixed-rate period`
                        : 'At the current rate and payment until paid off'}
                />
                <MetricCard
                  label="Paid off"
                  money={false}
                  value={payoffLabel}
                  sublabel={loan.payoff_date ? undefined : est.interestOnly ? 'The balance does not shrink' : 'From today, at the current rate and payment'}
                />
              </MetricGrid>
            </div>
          );
        })}

        {liabilities.length > 0 && (
          <div>
            <SubHeading>Liability accounts without loan terms</SubHeading>
            <p className="mt-1 text-[13px] text-ink-muted">
              No interest rate or term is recorded for {liabilities.length === 1 ? 'this account' : 'these accounts'}, so the figures below assume the rate and term you set here.
              Record the loan under Net Worth to use its real terms.
            </p>
            <div className="mt-3 grid grid-cols-2 gap-x-3 gap-y-4 md:grid-cols-4">
              <NumberField label="Assumed interest rate" unit="% p.a." value={assumedRate} onValueChange={v => setAssumedRate(Math.max(0, v))} />
              <NumberField label="Assumed term" unit="years" integer value={assumedTerm} onValueChange={v => setAssumedTerm(Math.max(1, v))} />
            </div>

            <div className="mt-5 flex flex-col gap-6">
              {liabilities.map(acc => {
                const principal = Math.abs(acc.balance ?? 0);
                const annualRate = assumedRate / 100;
                const termYears = assumedTerm;
                const monthlyRate = annualRate / 12;
                const numPayments = termYears * 12;
                const monthlyPayment = monthlyRate > 0
                  ? principal * (monthlyRate * Math.pow(1 + monthlyRate, numPayments)) / (Math.pow(1 + monthlyRate, numPayments) - 1)
                  : principal / numPayments;
                const totalPayment = monthlyPayment * numPayments;
                const totalInterest = totalPayment - principal;
                const assumption = `Assumes ${fmtPct(assumedRate, { digits: 2 })} over ${fmtNum(termYears)} years`;

                return (
                  <div key={acc.id}>
                    <p className="text-[14px] font-medium text-ink">{acc.name}</p>
                    <MetricGrid className="mt-2">
                      <MetricCard label="Outstanding" value={fmtEUR(principal)} />
                      <MetricCard label="Monthly payment" value={fmtEUR(monthlyPayment)} sublabel={assumption} />
                      <MetricCard label="Total interest" value={fmtEUR(totalInterest)} sublabel={assumption} />
                      <MetricCard label="Paid off" money={false} value={`in ${fmtNum(termYears)} years`} sublabel="The assumed term" />
                    </MetricGrid>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </Section>
  );
}
