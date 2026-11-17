import { usePlanning } from './PlanningContext';
import { Section, MetricCard, MetricGrid, NumberField, Chip, EmptyState } from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { toNum } from './shared';

// German legal terms stay, with a plain-English lead.
const TAX_LABELS: Record<string, string> = {
  taxable: 'Flat-rate tax on gains (Abgeltungsteuer, 26,375 %)',
  bav: 'Income tax at your marginal rate',
  riester: 'Income tax at your marginal rate',
  rurup: 'Income tax on the taxable share (Besteuerungsanteil)',
  savings: 'Tax-free',
};
const AVAILABILITY_AGE: Record<string, number> = {
  taxable: 0, bav: 62, riester: 62, rurup: 62, savings: 0,
};

// Projected per-account value at retirement, with tax-treatment label
// and accessibility check (bAV/Riester/Rürup are locked until 62).
// Brokerage accounts grow at one rate, everything else at another — both
// editable here (defaults 7 % / 2 %) and shared with the Withdrawal Simulation.
export default function WithdrawalSources() {
  const { accounts, pensionAge, pensionCurrentAge, growthBrokerage, setGrowthBrokerage, growthOther, setGrowthOther } = usePlanning();
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const yearsToRetire = Math.max(0, retireAge - currentAge);
  const brokerageGrowth = toNum(growthBrokerage);
  const otherGrowth = toNum(growthOther);

  const sources = accounts
    .filter(a => a.is_active && (a.balance ?? 0) > 0)
    .map(a => {
      const currentVal = a.balance ?? 0;
      const growth = (a.type === 'brokerage' ? brokerageGrowth : otherGrowth) / 100;
      const projectedVal = currentVal * Math.pow(1 + growth, yearsToRetire);
      const availableAge = AVAILABILITY_AGE[a.tax_treatment || 'taxable'] || 0;
      return {
        id: a.id,
        name: a.name,
        isBrokerage: a.type === 'brokerage',
        taxLabel: TAX_LABELS[a.tax_treatment || 'taxable'] || 'Flat-rate tax on gains (Abgeltungsteuer)',
        availableAge,
        projectedValue: Math.round(projectedVal),
        isAccessible: currentAge >= availableAge,
      };
    })
    .sort((a, b) => b.projectedValue - a.projectedValue);

  const totalProjected = sources.reduce((s, src) => s + src.projectedValue, 0);

  return (
    <Section
      id="withdrawal-sources"
      title="Withdrawal Sources"
      subtitle={`What each account could be worth at retirement (age ${retireAge}) and how withdrawals from it are taxed.`}
    >
      {sources.length === 0 ? (
        <EmptyState
          title="No accounts with a balance"
          message="Withdrawal sources are projected from your active accounts. Add an account or import transactions first."
          action={{ label: 'Go to Settings', href: '/settings' }}
        />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-x-3 gap-y-4 md:grid-cols-4">
            <NumberField label="Brokerage growth" unit="% p.a." value={brokerageGrowth} onValueChange={v => setGrowthBrokerage(String(v))} />
            <NumberField label="Other accounts growth" unit="% p.a." value={otherGrowth} onValueChange={v => setGrowthOther(String(v))} />
          </div>
          <p className="mt-2 text-[13px] text-ink-muted">
            Assumes today’s balances grow at these rates for {yearsToRetire} {yearsToRetire === 1 ? 'year' : 'years'} with no further deposits.
            Company, Riester and Rürup pensions are treated as locked until age 62.
          </p>

          <ul className="mt-4 border-t border-divider">
            {sources.map(src => (
              <li key={src.id} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-divider py-3">
                <div className="min-w-0 flex-1 basis-48">
                  <p className="text-[14px] font-medium text-ink break-words">{src.name}</p>
                  <p className="text-[13px] text-ink-muted">
                    {src.taxLabel} · {src.availableAge > 0 ? `available from age ${src.availableAge}` : 'available anytime'} · grows at {fmtPct(src.isBrokerage ? brokerageGrowth : otherGrowth)}
                  </p>
                  {!src.isAccessible && (
                    <Chip tone="warning" className="mt-1.5">
                      Locked for {src.availableAge - currentAge} more {src.availableAge - currentAge === 1 ? 'year' : 'years'}
                    </Chip>
                  )}
                </div>
                <span className="font-serif text-[18px] text-ink" data-privacy="blur">{fmtEUR(src.projectedValue)}</span>
              </li>
            ))}
          </ul>

          <MetricGrid className="mt-4">
            <MetricCard
              className="col-span-2"
              label="Total projected at retirement"
              value={fmtEUR(totalProjected)}
              sublabel={`At age ${retireAge}, before tax on withdrawals`}
            />
          </MetricGrid>
        </>
      )}
    </Section>
  );
}
