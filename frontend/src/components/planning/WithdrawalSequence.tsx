import { usePlanning } from './PlanningContext';

type Phase = { name: string; ageRange: string; sources: { name: string; taxTreatment: string; effectiveRate: number; reason: string }[] };

// Tax-optimized draw-down order. Bridge years (retire→67) burn tax-free
// savings + Abgeltungssteuer brokerage before locked accounts unlock. Full
// retirement adds bAV/Riester/Rürup at marginal rate, then gesetzliche
// Rente kicks in at 67 with its own Besteuerungsanteil. Sources within
// each phase are sorted by ascending effective rate.
export default function WithdrawalSequence() {
  const { accounts, pensionAge, pensionCurrentAge, crMarginalRate } = usePlanning();
  const retireAge = pensionAge;
  const currentAge = pensionCurrentAge;
  const marginalRate = parseFloat(crMarginalRate) || 42;
  const monthlyNeed = parseFloat(localStorage.getItem('fire_expenses') || '0') / 12;

  if (accounts.length === 0 || monthlyNeed <= 0 || currentAge >= retireAge) return null;

  const taxEfficiency: Record<string, { rate: number; phase: string }> = {
    savings: { rate: 0, phase: 'bridge' },
    taxable: { rate: 18.5, phase: 'bridge' },          // Abgeltungssteuer w/ Teilfreistellung
    rurup: { rate: marginalRate * 0.8, phase: 'retirement' },
    riester: { rate: marginalRate, phase: 'retirement' },
    bav: { rate: marginalRate, phase: 'retirement' },
  };

  const phases: Phase[] = [];
  if (retireAge < 67) phases.push({ name: 'Bridge Years', ageRange: `${retireAge}–67`, sources: [] });
  phases.push({ name: 'Full Retirement', ageRange: retireAge < 67 ? '67–80' : `${retireAge}–80`, sources: [] });
  phases.push({ name: 'Late Retirement', ageRange: '80+', sources: [] });

  const activeAccounts = accounts.filter(a => a.is_active && (a.balance ?? 0) !== 0);
  activeAccounts.forEach(a => {
    // checking/savings accounts default to 'savings' tax treatment (tax-free at
    // withdrawal — already-taxed cash). Without this fallback they'd land in
    // 'taxable' and incorrectly get Abgeltungssteuer applied.
    const tt = a.tax_treatment || (a.type === 'checking' || a.type === 'savings' ? 'savings' : 'taxable');
    const eff = taxEfficiency[tt] || { rate: 26.375, phase: 'bridge' };
    const source = {
      name: a.name, taxTreatment: tt, effectiveRate: eff.rate,
      reason: tt === 'savings' ? 'Tax-free, immediate access'
        : tt === 'taxable' ? 'Abgeltungssteuer ~18.5% (with Teilfreistellung)'
        : tt === 'bav' || tt === 'riester' ? `Taxed at marginal rate (${marginalRate}%)`
        : tt === 'rurup' ? 'Besteuerungsanteil ~80% at marginal rate'
        : `${eff.rate.toFixed(0)}% effective`,
    };
    phases.forEach((phase, pi) => {
      const isRetirementPhase = pi >= (retireAge < 67 ? 1 : 0);
      if (tt === 'savings' || tt === 'taxable') phase.sources.push(source);
      else if ((tt === 'bav' || tt === 'riester' || tt === 'rurup') && isRetirementPhase) phase.sources.push(source);
    });
  });

  const pensionSource = { name: 'Gesetzliche Rente', taxTreatment: 'pension', effectiveRate: marginalRate * 0.83, reason: 'Besteuerungsanteil ~83% (2026 retirement)' };
  phases.forEach((phase, pi) => {
    const isRetirement = pi >= (retireAge < 67 ? 1 : 0);
    if (isRetirement) phase.sources.push(pensionSource);
  });

  const activePhases = phases.filter(p => p.sources.length > 0);

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Withdrawal Sequence</h2>
      <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
        Tax-optimized draw-down order to minimize lifetime tax burden.
      </p>

      <div className="space-y-4 px-1 md:px-0">
        {activePhases.map((phase, pi) => (
          <div key={pi}>
            <div className="flex items-center gap-2 mb-2">
              <span className="w-6 h-6 rounded-full bg-parchment-deep border border-forest text-forest flex items-center justify-center text-[11px] font-semibold shrink-0">{pi + 1}</span>
              <div>
                <p className="text-[13px] font-medium text-ink">{phase.name}</p>
                <p className="text-[11px] text-ink-muted">Age {phase.ageRange}</p>
              </div>
            </div>
            <div className="space-y-1 ml-8">
              {phase.sources
                .sort((a, b) => a.effectiveRate - b.effectiveRate)
                .map((src, si) => (
                  <div key={si} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-1.5">
                    <div className="min-w-0 flex-1">
                      <p className="text-[12px] text-ink">{src.name}</p>
                      <p className="text-[11px] text-ink-muted">{src.reason}</p>
                    </div>
                    <span className={`text-[12px] tabular-nums font-medium shrink-0 ml-3 ${src.effectiveRate < 20 ? 'text-sage' : src.effectiveRate < 35 ? 'text-amber' : 'text-claret'}`}>
                      ~{src.effectiveRate.toFixed(0)}%
                    </span>
                  </div>
                ))}
            </div>
          </div>
        ))}
      </div>

      <p className="text-[11px] text-ink-muted mt-3 px-1 md:px-0">
        Strategy: Exhaust lower-tax sources first in bridge years, then draw from tax-deferred accounts when marginal rate is lower in retirement.
      </p>
    </div>
  );
}
