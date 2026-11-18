import { usePlanning } from './PlanningContext';
import { Section, MetricCard, MetricGrid, NumberField, Chip, EmptyState } from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { toNum } from './shared';
import { accountTaxClass, type AccountTaxClass } from './planningMath';
import { PRIVATE_PENSION_EARLIEST_AGE } from './constants';

// German legal terms stay, with a plain-English lead.
const TAX_LABELS: Record<Exclude<AccountTaxClass, 'excluded'>, string> = {
  capital: 'Flat-rate tax on the gain (Abgeltungsteuer, 26,375 %)',
  private_sale: 'Tax-free once held for a year (crypto, gold)',
  bav: 'Income tax at your marginal rate',
  riester: 'Income tax at your marginal rate',
  rurup: 'Income tax on the taxable share (Besteuerungsanteil)',
  cash: 'Tax-free',
};
const LOCKED: AccountTaxClass[] = ['bav', 'riester', 'rurup'];

// Projected per-account value at retirement, with tax-treatment label
// and accessibility check (bAV/Riester/Rürup pay out from 62, checked
// against the retirement age). Brokerage, crypto and gold grow at one rate,
// everything else at another — both editable here (defaults 7 % / 2 %) and
// shared with the Withdrawal Simulation. Loans and property are not sources.
export default function WithdrawalSources() {
  const { accounts, pensionAge, pensionCurrentAge, growthBrokerage, setGrowthBrokerage, growthOther, setGrowthOther } = usePlanning();
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const yearsToRetire = Math.max(0, retireAge - currentAge);
  const brokerageGrowth = toNum(growthBrokerage);
  const otherGrowth = toNum(growthOther);

  const sources = accounts
    .filter(a => a.is_active && (a.balance ?? 0) > 0 && accountTaxClass(a) !== 'excluded')
    .map(a => {
      const cls = accountTaxClass(a) as Exclude<AccountTaxClass, 'excluded'>;
      const currentVal = a.balance ?? 0;
      const isBrokerage = cls === 'capital' || cls === 'private_sale';
      const growth = (isBrokerage ? brokerageGrowth : otherGrowth) / 100;
      const projectedVal = currentVal * Math.pow(1 + growth, yearsToRetire);
      const availableAge = LOCKED.includes(cls) ? PRIVATE_PENSION_EARLIEST_AGE : 0;
      return {
        id: a.id,
        name: a.name,
        isBrokerage,
        taxLabel: TAX_LABELS[cls],
        availableAge,
        projectedValue: Math.round(projectedVal),
        // What matters is whether it is open when you stop working.
        isAccessible: retireAge >= availableAge,
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
            Company, Riester and Rürup pensions pay out from age {PRIVATE_PENSION_EARLIEST_AGE} at the earliest.
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
                      Locked for {src.availableAge - retireAge} {src.availableAge - retireAge === 1 ? 'year' : 'years'} after you retire
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
