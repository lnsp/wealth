import { usePlanning } from './PlanningContext';
import { Section, SubHeading, EmptyState } from '../ui';
import { fmtPct } from '../../utils/format';
import { Assumptions } from './shared';
import { accountTaxClass, besteuerungsanteil, capitalIncomeTaxRate, parsePct, type AccountTaxClass } from './planningMath';
import { DEFAULT_MARGINAL_RATE_PCT, PRIVATE_PENSION_EARLIEST_AGE, REGELALTERSGRENZE, TFS_EQUITY_FUND } from './constants';

type Source = { name: string; taxTreatment: string; effectiveRate: number; reason: string };
type Phase = { name: string; ageRange: string; sources: Source[] };

// Tax-optimized draw-down order. Bridge years (retire→67) burn tax-free
// cash + Abgeltungsteuer brokerage before locked accounts unlock. Full
// retirement adds bAV/Riester/Rürup at the marginal rate, then the statutory
// pension starts at 67. The taxable shares of statutory and Rürup pensions
// follow the year payments start (§ 22 EStG table). Sources within each phase
// are sorted by ascending effective rate.
export default function WithdrawalSequence() {
  const { accounts, pensionAge, pensionCurrentAge, crMarginalRate, fireExpenses, churchRate } = usePlanning();
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const marginalRate = parsePct(crMarginalRate, DEFAULT_MARGINAL_RATE_PCT);
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

  const thisYear = new Date().getFullYear();
  const statutoryShare = besteuerungsanteil(thisYear + (REGELALTERSGRENZE - currentAge));
  const rurupShare = besteuerungsanteil(thisYear + (Math.max(retireAge, PRIVATE_PENSION_EARLIEST_AGE) - currentAge));
  const fundGainRate = capitalIncomeTaxRate(churchRate) * (1 - TFS_EQUITY_FUND) * 100;
  const pct = (v: number) => fmtPct(v, { digits: 0 });
  const RATES: Record<Exclude<AccountTaxClass, 'excluded'>, { rate: number; reason: string }> = {
    cash: { rate: 0, reason: 'Tax-free, available immediately' },
    private_sale: { rate: 0, reason: 'Crypto and gold: tax-free once held for a year' },
    capital: { rate: fundGainRate, reason: `Flat-rate tax on the gain only (Abgeltungsteuer), about ${pct(fundGainRate)} of the gain after the partial exemption for funds` },
    rurup: { rate: marginalRate * rurupShare, reason: `${pct(rurupShare * 100)} taxable (Besteuerungsanteil), at your marginal rate` },
    riester: { rate: marginalRate, reason: `Fully taxable, at your marginal rate (${pct(marginalRate)})` },
    bav: { rate: marginalRate, reason: `Fully taxable, at your marginal rate (${pct(marginalRate)}), plus health insurance` },
  };

  const phases: Phase[] = [];
  if (retireAge < 67) phases.push({ name: 'Bridge years', ageRange: `${retireAge}–67`, sources: [] });
  phases.push({ name: 'Full retirement', ageRange: retireAge < 67 ? '67–80' : `${retireAge}–80`, sources: [] });
  phases.push({ name: 'Late retirement', ageRange: '80 and older', sources: [] });

  // Loans, credit cards, property and accounts below zero are not sources.
  const activeAccounts = accounts.filter(a => a.is_active && (a.balance ?? 0) > 0 && accountTaxClass(a) !== 'excluded');
  activeAccounts.forEach(a => {
    // Cash is keyed on the account type: tax_treatment defaults to 'taxable' for checking accounts too.
    const cls = accountTaxClass(a) as Exclude<AccountTaxClass, 'excluded'>;
    const eff = RATES[cls];
    const source: Source = { name: a.name, taxTreatment: cls, effectiveRate: eff.rate, reason: eff.reason };
    const locked = cls === 'bav' || cls === 'riester' || cls === 'rurup';
    phases.forEach((phase, pi) => {
      const isRetirementPhase = pi >= (retireAge < 67 ? 1 : 0);
      if (!locked || isRetirementPhase) phase.sources.push(source);
    });
  });

  const pensionSource: Source = { name: 'Statutory pension', taxTreatment: 'pension', effectiveRate: marginalRate * statutoryShare, reason: `${pct(statutoryShare * 100)} taxable (Besteuerungsanteil for a ${thisYear + (REGELALTERSGRENZE - currentAge)} start), at your marginal rate` };
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
        <p>Within each phase, the lowest-taxed source is drawn first. Brokerage tax applies to the gain in each sale only, pension tax to the whole payout.</p>
        <p>The statutory pension starts at age 67; company, Riester and Rürup pensions are drawn only once you have retired.</p>
        <p>Tax rates use the {fmtPct(marginalRate, { digits: 0 })} marginal rate from Your Profile; your rate in retirement may be lower.</p>
      </Assumptions>
    </Section>
  );
}
