import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';

// Human Capital = present value of future earnings, discounted at 3% real
// and grown at 2% nominal salary growth. The crossover chart shows when
// financial capital (current NW + contributions @7% real) surpasses the
// remaining human-capital stream — typically late-40s for a typical career.
export default function HumanCapital() {
  const { totalNetWorth, crGrossIncome, setCrGrossIncome, pensionCurrentAge, setPensionCurrentAge, pensionAge, crBudget, fmt } = usePlanning();
  const tc = useThemeColors();

  const grossIncome = parseFloat(crGrossIncome) || 0;
  const currentAge = pensionCurrentAge;
  const retireAge = pensionAge;
  const yearsToRetire = Math.max(0, retireAge - currentAge);
  const discountRate = 0.03;
  const growthRate = 0.02;
  const monthlyContrib = parseFloat(crBudget) || 0;

  let humanCapital = 0;
  if (grossIncome > 0 && yearsToRetire > 0) {
    for (let y = 1; y <= yearsToRetire; y++) {
      humanCapital += grossIncome * Math.pow(1 + growthRate, y) / Math.pow(1 + discountRate, y);
    }
    humanCapital = Math.round(humanCapital);
  }

  const financialCapital = totalNetWorth;
  const crossoverData: { age: number; human: number; financial: number }[] = [];
  if (grossIncome > 0 && yearsToRetire > 0) {
    let projFinancial = financialCapital;
    for (let y = 0; y <= yearsToRetire; y++) {
      let remainingHuman = 0;
      for (let r = y + 1; r <= yearsToRetire; r++) {
        remainingHuman += grossIncome * Math.pow(1 + growthRate, r) / Math.pow(1 + discountRate, r - y);
      }
      crossoverData.push({ age: currentAge + y, human: Math.round(remainingHuman), financial: Math.round(projFinancial) });
      projFinancial = (projFinancial + monthlyContrib * 12) * 1.07;
    }
  }
  const crossoverAge = crossoverData.find(d => d.financial >= d.human)?.age;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Human Capital</h2>
      <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
        Present value of your future earnings compared to financial capital.
      </p>

      <div className="grid grid-cols-2 gap-2 mb-4 px-1 md:px-0">
        <div>
          <label className="text-[11px] text-ink-muted block mb-1">Gross Annual Income</label>
          <input aria-label="Gross annual income" type="number" value={crGrossIncome} onChange={e => { setCrGrossIncome(e.target.value); localStorage.setItem('gross_annual_income', e.target.value); }} placeholder="e.g. 60000" className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
        <div>
          <label className="text-[11px] text-ink-muted block mb-1">Current Age</label>
          <input aria-label="Current age" type="number" value={pensionCurrentAge} onChange={e => { setPensionCurrentAge(parseInt(e.target.value) || 35); localStorage.setItem('pension_current_age', e.target.value); }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
      </div>

      {grossIncome > 0 && yearsToRetire > 0 && (
        <>
          <div className="grid grid-cols-2 md:grid-cols-3 gap-2 mb-4 px-1 md:px-0">
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Human Capital</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-forest">{fmt(humanCapital)}</p>
              <p className="text-[11px] text-ink-muted mt-0.5">{yearsToRetire} years to retirement</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Financial Capital</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-gold">{fmt(financialCapital)}</p>
              <p className="text-[11px] text-ink-muted mt-0.5">current net worth</p>
            </div>
            {crossoverAge && (
              <div className="rounded-xl bg-parchment-deep p-3 text-center">
                <p className="text-[12px] text-ink-muted mb-1">Crossover Age</p>
                <p className="font-serif text-[20px] font-semibold tabular-nums">{crossoverAge}</p>
                <p className="text-[11px] text-ink-muted mt-0.5">financial surpasses human</p>
              </div>
            )}
          </div>

          <EChartWrapper option={{
            tooltip: { trigger: 'axis' as const },
            legend: { data: ['Human Capital', 'Financial Capital'], bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
            xAxis: { type: 'category' as const, data: crossoverData.map(d => `${d.age}`), axisLabel: { fontSize: 11, color: tc.inkMuted }, axisLine: { show: false }, axisTick: { show: false } },
            yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => v >= 1000000 ? `${(v / 1000000).toFixed(1)}M` : `${(v / 1000).toFixed(0)}k`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
            series: [
              { name: 'Human Capital', type: 'line' as const, data: crossoverData.map(d => d.human), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.forest, width: 2 }, areaStyle: { opacity: 0.08, color: tc.forest } },
              { name: 'Financial Capital', type: 'line' as const, data: crossoverData.map(d => d.financial), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.gold, width: 2 }, areaStyle: { opacity: 0.08, color: tc.gold } },
            ],
            grid: { left: 55, right: 16, top: 10, bottom: 40 },
          }} height="250px" />
        </>
      )}
    </div>
  );
}
