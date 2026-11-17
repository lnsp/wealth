import { usePlanning } from './PlanningContext';
import { Section, SubHeading, EmptyState } from '../ui';
import { fmtPct } from '../../utils/format';
import { Assumptions } from './shared';

type Source = { name: string; taxTreatment: string; effectiveRate: number; reason: string };
type Phase = { name: string; ageRange: string; sources: Source[] };

// Tax-optimized draw-down order. Bridge years (retire→67) burn tax-free
// savings + Abgeltungsteuer brokerage before locked accounts unlock. Full
// retirement adds bAV/Riester/Rürup at marginal rate, then gesetzliche
// Rente kicks in at 67 with its own Besteuerungsanteil. Sources within
// each phase are sorted by ascending effective rate.
export default function WithdrawalSequence() {
  const { accounts, pensionAge, pensionCurrentAge, crMarginalRate, fireExpenses } = usePlanning();
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const marginalRate = parseFloat(crMarginalRate) || 42;
  const monthlyNeed = (parseFloat(fireExpenses) || 0) / 12;

  const title = 'Withdrawal Sequence';
  const subtitle = 'The order to draw on your accounts in retirement so that the least tax is paid.';

  if (accounts.length === 0 || monthlyNeed <= 0 || currentAge >= retireAge) {
    return (
      <Section id="withdrawal-sequence" title={title} subtitle={subtitle}>
        <EmptyState
          title="Nothing to sequence yet"
          message={
            accounts.length === 0 ? 'Add an account first; the sequence is built from your active accounts.'
              : monthlyNeed <= 0 ? 'Enter your annual expenses in the FIRE Calculator above to see the withdrawal order.'
                : 'Your retirement age in Your Profile must be higher than your current age.'
          }
          action={{ label: monthlyNeed <= 0 && accounts.length > 0 ? 'Go to the FIRE Calculator' : 'Go to Your Profile', href: monthlyNeed <= 0 && accounts.length > 0 ? '#fire' : '#profile' }}
        />
      </Section>
    );
  }

  const taxEfficiency: Record<string, { rate: number; phase: string }> = {
    savings: { rate: 0, phase: 'bridge' },
    taxable: { rate: 18.5, phase: 'bridge' },          // Abgeltungsteuer w/ Teilfreistellung
    rurup: { rate: marginalRate * 0.8, phase: 'retirement' },
    riester: { rate: marginalRate, phase: 'retirement' },
    bav: { rate: marginalRate, phase: 'retirement' },
  };

  const phases: Phase[] = [];
  if (retireAge < 67) phases.push({ name: 'Bridge years', ageRange: `${retireAge}–67`, sources: [] });
  phases.push({ name: 'Full retirement', ageRange: retireAge < 67 ? '67–80' : `${retireAge}–80`, sources: [] });
  phases.push({ name: 'Late retirement', ageRange: '80 and older', sources: [] });

  const activeAccounts = accounts.filter(a => a.is_active && (a.balance ?? 0) !== 0);
  activeAccounts.forEach(a => {
    // checking/savings accounts default to 'savings' tax treatment (tax-free at
    // withdrawal — already-taxed cash). Without this fallback they'd land in
    // 'taxable' and incorrectly get Abgeltungsteuer applied.
    const tt = a.tax_treatment || (a.type === 'checking' || a.type === 'savings' ? 'savings' : 'taxable');
    const eff = taxEfficiency[tt] || { rate: 26.375, phase: 'bridge' };
    const source: Source = {
      name: a.name, taxTreatment: tt, effectiveRate: eff.rate,
      reason: tt === 'savings' ? 'Tax-free, available immediately'
        : tt === 'taxable' ? 'Flat-rate tax on gains (Abgeltungsteuer), about 18,5 % after the partial exemption for funds (Teilfreistellung)'
        : tt === 'bav' || tt === 'riester' ? `Taxed at your marginal rate (${fmtPct(marginalRate, { digits: 0 })})`
        : tt === 'rurup' ? 'About 80 % taxable (Besteuerungsanteil), at your marginal rate'
        : `About ${fmtPct(eff.rate, { digits: 0 })} effective tax`,
    };
    phases.forEach((phase, pi) => {
      const isRetirementPhase = pi >= (retireAge < 67 ? 1 : 0);
      if (tt === 'savings' || tt === 'taxable') phase.sources.push(source);
      else if ((tt === 'bav' || tt === 'riester' || tt === 'rurup') && isRetirementPhase) phase.sources.push(source);
    });
  });

  const pensionSource: Source = { name: 'Statutory pension', taxTreatment: 'pension', effectiveRate: marginalRate * 0.83, reason: 'About 83 % taxable (Besteuerungsanteil), at your marginal rate' };
  phases.forEach((phase, pi) => {
    const isRetirement = pi >= (retireAge < 67 ? 1 : 0);
    if (isRetirement) phase.sources.push(pensionSource);
  });

  const activePhases = phases.filter(p => p.sources.length > 0);

  return (
    <Section id="withdrawal-sequence" title={title} subtitle={subtitle}>
      <ol className="flex flex-col gap-6">
        {activePhases.map((phase, pi) => (
          <li key={phase.name}>
            <SubHeading>{pi + 1}. {phase.name} <span className="font-normal text-ink-muted">· age {phase.ageRange}</span></SubHeading>
            <ol className="mt-2 border-t border-divider">
              {[...phase.sources]
                .sort((a, b) => a.effectiveRate - b.effectiveRate)
                .map((src, si) => (
                  <li key={`${src.name}-${si}`} className="flex items-baseline justify-between gap-4 border-b border-divider py-2.5">
                    <div className="min-w-0 flex-1">
                      <p className="text-[14px] text-ink break-words">{src.name}</p>
                      <p className="text-[13px] text-ink-muted">{src.reason}</p>
                    </div>
                    <span className="shrink-0 text-right text-[14px] font-medium text-ink">
                      {src.effectiveRate === 0 ? 'No tax' : `about ${fmtPct(src.effectiveRate, { digits: 0 })} tax`}
                    </span>
                  </li>
                ))}
            </ol>
          </li>
        ))}
      </ol>

      <Assumptions>
        <p>Within each phase, the lowest-taxed source is drawn first.</p>
        <p>The statutory pension starts at age 67; company, Riester and Rürup pensions are drawn only once you have retired.</p>
        <p>Tax rates use the {fmtPct(marginalRate, { digits: 0 })} marginal rate from Your Profile; your rate in retirement may be lower.</p>
      </Assumptions>
    </Section>
  );
}
