import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { Section, SubHeading, MetricCard, MetricGrid, NumberField, Chip, EmptyState } from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { Assumptions, ScrollTable, toNum } from './shared';
import {
  accountTaxClass, capitalIncomeTaxRate, depletionAge, parsePct, pensionTaxableShare, simulateWithdrawals,
  statutoryAnnual, type SimPension, type WithdrawalOrder, type WithdrawalParams,
} from './planningMath';
import {
  DEFAULT_INFLATION_PCT, EARLIEST_STATUTORY_AGE, GRUNDFREIBETRAG_2026, RENTENWERT, SPARER_PAUSCHBETRAG, TFS_EQUITY_FUND,
} from './constants';

const ORDERS: { id: WithdrawalOrder; label: string; detail: string }[] = [
  { id: 'optimal', label: 'Tax-free band first', detail: 'Brokerage gains up to the allowances, then cash, then more brokerage' },
  { id: 'brokerage_first', label: 'Brokerage first', detail: 'Sell investments before using cash' },
  { id: 'cash_first', label: 'Cash first', detail: 'Use cash before selling investments' },
];

// 30-year year-by-year withdrawal simulation comparing three withdrawal
// orders. All tax rules live in planningMath.simulateWithdrawals: pensions
// taxed with the § 32a tariff on the year's total, brokerage sales taxed on
// their gain share with the Günstigerprüfung for every order, crypto and gold
// tax-free after a year (§ 23). Spending and pensions are entered in today's
// money and inflated from today; the table shows euros of each year.
//
// Stress tests re-run the cheapest order for 40 years with one assumption
// changed (high inflation, low returns, no pension increases, care costs, all
// four) to flag when the money runs out.
export default function WithdrawalSimulation() {
  const {
    accounts, projData, fireExpenses, pensionIncome, pensionSources, pensionAge, pensionCurrentAge,
    growthBrokerage, growthOther, crChildren, churchRate, joint, fireInflation, setFireInflation,
  } = usePlanning();
  const [returnPct, setReturnPct] = useState(4);
  const [pensionAdjPct, setPensionAdjPct] = useState(1.5);
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const annualNeed = toNum(fireExpenses);
  const inflationPct = parsePct(fireInflation, DEFAULT_INFLATION_PCT);

  const title = 'Withdrawal Simulation';

  if (annualNeed <= 0 || currentAge >= retireAge) {
    return (
      <Section id="withdrawal-simulation" title={title} subtitle="Thirty years of retirement, year by year, comparing three withdrawal orders.">
        <EmptyState
          title="Nothing to simulate yet"
          message={annualNeed <= 0
            ? 'Enter your annual expenses in the FIRE Calculator above to run the simulation.'
            : 'Your retirement age in Your Profile must be higher than your current age.'}
          action={annualNeed <= 0 ? { label: 'Go to the FIRE Calculator', href: '#fire' } : { label: 'Go to Your Profile', href: '#profile' }}
        />
      </Section>
    );
  }

  const thisYear = new Date().getFullYear();
  const yearsToRetire = retireAge - currentAge;
  const brokerageGrowth = toNum(growthBrokerage) / 100;
  const cashGrowth = toNum(growthOther) / 100;

  // Today's balances by how withdrawals are taxed. Loans and negative balances are not sources;
  // company, Riester and Rürup contracts pay out as pensions (entered under Pension Gap).
  const today = { capital: 0, privateSale: 0, cash: 0 };
  for (const a of accounts) {
    const bal = a.balance ?? 0;
    if (!a.is_active || bal <= 0) continue;
    const cls = accountTaxClass(a);
    if (cls === 'capital') today.capital += bal;
    else if (cls === 'private_sale') today.privateSale += bal;
    else if (cls === 'cash') today.cash += bal;
  }
  const growBrokerage = Math.pow(1 + brokerageGrowth, yearsToRetire);
  const tfs = projData?.drawdown?.teilfreistellung_pct != null ? projData.drawdown.teilfreistellung_pct / 100 : TFS_EQUITY_FUND;

  // Pensions: the Pension Gap sources when there are any, else the FIRE Calculator's figure (after tax).
  const explicitAnnualPension = toNum(pensionIncome);
  const sources = pensionSources.filter(src => src.monthly_gross > 0 || src.rentenpunkte > 0);
  const pensions: SimPension[] = sources.length > 0
    ? sources.map(src => {
      const start = src.start_age > 0 ? src.start_age : retireAge;
      const claimAge = src.type === 'gesetzliche' ? Math.max(start, EARLIEST_STATUTORY_AGE) : start;
      const annualGross = src.type === 'gesetzliche' && src.rentenpunkte > 0 ? statutoryAnnual(src.rentenpunkte, claimAge) : src.monthly_gross * 12;
      return {
        type: src.type,
        annualGross,
        startAge: claimAge,
        taxableShare: pensionTaxableShare(src.type, thisYear + (claimAge - currentAge), claimAge, src.tax_portion_pct),
      };
    })
    : explicitAnnualPension > 0
      ? [{ type: 'other', annualGross: explicitAnnualPension, startAge: retireAge, taxableShare: 0, isNet: true }]
      : [];

  const base: WithdrawalParams = {
    retireAge,
    yearsToRetire,
    firstCalendarYear: thisYear + yearsToRetire,
    annualNeed,
    inflation: inflationPct / 100,
    returnRate: returnPct / 100,
    cashRate: 0.01,
    pensionAdj: pensionAdjPct / 100,
    capital: today.capital * growBrokerage,
    capitalBasis: today.capital,
    privateSale: today.privateSale * growBrokerage,
    cash: today.cash * Math.pow(1 + cashGrowth, yearsToRetire),
    tfs,
    capRate: capitalIncomeTaxRate(churchRate),
    allowance: SPARER_PAUSCHBETRAG * (joint ? 2 : 1),
    pensions,
    childless: toNum(crChildren) === 0,
    joint,
    churchRate,
    years: 30,
  };

  const results = ORDERS.map(o => {
    const sim = simulateWithdrawals(base, o.id);
    return { ...o, sim, tax: sim.reduce((s, y) => s + y.taxPaid, 0), left: sim[sim.length - 1]?.remaining ?? 0 };
  });
  const cheapest = results.reduce((a, b) => (b.tax < a.tax ? b : a));
  const dearest = results.reduce((a, b) => (b.tax > a.tax ? b : a));
  const savings = dearest.tax - cheapest.tax;

  const scenarios = [
    { label: 'Base case', detail: 'Your inputs above', p: {} },
    { label: 'High inflation', detail: '3,5 % a year', p: { inflation: 0.035 } },
    { label: 'Low returns', detail: '2 % a year', p: { returnRate: 0.02 } },
    { label: 'No pension increases', detail: 'Pensions stay flat (no Rentenanpassung)', p: { pensionAdj: 0 } },
    { label: 'Long-term care', detail: '24.000 € a year extra from age 80, in today’s money', p: { careFromAge: 80, careCost: 24000 } },
    { label: 'Worst case', detail: 'All four at once', p: { inflation: 0.035, returnRate: 0.02, pensionAdj: 0, careFromAge: 80, careCost: 24000 } },
  ];

  const depletesAt = depletionAge(cheapest.sim);

  return (
    <Section
      id="withdrawal-simulation"
      title={title}
      subtitle={`Thirty years of retirement from age ${retireAge}, year by year, comparing three withdrawal orders.`}
    >
      <div className="grid grid-cols-2 gap-x-3 gap-y-4 md:grid-cols-3">
        <NumberField label="Return in retirement" unit="% p.a." value={returnPct} onValueChange={setReturnPct} />
        <NumberField label="Inflation" unit="% p.a." value={inflationPct} onValueChange={v => setFireInflation(String(v))} hint="Shared with the FIRE Calculator." />
        <NumberField label="Yearly pension increase" unit="% p.a." value={pensionAdjPct} onValueChange={setPensionAdjPct} />
      </div>

      <SubHeading className="mt-6">Tax paid over 30 years, by withdrawal order</SubHeading>
      <MetricGrid className="mt-3">
        {results.map(r => (
          <MetricCard
            key={r.id}
            label={r.label}
            value={fmtEUR(r.tax)}
            sublabel={<>{r.detail}. <span data-privacy="blur">{fmtEUR(r.left)}</span> left after 30 years.</>}
          />
        ))}
      </MetricGrid>

      <p className="mt-3 text-[14px] text-ink-body">
        {savings >= 1
          ? <>“{cheapest.label}” pays the least tax: <span className="font-medium text-sage" data-privacy="blur">{fmtEUR(savings)}</span> less than “{dearest.label}”.</>
          : 'All three orders pay about the same tax.'}
        {' '}Every order gets the same allowances, including the lower personal rate on capital gains where it applies (Günstigerprüfung).
      </p>

      <SubHeading className="mt-6">“{cheapest.label}”, year by year</SubHeading>
      <p className="mt-1 mb-3 text-[13px] text-ink-muted">Amounts per year, in euros of that year. The first ten years, then every fifth.</p>
      <ScrollTable label="Withdrawal simulation, year by year">
        <table className="apple-table min-w-[680px]">
          <thead>
            <tr>
              <th scope="col">Year (age)</th>
              <th scope="col" className="num">Spending</th>
              <th scope="col" className="num">Pension</th>
              <th scope="col" className="num">Brokerage</th>
              <th scope="col" className="num">Cash</th>
              <th scope="col" className="num">Tax</th>
              <th scope="col" className="num">Left over</th>
            </tr>
          </thead>
          <tbody>
            {cheapest.sim.filter((_, i) => i < 10 || i % 5 === 0).map(y => (
              <tr key={y.year}>
                <td className="whitespace-nowrap">{y.year} <span className="text-ink-muted">({y.age})</span></td>
                <td className="num" data-privacy="blur">{fmtEUR(y.need)}</td>
                <td className="num" data-privacy="blur">{y.fromPension > 0 ? fmtEUR(y.fromPension) : '—'}</td>
                <td className="num" data-privacy="blur">{y.fromBrokerage > 0 ? fmtEUR(y.fromBrokerage) : '—'}</td>
                <td className="num" data-privacy="blur">{y.fromCash > 0 ? fmtEUR(y.fromCash) : '—'}</td>
                <td className="num" data-privacy="blur">{fmtEUR(y.taxPaid)}</td>
                <td className="num whitespace-nowrap">
                  <span className="font-medium text-ink" data-privacy="blur">{fmtEUR(y.remaining)}</span>
                  {y.shortfall > 0 && <span className="block text-[12px] text-claret" data-privacy="blur">short by {fmtEUR(y.shortfall)}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </ScrollTable>

      {depletesAt && (
        <p className="mt-3 flex flex-wrap items-center gap-2 text-[14px] text-ink">
          <Chip tone="danger">Runs out</Chip> In the base case the money runs out at age {depletesAt}.
        </p>
      )}

      <SubHeading className="mt-6">Stress tests</SubHeading>
      <p className="mt-1 text-[13px] text-ink-muted">“{cheapest.label}” over 40 years, with one assumption changed at a time.</p>
      <ul className="mt-2 border-t border-divider">
        {scenarios.map(s => {
          const deplAge = depletionAge(simulateWithdrawals({ ...base, years: 40, ...s.p }, cheapest.id));
          const survives = deplAge === null;
          return (
            <li key={s.label} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-b border-divider py-2.5">
              <div className="min-w-0">
                <p className="text-[14px] text-ink">{s.label}</p>
                <p className="text-[13px] text-ink-muted">{s.detail}</p>
              </div>
              <Chip tone={survives ? 'success' : deplAge > 85 ? 'warning' : 'danger'}>
                {survives ? 'Lasts 40 years' : `Runs out at age ${deplAge}`}
              </Chip>
            </li>
          );
        })}
      </ul>

      <Assumptions>
        <p>
          {sources.length > 0
            ? <>Pension: your {sources.length} pension {sources.length === 1 ? 'source' : 'sources'} from Pension Gap, each from its own start age (the statutory pension not before 63, reduced 0,3 % a month before 67); pension points are worth {fmtEUR(RENTENWERT, { cents: true })} a month each. The taxable share follows the start year (statutory and Rürup) or age (private annuities); company and Riester pensions are taxed in full.</>
            : explicitAnnualPension > 0
              ? <>Pension: <span data-privacy="blur">{fmtEUR(explicitAnnualPension)}</span> a year after tax from age {retireAge} — the “Pension income in retirement” figure from the FIRE Calculator.</>
              : <>Pension: none. No pension is entered under Pension Gap or in the FIRE Calculator, so the portfolio covers all spending.</>}
          {' '}Pensions are in today’s money and rise {fmtPct(pensionAdjPct)} a year from today.
        </p>
        <p>
          Starting balances: today’s brokerage, crypto and precious-metal accounts grow {fmtPct(brokerageGrowth * 100)} a year and cash {fmtPct(cashGrowth * 100)} a year until retirement (the rates set in Withdrawal Sources), with no further deposits; loans and accounts below zero are left out.
        </p>
        <p>
          Spending of <span data-privacy="blur">{fmtEUR(annualNeed)}</span> a year in today’s money rises {fmtPct(inflationPct)} a year from today, so the first retirement year needs <span data-privacy="blur">{fmtEUR(annualNeed * Math.pow(1 + inflationPct / 100, yearsToRetire))}</span>. In retirement, investments return {fmtPct(returnPct)} a year and cash earns 1 %.
        </p>
        <p>
          Tax: pensions with the 2026 income-tax tariff on their combined taxable part (basic allowance {fmtEUR(GRUNDFREIBETRAG_2026)}, assumed to rise with prices), after health and care insurance of retirees. Brokerage sales are taxed only on their gain, at {fmtPct(base.capRate * 100, { digits: 3 })} after the {fmtPct(tfs * 100, { digits: 0 })} partial exemption and the {fmtEUR(base.allowance)} saver’s allowance, or at your personal rate if lower; today’s balance counts as the amount paid in. Crypto and gold held over a year are tax-free.
        </p>
      </Assumptions>
    </Section>
  );
}
