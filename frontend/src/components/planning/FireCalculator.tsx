import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';

// "Financial Independence" calculator — solves for the FIRE number
// (annual expenses / SWR), shows progress toward it, and projects years
// remaining given current monthlyContrib/return assumptions from projData.
// When projData includes a drawdown schedule (returned when expenses>0),
// renders the retirement-phase chart + tax breakdown below the headline.
export default function FireCalculator() {
  const { totalNetWorth, projData, projContrib, projReturn, fireExpenses, setFireExpenses, fireSWR, setFireSWR, pensionIncome, setPensionIncome, otherIncome, setOtherIncome, fmt } = usePlanning();
  const tc = useThemeColors();

  if (totalNetWorth <= 0) return null;

  const expenses = parseFloat(fireExpenses) || 0;
  const swr = parseFloat(fireSWR) || 3.5;
  const fireNumber = expenses > 0 ? expenses / (swr / 100) : 0;
  const progress = fireNumber > 0 ? Math.min((totalNetWorth / fireNumber) * 100, 100) : 0;
  const monthlyContrib = projData ? (parseFloat(projContrib) || projData.contribution) : 0;
  const annualReturn = projData ? (parseFloat(projReturn) || projData.return_pct) : 7;
  const monthlyReturn = Math.pow(1 + annualReturn / 100, 1 / 12) - 1;

  // Years-to-FIRE: solve for n where FV*(1+r)^n + PMT*((1+r)^n - 1)/r ≥ FIRE.
  // Iterate month-by-month rather than closed-form because PMT may be 0
  // (in which case the algebra divides by zero); 600 = 50yr cap.
  let yearsToFire = 0;
  if (fireNumber > 0 && fireNumber > totalNetWorth && monthlyReturn > 0) {
    let fv = totalNetWorth;
    for (let m = 1; m <= 600; m++) {
      fv = fv * (1 + monthlyReturn) + monthlyContrib;
      if (fv >= fireNumber) { yearsToFire = Math.ceil(m / 12); break; }
    }
    if (yearsToFire === 0) yearsToFire = 50;
  }

  const totalOtherIncome = (parseFloat(pensionIncome) || 0) + (parseFloat(otherIncome) || 0);
  const withdrawalGap = Math.max(expenses - totalOtherIncome, 0);
  const adjustedFireNumber = withdrawalGap > 0 ? withdrawalGap / (swr / 100) : 0;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 id="fire" className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">FIRE Calculator</h2>
      <p className="text-[13px] text-ink-muted mb-3">
        Financial Independence — when investment income covers your expenses.
      </p>

      <div className="flex flex-col sm:flex-row gap-3 mb-4">
        <div className="flex items-center gap-2 flex-1">
          <label className="text-[12px] text-ink-muted shrink-0">Annual expenses</label>
          <input aria-label="Annual expenses" type="number" placeholder="e.g. 36000" value={fireExpenses}
            onChange={e => { setFireExpenses(e.target.value); localStorage.setItem('fire_expenses', e.target.value); }}
            className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-1.5 text-[16px] tabular-nums" />
        </div>
        <div className="flex items-center gap-2">
          <label className="text-[12px] text-ink-muted shrink-0">SWR</label>
          <input aria-label="Safe withdrawal rate" type="number" value={fireSWR} onChange={e => { setFireSWR(e.target.value); localStorage.setItem('fire_swr', e.target.value); }} step="0.5" min="1" max="10"
            className="w-16 rounded-[8px] border border-divider bg-parchment text-ink px-2 py-1.5 text-[16px] tabular-nums text-right" />
          <span className="text-[12px] text-ink-muted">%</span>
        </div>
      </div>

      {expenses > 0 && (
        <div className="flex flex-col sm:flex-row gap-3 mb-4">
          <div className="flex items-center gap-2 flex-1">
            <label className="text-[12px] text-ink-muted shrink-0">Pension (annual)</label>
            <input aria-label="Pension income" type="number" placeholder="e.g. 18000" value={pensionIncome}
              onChange={e => { setPensionIncome(e.target.value); localStorage.setItem('pension_income', e.target.value); }}
              className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-1.5 text-[16px] tabular-nums" />
          </div>
          <div className="flex items-center gap-2 flex-1">
            <label className="text-[12px] text-ink-muted shrink-0">Other income</label>
            <input aria-label="Other income" type="number" placeholder="e.g. 6000" value={otherIncome}
              onChange={e => { setOtherIncome(e.target.value); localStorage.setItem('other_income', e.target.value); }}
              className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-1.5 text-[16px] tabular-nums" />
          </div>
        </div>
      )}

      {fireNumber > 0 && (
        <>
          {totalOtherIncome > 0 && (
            <div className="mb-3 rounded-lg bg-inset border-l-[3px] border-sage p-3">
              <p className="text-[12px] text-ink-muted">
                Expenses {fmt(expenses)} − Income {fmt(totalOtherIncome)} = <span className="font-medium text-ink">Withdrawal gap {fmt(withdrawalGap)}/yr</span>
                {adjustedFireNumber < fireNumber && (
                  <span className="text-sage font-medium ml-1">(FIRE number reduces to {fmt(adjustedFireNumber)})</span>
                )}
              </p>
            </div>
          )}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <div className="rounded-xl bg-parchment-deep p-3 text-center overflow-hidden">
              <p className="text-[12px] text-ink-muted mb-1">FIRE Number</p>
              <p className="font-serif text-[13px] md:text-[20px] font-semibold tabular-nums">{fmt(totalOtherIncome > 0 ? adjustedFireNumber : fireNumber)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Progress</p>
              <p className={`font-serif text-[20px] font-semibold tabular-nums ${progress >= 100 ? 'text-sage' : progress >= 50 ? 'text-forest' : 'text-ink'}`}>
                {progress.toFixed(1)}%
              </p>
              <div className="mt-1.5 h-2 rounded-full bg-divider overflow-hidden">
                <div className={`h-full rounded-full transition-all duration-500 ${progress >= 100 ? 'bg-sage' : 'bg-forest'}`}
                  style={{ width: `${progress}%` }} />
              </div>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Years to FIRE</p>
              <p className={`font-serif text-[20px] font-semibold tabular-nums ${progress >= 100 ? 'text-sage' : 'text-ink'}`}>
                {progress >= 100 ? 'Achieved' : yearsToFire > 0 ? `~${yearsToFire} years` : '...'}
              </p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center overflow-hidden">
              <p className="text-[12px] text-ink-muted mb-1">Remaining</p>
              <p className="font-serif text-[13px] md:text-[20px] font-semibold tabular-nums">
                {progress >= 100 ? fmt(totalNetWorth - fireNumber) : fmt(fireNumber - totalNetWorth)}
              </p>
              <p className="text-[11px] text-ink-muted mt-0.5">
                {progress >= 100 ? 'surplus' : 'to go'}
              </p>
            </div>
          </div>
        </>
      )}

      {projData?.drawdown && (
        <div className="mt-4">
          <p className="text-[15px] font-medium text-ink mb-2">Retirement Phase</p>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
            <div className="rounded-xl bg-inset border-l-[3px] border-sage p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">FIRE Date</p>
              <p className="text-[13px] font-semibold text-sage">{projData.drawdown.fire_date}</p>
            </div>
            <div className="rounded-xl bg-inset border-l-[3px] border-sage p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Portfolio Lasts</p>
              <p className="text-[13px] font-semibold tabular-nums">{projData.drawdown.longevity_years >= 40 ? '40+ years' : `${projData.drawdown.longevity_years} years`}</p>
            </div>
            <div className="rounded-xl bg-inset border-l-[3px] border-sage p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Monthly Withdrawal</p>
              <p className="text-[13px] font-semibold tabular-nums">{fmt(expenses / 12)}</p>
            </div>
            <div className="rounded-xl bg-inset border-l-[3px] border-sage p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Success Rate</p>
              <p className={`text-[13px] font-semibold tabular-nums ${projData.drawdown.success_rate >= 100 ? 'text-sage' : projData.drawdown.success_rate >= 80 ? 'text-amber' : 'text-claret'}`}>
                {projData.drawdown.success_rate}%
              </p>
            </div>
          </div>
          {projData.drawdown.series.length > 2 && (
            <EChartWrapper option={{
              grid: { left: 55, right: 16, top: 16, bottom: 30 },
              xAxis: { type: 'category' as const, data: projData.drawdown.series.map(s => s.date), axisLabel: { fontSize: 11, interval: Math.max(Math.floor(projData.drawdown.series.length / 8) - 1, 0) } },
              yAxis: { type: 'value' as const, axisLabel: { fontSize: 11, formatter: (v: number) => `${(v / 1000).toFixed(0)}K` } },
              series: [{
                type: 'line', data: projData.drawdown.series.map(s => s.value), smooth: true,
                areaStyle: { color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1, colorStops: [{ offset: 0, color: 'rgba(74,106,74,0.3)' }, { offset: 1, color: 'rgba(74,106,74,0.02)' }] } },
                lineStyle: { color: tc.sage, width: 2 }, itemStyle: { color: tc.sage }, symbol: 'none',
              }],
              tooltip: { trigger: 'axis' as const },
            }} height="200px" />
          )}
          {projData.drawdown.tax_breakdown && projData.drawdown.tax_breakdown.length > 0 && (
            <div className="mt-3">
              <p className="text-[12px] text-ink-muted mb-1">
                Tax During Withdrawal · Cumulative: <span className="text-claret font-medium">{fmt(projData.drawdown.cumulative_tax)}</span>
              </p>
              <div className="md:hidden space-y-1.5">
                {projData.drawdown.tax_breakdown.filter((_, i) => i % 5 === 0 || i === (projData.drawdown?.tax_breakdown?.length ?? 1) - 1).map(t => (
                  <div key={t.year} className="rounded-lg bg-parchment-deep px-3 py-1.5">
                    <div className="flex items-baseline justify-between mb-0.5">
                      <span className="text-[12px] font-semibold text-ink">{t.year}</span>
                      <span className="text-[11px] tabular-nums">{t.effective_rate_pct}% eff.</span>
                    </div>
                    <div className="grid grid-cols-3 gap-x-3 text-[11px] tabular-nums">
                      <div><span className="text-ink-muted">Gross</span><br/>{fmt(t.gross_withdrawal)}</div>
                      <div><span className="text-ink-muted">Tax</span><br/><span className="text-claret">{fmt(t.estimated_tax)}</span></div>
                      <div><span className="text-ink-muted">Net</span><br/>{fmt(t.net_received)}</div>
                    </div>
                  </div>
                ))}
              </div>
              <div className="hidden md:block overflow-x-auto">
                <table className="w-full text-[11px]">
                  <thead>
                    <tr className="text-ink-muted border-b border-divider">
                      <th className="px-1.5 py-1 text-left font-medium">Year</th>
                      <th className="px-1.5 py-1 text-right font-medium">Gross</th>
                      <th className="px-1.5 py-1 text-right font-medium">Tax</th>
                      <th className="px-1.5 py-1 text-right font-medium">Net</th>
                      <th className="px-1.5 py-1 text-right font-medium">Rate</th>
                    </tr>
                  </thead>
                  <tbody>
                    {projData.drawdown.tax_breakdown.filter((_, i) => i % 5 === 0 || i === (projData.drawdown?.tax_breakdown?.length ?? 1) - 1).map(t => (
                      <tr key={t.year} className="border-b border-divider">
                        <td className="px-1.5 py-1 tabular-nums">{t.year}</td>
                        <td className="px-1.5 py-1 text-right tabular-nums">{fmt(t.gross_withdrawal)}</td>
                        <td className="px-1.5 py-1 text-right tabular-nums text-claret">{fmt(t.estimated_tax)}</td>
                        <td className="px-1.5 py-1 text-right tabular-nums">{fmt(t.net_received)}</td>
                        <td className="px-1.5 py-1 text-right tabular-nums">{t.effective_rate_pct}%</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>
      )}

      {expenses === 0 && (
        <p className="text-[13px] text-ink-muted">
          Enter your annual expenses to calculate your FIRE number and projected independence date.
        </p>
      )}
    </div>
  );
}
