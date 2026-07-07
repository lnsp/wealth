import { usePlanning } from './PlanningContext';

const TAX_LABELS: Record<string, string> = {
  taxable: 'Abgeltungssteuer (26.375%)',
  bav: 'Einkommensteuer (marginal rate)',
  riester: 'Einkommensteuer (marginal rate)',
  rurup: 'Besteuerungsanteil (by retirement year)',
  savings: 'Tax-free',
};
const AVAILABILITY_AGE: Record<string, number> = {
  taxable: 0, bav: 62, riester: 62, rurup: 62, savings: 0,
};

// Projected per-account value at retirement, with tax-treatment label
// and accessibility check (bAV/Riester/Rürup are locked until 62).
// Brokerage accounts grow at 7%, everything else at 2% — matches the
// optimistic/cash split used by the Withdrawal Simulation.
export default function WithdrawalSources() {
  const { accounts, pensionAge, pensionCurrentAge, fmt } = usePlanning();
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const yearsToRetire = Math.max(0, retireAge - currentAge);

  if (accounts.length === 0) return null;

  const sources = accounts
    .filter(a => a.is_active && (a.balance ?? 0) > 0)
    .map(a => {
      const currentVal = a.balance ?? 0;
      const growth = a.type === 'brokerage' ? 0.07 : 0.02;
      const projectedVal = currentVal * Math.pow(1 + growth, yearsToRetire);
      return {
        name: a.name,
        taxLabel: TAX_LABELS[a.tax_treatment || 'taxable'] || 'Abgeltungssteuer',
        availableAge: AVAILABILITY_AGE[a.tax_treatment || 'taxable'] || 0,
        projectedValue: Math.round(projectedVal),
        isAccessible: currentAge >= (AVAILABILITY_AGE[a.tax_treatment || 'taxable'] || 0),
      };
    })
    .sort((a, b) => b.projectedValue - a.projectedValue);

  if (sources.length === 0) return null;
  const totalProjected = sources.reduce((s, src) => s + src.projectedValue, 0);

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Withdrawal Sources</h2>
      <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
        Projected account values at retirement (age {retireAge}) with tax treatment for withdrawals.
      </p>

      <div className="space-y-1.5 px-1 md:px-0">
        {sources.map((src, i) => (
          <div key={i} className="rounded-xl bg-parchment-deep p-3">
            <div className="flex items-center justify-between mb-1">
              <span className="text-[14px] font-medium text-ink">{src.name}</span>
              <span className="font-serif text-[15px] tabular-nums font-medium text-ink">{fmt(src.projectedValue)}</span>
            </div>
            <div className="flex items-center justify-between text-[11px] text-ink-muted">
              <span>{src.taxLabel}</span>
              <span>{src.availableAge > 0 ? `Available from age ${src.availableAge}` : 'Anytime'}</span>
            </div>
            {!src.isAccessible && (
              <p className="text-[11px] text-amber mt-1">Locked until age {src.availableAge} ({src.availableAge - currentAge} years)</p>
            )}
          </div>
        ))}
      </div>

      <div className="flex items-center justify-between mt-3 pt-2 border-t border-divider px-1 md:px-0">
        <span className="text-[13px] text-ink-muted">Total Projected at Retirement</span>
        <span className="font-serif text-[17px] font-semibold tabular-nums text-ink">{fmt(totalProjected)}</span>
      </div>
    </div>
  );
}
