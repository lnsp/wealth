import { usePlanning } from './PlanningContext';

// 30-year year-by-year withdrawal simulation comparing three strategies:
// optimal (fill 0% Grundfreibetrag with brokerage gains first, then tax-
// free cash, then taxable brokerage), brokerage-first (naive), and cash-
// first (naive). The "optimal" advantage compounds because the German
// 11604 EUR Grundfreibetrag would otherwise go unused early on.
//
// Sensitivity analysis re-runs a simplified optimal with 6 stress
// scenarios (high inflation, low returns, no Rentenanpassung, late-life
// Pflege costs, worst-case combo) to flag depletion risk.
export default function WithdrawalSimulation() {
  const { accounts, pensionAge, pensionCurrentAge, crMarginalRate, fmt } = usePlanning();
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const annualNeed = parseFloat(localStorage.getItem('fire_expenses') || '0');
  const marginalRate = parseFloat(crMarginalRate) || 42;

  if (annualNeed <= 0 || currentAge >= retireAge) return null;

  const yearsToRetire = retireAge - currentAge;
  const returnRate = 0.04;
  const inflationRate = 0.02;

  const brokerageAccs = accounts.filter(a => a.type === 'brokerage' && a.is_active);
  const cashAccs = accounts.filter(a => (a.type === 'checking' || a.type === 'savings') && a.is_active);
  const brokerageVal = brokerageAccs.reduce((s, a) => s + (a.balance ?? 0), 0) * Math.pow(1.07, yearsToRetire);
  const cashVal = cashAccs.reduce((s, a) => s + (a.balance ?? 0), 0) * Math.pow(1.02, yearsToRetire);

  type SimYear = { year: number; age: number; need: number; fromBrokerage: number; fromCash: number; fromPension: number; taxPaid: number; remaining: number; shortfall: number };
  const simulate = (strategy: 'optimal' | 'brokerage_first' | 'cash_first'): SimYear[] => {
    let brok = brokerageVal;
    let cash = cashVal;
    const years: SimYear[] = [];
    const pensionMonthly = 1200;

    for (let y = 0; y < 30; y++) {
      const age = retireAge + y;
      const calYear = new Date().getFullYear() + yearsToRetire + y;
      const need = annualNeed * Math.pow(1 + inflationRate, y);
      const pensionAnnual = age >= 67 ? pensionMonthly * 12 : 0;
      const pensionTax = pensionAnnual * 0.83 * (marginalRate / 100);
      let remaining = need - pensionAnnual;
      let fromBrok = 0, fromCash = 0, taxPaid = pensionTax;

      if (remaining <= 0) {
        years.push({ year: calYear, age, need: Math.round(need), fromBrokerage: 0, fromCash: 0, fromPension: Math.round(pensionAnnual), taxPaid: Math.round(taxPaid), remaining: Math.round(brok + cash), shortfall: 0 });
        brok *= (1 + returnRate); cash *= 1.01;
        continue;
      }

      if (strategy === 'optimal') {
        // Optimal: fill the 0% Grundfreibetrag bracket with brokerage gains
        // first (Teilfreistellung→/0.7 to convert tax-free room into gross
        // brokerage withdrawal), THEN tax-free cash, THEN taxable brokerage.
        const grundfreibetrag = 11604;
        const taxableRoom = Math.max(0, grundfreibetrag - pensionAnnual * 0.83);
        const brokInFreeband = Math.min(remaining, brok, taxableRoom / 0.7);
        if (brokInFreeband > 0) { brok -= brokInFreeband; fromBrok = brokInFreeband; remaining -= brokInFreeband; }
        if (remaining > 0) { const fromC = Math.min(remaining, cash); cash -= fromC; fromCash = fromC; remaining -= fromC; }
        if (remaining > 0) { const fromB = Math.min(remaining, brok); brok -= fromB; fromBrok += fromB; remaining -= fromB; taxPaid += fromB * 0.185; }
      } else if (strategy === 'brokerage_first') {
        const fromB = Math.min(remaining, brok); brok -= fromB; fromBrok = fromB; remaining -= fromB; taxPaid += fromB * 0.185;
        if (remaining > 0) { const fromC = Math.min(remaining, cash); cash -= fromC; fromCash = fromC; remaining -= fromC; }
      } else {
        const fromC = Math.min(remaining, cash); cash -= fromC; fromCash = fromC; remaining -= fromC;
        if (remaining > 0) { const fromB = Math.min(remaining, brok); brok -= fromB; fromBrok = fromB; remaining -= fromB; taxPaid += fromB * 0.185; }
      }

      brok *= (1 + returnRate); cash *= 1.01;
      const shortfall = Math.max(0, Math.round(remaining));
      years.push({ year: calYear, age, need: Math.round(need), fromBrokerage: Math.round(fromBrok), fromCash: Math.round(fromCash), fromPension: Math.round(pensionAnnual), taxPaid: Math.round(taxPaid), remaining: Math.round(brok + cash), shortfall });
    }
    return years;
  };

  const optimal = simulate('optimal');
  const brokFirst = simulate('brokerage_first');
  const cashFirst = simulate('cash_first');

  const totalTax = (sim: SimYear[]) => sim.reduce((s, y) => s + y.taxPaid, 0);
  const depletionAge = (sim: SimYear[]) => sim.find(y => y.remaining <= 0)?.age || null;
  const optTax = totalTax(optimal);
  const worstNaive = Math.max(totalTax(brokFirst), totalTax(cashFirst));
  const savings = worstNaive - optTax;

  const runScenario = (inflRate: number, retRate: number, pensionAdj: number, careCostAge: number | null, careCostAnnual: number) => {
    let brok = brokerageVal, cash = cashVal;
    const pensionBase = 1200 * 12;
    for (let y = 0; y < 40; y++) {
      const age = retireAge + y;
      const need = annualNeed * Math.pow(1 + inflRate, y) + (careCostAge && age >= careCostAge ? careCostAnnual : 0);
      const pension = age >= 67 ? pensionBase * Math.pow(1 + pensionAdj, y) : 0;
      let rem = Math.max(0, need - pension);
      const fromC = Math.min(rem, cash); cash -= fromC; rem -= fromC;
      if (rem > 0) { const fromB = Math.min(rem, brok); brok -= fromB; rem -= fromB; }
      brok *= (1 + retRate); cash *= 1.01;
      if (brok + cash <= 0 && rem > 0) return age;
    }
    return null;
  };

  const scenarios = [
    { label: 'Base Case', inflation: 0.02, returns: 0.04, pensionAdj: 0.015, careAge: null as number | null, careCost: 0 },
    { label: 'High Inflation (3.5%)', inflation: 0.035, returns: 0.04, pensionAdj: 0.015, careAge: null, careCost: 0 },
    { label: 'Low Returns (2%)', inflation: 0.02, returns: 0.02, pensionAdj: 0.015, careAge: null, careCost: 0 },
    { label: 'No Rentenanpassung', inflation: 0.02, returns: 0.04, pensionAdj: 0, careAge: null, careCost: 0 },
    { label: 'Long-Term Care (from 80)', inflation: 0.02, returns: 0.04, pensionAdj: 0.015, careAge: 80, careCost: 24000 },
    { label: 'Worst Case', inflation: 0.035, returns: 0.02, pensionAdj: 0, careAge: 80, careCost: 24000 },
  ];

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Withdrawal Simulation</h2>
      <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
        30-year simulation at 4% return, 2% inflation. Compare strategies.
      </p>

      <div className="grid grid-cols-3 gap-2 mb-4 px-1 md:px-0">
        <div className="rounded-xl bg-parchment-deep p-3 text-center">
          <p className="text-[11px] text-ink-muted mb-1">Optimal</p>
          <p className="font-serif text-[17px] font-semibold tabular-nums text-sage">{fmt(optTax)}</p>
          <p className="text-[11px] text-ink-muted">lifetime tax</p>
        </div>
        <div className="rounded-xl bg-parchment-deep p-3 text-center">
          <p className="text-[11px] text-ink-muted mb-1">Brokerage First</p>
          <p className="font-serif text-[17px] font-semibold tabular-nums">{fmt(totalTax(brokFirst))}</p>
          <p className="text-[11px] text-ink-muted">lifetime tax</p>
        </div>
        <div className="rounded-xl bg-parchment-deep p-3 text-center">
          <p className="text-[11px] text-ink-muted mb-1">Cash First</p>
          <p className="font-serif text-[17px] font-semibold tabular-nums">{fmt(totalTax(cashFirst))}</p>
          <p className="text-[11px] text-ink-muted">lifetime tax</p>
        </div>
      </div>

      {savings > 0 && (
        <p className="text-[12px] text-sage mb-3 px-1 md:px-0">Optimal strategy saves {fmt(savings)} in lifetime tax vs. worst naive approach.</p>
      )}

      <div className="overflow-x-auto px-1 md:px-0">
        <table className="w-full text-[12px] min-w-[550px]">
          <thead>
            <tr className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] border-b border-divider">
              <th className="text-left py-2 font-medium">Year</th>
              <th className="text-right py-2 font-medium">Need</th>
              <th className="text-right py-2 font-medium">Pension</th>
              <th className="text-right py-2 font-medium">Brokerage</th>
              <th className="text-right py-2 font-medium">Cash</th>
              <th className="text-right py-2 font-medium">Tax</th>
              <th className="text-right py-2 font-medium">Remaining</th>
            </tr>
          </thead>
          <tbody>
            {optimal.filter((_, i) => i < 10 || i % 5 === 0).map(y => (
              <tr key={y.year} className="border-b border-divider">
                <td className="py-1.5 text-ink">{y.year} <span className="text-ink-muted">({y.age})</span></td>
                <td className="py-1.5 text-right tabular-nums">{fmt(y.need)}</td>
                <td className="py-1.5 text-right tabular-nums text-forest">{y.fromPension > 0 ? fmt(y.fromPension) : '—'}</td>
                <td className="py-1.5 text-right tabular-nums">{y.fromBrokerage > 0 ? fmt(y.fromBrokerage) : '—'}</td>
                <td className="py-1.5 text-right tabular-nums">{y.fromCash > 0 ? fmt(y.fromCash) : '—'}</td>
                <td className="py-1.5 text-right tabular-nums text-claret">{fmt(y.taxPaid)}</td>
                <td className={`py-1.5 text-right tabular-nums font-medium ${y.remaining > 0 ? 'text-ink' : 'text-claret'}`}>{fmt(y.remaining)}{y.shortfall > 0 ? <span className="text-claret text-[10px] ml-1">-{fmt(y.shortfall)}</span> : ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {depletionAge(optimal) && (
        <p className="text-[12px] text-claret mt-2 px-1 md:px-0">Portfolio depletes at age {depletionAge(optimal)}.</p>
      )}

      <div className="mt-4 pt-3 border-t border-divider px-1 md:px-0">
        <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Sensitivity Analysis</p>
        <div className="space-y-1">
          {scenarios.map((s, i) => {
            const deplAge = runScenario(s.inflation, s.returns, s.pensionAdj, s.careAge, s.careCost);
            const survives = deplAge === null;
            return (
              <div key={i} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-1.5">
                <span className="text-[12px] text-ink">{s.label}</span>
                <span className={`text-[12px] tabular-nums font-medium ${survives ? 'text-sage' : deplAge && deplAge > 85 ? 'text-amber' : 'text-claret'}`}>
                  {survives ? 'Survives 40y' : `Depletes at ${deplAge}`}
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
