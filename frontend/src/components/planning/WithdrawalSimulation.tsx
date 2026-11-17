import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { Section, SubHeading, MetricCard, MetricGrid, NumberField, Chip, EmptyState } from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { Assumptions, ScrollTable, toNum } from './shared';

// 30-year year-by-year withdrawal simulation comparing three strategies:
// optimal (fill 0% Grundfreibetrag with brokerage gains first, then tax-
// free cash, then taxable brokerage), brokerage-first (naive), and cash-
// first (naive). The "optimal" advantage compounds because the German
// 11604 EUR Grundfreibetrag would otherwise go unused early on.
//
// Return, inflation and the yearly pension increase are inputs (defaults 4 % / 2 % /
// 1,5 %); every other constant is listed under "Assumptions" below the results.
//
// Sensitivity analysis re-runs a simplified optimal with 6 stress
// scenarios (high inflation, low returns, no Rentenanpassung, late-life
// Pflege costs, worst-case combo) to flag depletion risk.
export default function WithdrawalSimulation() {
  const { accounts, fireExpenses, pensionIncome, pensionSources, pensionAge, pensionCurrentAge, crMarginalRate, growthBrokerage, growthOther } = usePlanning();
  const [returnPct, setReturnPct] = useState(4);
  const [inflationPct, setInflationPct] = useState(2);
  const [pensionAdjPct, setPensionAdjPct] = useState(1.5);
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const annualNeed = parseFloat(fireExpenses) || 0;
  const marginalRate = parseFloat(crMarginalRate) || 42;

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

  const yearsToRetire = retireAge - currentAge;
  const returnRate = returnPct / 100;
  const inflationRate = inflationPct / 100;
  const pensionAdjRate = pensionAdjPct / 100;
  const brokerageGrowth = toNum(growthBrokerage);
  const cashGrowth = toNum(growthOther);
  const grundfreibetrag = 11604;
  const brokTaxRate = 0.185; // ~26.375% Abgeltungsteuer+Soli × 70% after 30% Teilfreistellung

  const brokerageAccs = accounts.filter(a => ['brokerage', 'crypto', 'precious_metals'].includes(a.type) && a.is_active);
  const cashAccs = accounts.filter(a => (a.type === 'checking' || a.type === 'savings') && a.is_active);
  const brokerageVal = brokerageAccs.reduce((s, a) => s + (a.balance ?? 0), 0) * Math.pow(1 + brokerageGrowth / 100, yearsToRetire);
  const cashVal = cashAccs.reduce((s, a) => s + (a.balance ?? 0), 0) * Math.pow(1 + cashGrowth / 100, yearsToRetire);

  const explicitAnnualPension = parseFloat(pensionIncome) || 0;
  const computePensionAtAge = (age: number, yearsElapsed: number, pensionAdj: number) => {
    const growth = Math.pow(1 + pensionAdj, yearsElapsed);
    if (explicitAnnualPension > 0) {
      const gross = age >= 67 ? explicitAnnualPension * growth : 0;
      return { gross, taxable: gross * 0.83 };
    }
    let gross = 0;
    let taxable = 0;
    for (const src of pensionSources) {
      const startAge = src.start_age || 67;
      if (age < startAge) continue;
      const baseAnnual = src.monthly_gross > 0
        ? src.monthly_gross * 12
        : (src.rentenpunkte > 0 ? src.rentenpunkte * 39.32 * 12 : 0);
      if (baseAnnual <= 0) continue;
      const adjGross = baseAnnual * growth;
      const taxPct = (src.tax_portion_pct ?? 83) / 100;
      gross += adjGross;
      taxable += adjGross * taxPct;
    }
    return { gross, taxable };
  };

  type SimYear = { year: number; age: number; need: number; fromBrokerage: number; fromCash: number; fromPension: number; taxPaid: number; remaining: number; shortfall: number };
  const simulate = (strategy: 'optimal' | 'brokerage_first' | 'cash_first'): SimYear[] => {
    let brok = brokerageVal;
    let cash = cashVal;
    const years: SimYear[] = [];

    for (let y = 0; y < 30; y++) {
      const age = retireAge + y;
      const calYear = new Date().getFullYear() + yearsToRetire + y;
      const need = annualNeed * Math.pow(1 + inflationRate, y);
      const { gross: pensionAnnual, taxable: pensionTaxable } = computePensionAtAge(age, y, pensionAdjRate);
      const pensionTax = Math.max(0, pensionTaxable - grundfreibetrag) * (marginalRate / 100);
      const netPension = Math.max(0, pensionAnnual - pensionTax);
      let remaining = need - netPension;
      let fromBrok = 0, fromCash = 0, taxPaid = pensionTax;

      if (remaining <= 0) {
        years.push({ year: calYear, age, need: Math.round(need), fromBrokerage: 0, fromCash: 0, fromPension: Math.round(netPension), taxPaid: Math.round(taxPaid), remaining: Math.round(brok + cash), shortfall: 0 });
        brok *= (1 + returnRate); cash *= 1.01;
        continue;
      }

      const withdrawTaxableBrokerage = () => {
        if (remaining <= 0 || brok <= 0) return;
        const grossNeeded = remaining / (1 - brokTaxRate);
        const grossB = Math.min(brok, grossNeeded);
        const taxB = grossB * brokTaxRate;
        const netB = grossB - taxB;
        brok -= grossB;
        fromBrok += grossB;
        taxPaid += taxB;
        remaining = Math.max(0, remaining - netB);
      };

      const withdrawCash = () => {
        if (remaining <= 0 || cash <= 0) return;
        const fromC = Math.min(remaining, cash);
        cash -= fromC;
        fromCash += fromC;
        remaining = Math.max(0, remaining - fromC);
      };

      if (strategy === 'optimal') {
        // Optimal: fill the 0% Grundfreibetrag bracket with brokerage gains
        // first (Teilfreistellung -> /0.7 to convert tax-free room into gross
        // brokerage withdrawal), THEN tax-free cash, THEN taxable brokerage.
        const taxableRoom = Math.max(0, grundfreibetrag - pensionTaxable);
        const brokInFreeband = Math.min(remaining, brok, taxableRoom / 0.7);
        if (brokInFreeband > 0) {
          brok -= brokInFreeband;
          fromBrok += brokInFreeband;
          remaining = Math.max(0, remaining - brokInFreeband);
        }
        withdrawCash();
        withdrawTaxableBrokerage();
      } else if (strategy === 'brokerage_first') {
        withdrawTaxableBrokerage();
        withdrawCash();
      } else {
        withdrawCash();
        withdrawTaxableBrokerage();
      }

      brok *= (1 + returnRate); cash *= 1.01;
      const shortfall = Math.max(0, Math.round(remaining));
      years.push({ year: calYear, age, need: Math.round(need), fromBrokerage: Math.round(fromBrok), fromCash: Math.round(fromCash), fromPension: Math.round(netPension), taxPaid: Math.round(taxPaid), remaining: Math.round(brok + cash), shortfall });
    }
    return years;
  };

  const optimal = simulate('optimal');
  const brokFirst = simulate('brokerage_first');
  const cashFirst = simulate('cash_first');

  const totalTax = (sim: SimYear[]) => sim.reduce((s, y) => s + y.taxPaid, 0);
  const depletionAge = (sim: SimYear[]) => sim.find(y => y.remaining <= 0 && y.shortfall > 0)?.age || sim.find(y => y.remaining <= 0)?.age || null;
  const optTax = totalTax(optimal);
  const worstNaive = Math.max(totalTax(brokFirst), totalTax(cashFirst));
  const savings = worstNaive - optTax;

  const runScenario = (inflRate: number, retRate: number, pensionAdj: number, careCostAge: number | null, careCostAnnual: number) => {
    let brok = brokerageVal, cash = cashVal;
    for (let y = 0; y < 40; y++) {
      const age = retireAge + y;
      const need = annualNeed * Math.pow(1 + inflRate, y) + (careCostAge && age >= careCostAge ? careCostAnnual : 0);
      const { gross: pensionGross, taxable: pensionTaxable } = computePensionAtAge(age, y, pensionAdj);
      const pensionTax = Math.max(0, pensionTaxable - grundfreibetrag) * (marginalRate / 100);
      const netPension = Math.max(0, pensionGross - pensionTax);
      let rem = Math.max(0, need - netPension);
      const taxableRoom = Math.max(0, grundfreibetrag - pensionTaxable);
      const freeBrok = Math.min(rem, brok, taxableRoom / 0.7);
      if (freeBrok > 0) { brok -= freeBrok; rem = Math.max(0, rem - freeBrok); }
      const fromC = Math.min(rem, cash); cash -= fromC; rem = Math.max(0, rem - fromC);
      if (rem > 0 && brok > 0) {
        const grossB = Math.min(brok, rem / (1 - brokTaxRate));
        brok -= grossB;
        rem = Math.max(0, rem - grossB * (1 - brokTaxRate));
      }
      brok *= (1 + retRate); cash *= 1.01;
      if (brok + cash <= 0.5 && rem > 1) return age;
    }
    return null;
  };

  const scenarios = [
    { label: 'Base case', detail: 'Your inputs above', inflation: inflationRate, returns: returnRate, pensionAdj: pensionAdjRate, careAge: null as number | null, careCost: 0 },
    { label: 'High inflation', detail: '3,5 % a year', inflation: 0.035, returns: returnRate, pensionAdj: pensionAdjRate, careAge: null, careCost: 0 },
    { label: 'Low returns', detail: '2 % a year', inflation: inflationRate, returns: 0.02, pensionAdj: pensionAdjRate, careAge: null, careCost: 0 },
    { label: 'No pension increases', detail: 'Pensions stay flat (no Rentenanpassung)', inflation: inflationRate, returns: returnRate, pensionAdj: 0, careAge: null, careCost: 0 },
    { label: 'Long-term care', detail: '24.000 € a year extra from age 80', inflation: inflationRate, returns: returnRate, pensionAdj: pensionAdjRate, careAge: 80, careCost: 24000 },
    { label: 'Worst case', detail: 'All four at once', inflation: 0.035, returns: 0.02, pensionAdj: 0, careAge: 80, careCost: 24000 },
  ];

  const pensionSourceCount = pensionSources.filter(src => src.monthly_gross > 0 || src.rentenpunkte > 0).length;
  const depletesAt = depletionAge(optimal);

  return (
    <Section
      id="withdrawal-simulation"
      title={title}
      subtitle={`Thirty years of retirement from age ${retireAge}, year by year, comparing three withdrawal orders.`}
    >
      <div className="grid grid-cols-2 gap-x-3 gap-y-4 md:grid-cols-3">
        <NumberField label="Return in retirement" unit="% p.a." value={returnPct} onValueChange={setReturnPct} />
        <NumberField label="Inflation" unit="% p.a." value={inflationPct} onValueChange={setInflationPct} />
        <NumberField label="Yearly pension increase" unit="% p.a." value={pensionAdjPct} onValueChange={setPensionAdjPct} />
      </div>

      <SubHeading className="mt-6">Tax paid over 30 years, by withdrawal order</SubHeading>
      <MetricGrid className="mt-3">
        <MetricCard label="Tax-optimised order" value={fmtEUR(optTax)} sublabel="Tax-free allowance first, then cash, then brokerage" />
        <MetricCard label="Brokerage first" value={fmtEUR(totalTax(brokFirst))} sublabel="Sell investments before using cash" />
        <MetricCard label="Cash first" value={fmtEUR(totalTax(cashFirst))} sublabel="Use cash before selling investments" />
      </MetricGrid>

      {savings > 0 && (
        <p className="mt-3 text-[14px] text-ink-body">
          The tax-optimised order saves <span className="font-medium text-sage" data-privacy="blur">{fmtEUR(savings)}</span> in tax compared with the more expensive of the other two.
        </p>
      )}

      <SubHeading className="mt-6">Tax-optimised order, year by year</SubHeading>
      <p className="mt-1 mb-3 text-[13px] text-ink-muted">Amounts per year. The first ten years, then every fifth.</p>
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
            {optimal.filter((_, i) => i < 10 || i % 5 === 0).map(y => (
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
      <p className="mt-1 text-[13px] text-ink-muted">The tax-optimised order over 40 years, with one assumption changed at a time.</p>
      <ul className="mt-2 border-t border-divider">
        {scenarios.map(s => {
          const deplAge = runScenario(s.inflation, s.returns, s.pensionAdj, s.careAge, s.careCost);
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
          {explicitAnnualPension > 0
            ? <>Pension: <span data-privacy="blur">{fmtEUR(explicitAnnualPension)}</span> a year from age 67 — the “Pension income in retirement” figure from the FIRE Calculator — with 83 % of it taxable.</>
            : pensionSourceCount > 0
              ? <>Pension: your {pensionSourceCount} pension {pensionSourceCount === 1 ? 'source' : 'sources'} from Pension Gap, each from its own start age; pension points are valued at 39,32 € a month each.</>
              : <>Pension: none. No pension is entered in the FIRE Calculator or under Pension Gap, so the portfolio covers all spending.</>}
          {' '}It rises {fmtPct(pensionAdjPct)} a year.
        </p>
        <p>
          Starting balances: today’s brokerage, crypto and precious-metal accounts grow {fmtPct(brokerageGrowth)} a year and cash {fmtPct(cashGrowth)} a year until retirement (the rates set in Withdrawal Sources), with no further deposits.
        </p>
        <p>In retirement, investments return {fmtPct(returnPct)} a year and cash earns 1 % a year; spending of <span data-privacy="blur">{fmtEUR(annualNeed)}</span> a year (your annual expenses) rises with inflation.</p>
        <p>
          Tax: brokerage withdrawals at 18,5 % (26,375 % flat-rate tax after the 30 % partial exemption for equity funds, applied to the whole withdrawal);
          pension income above the 11.604 € basic allowance (Grundfreibetrag) at your {fmtPct(marginalRate, { digits: 0 })} marginal rate. The allowance stays fixed for all years.
        </p>
      </Assumptions>
    </Section>
  );
}
