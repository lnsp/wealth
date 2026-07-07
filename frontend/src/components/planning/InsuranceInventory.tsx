import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';

type Policy = { type: string; provider: string; annual_cost: number; coverage: number; renewal_date: string };

const POLICY_TYPES = ['BU', 'Risikoleben', 'Haftpflicht', 'Hausrat', 'Rechtsschutz', 'KFZ', 'KV', 'Unfall'];

// Policy roster + Coverage Analysis (Protection Score, gap detection, and
// over-insurance warnings). Bundled in one component because the Gap
// Analysis ingests the same policies array — splitting them would require
// hoisting the array into context and is not worth it.
export default function InsuranceInventory() {
  const { crGrossIncome, crMarginalRate, fmt } = usePlanning();
  const tc = useThemeColors();
  const [insurancePolicies, setInsurancePolicies] = useState<Policy[]>(() => {
    try { return JSON.parse(localStorage.getItem('insurance_policies') || '[]'); } catch { return []; }
  });

  const persist = (updated: Policy[]) => {
    setInsurancePolicies(updated);
    localStorage.setItem('insurance_policies', JSON.stringify(updated));
  };

  return (
    <>
      {/* Insurance Inventory */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Insurance Inventory</h2>
        <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
          Track your insurance policies, annual costs, and renewal dates.
        </p>

        <div className="flex flex-wrap gap-1.5 mb-4 px-1 md:px-0">
          {POLICY_TYPES.map(type => (
            <button
              key={type}
              onClick={() => persist([...insurancePolicies, { type, provider: '', annual_cost: 0, coverage: 0, renewal_date: '' }])}
              className="apple-btn-secondary text-[12px] px-3 py-2"
            >
              + {type}
            </button>
          ))}
        </div>

        {insurancePolicies.length > 0 && (
          <div className="space-y-2 px-1 md:px-0">
            {insurancePolicies.map((pol, idx) => (
              <div key={idx} className="rounded-xl bg-parchment-deep p-3">
                <div className="flex items-center justify-between mb-2">
                  <span className="text-[14px] font-medium text-ink">{pol.type}</span>
                  <button onClick={() => persist(insurancePolicies.filter((_, i) => i !== idx))} className="text-[12px] text-claret">Remove</button>
                </div>
                <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                  <div>
                    <label className="text-[11px] text-ink-muted block mb-0.5">Provider</label>
                    <input aria-label="Provider" type="text" value={pol.provider} onChange={e => {
                      const updated = [...insurancePolicies];
                      updated[idx] = { ...pol, provider: e.target.value };
                      persist(updated);
                    }} placeholder="e.g. Allianz" className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px]" />
                  </div>
                  <div>
                    <label className="text-[11px] text-ink-muted block mb-0.5">Annual Cost</label>
                    <input aria-label="Annual cost" type="number" value={pol.annual_cost || ''} onChange={e => {
                      const updated = [...insurancePolicies];
                      updated[idx] = { ...pol, annual_cost: parseFloat(e.target.value) || 0 };
                      persist(updated);
                    }} placeholder="EUR/year" className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                  </div>
                  <div>
                    <label className="text-[11px] text-ink-muted block mb-0.5">Coverage</label>
                    <input aria-label="Coverage amount" type="number" value={pol.coverage || ''} onChange={e => {
                      const updated = [...insurancePolicies];
                      updated[idx] = { ...pol, coverage: parseFloat(e.target.value) || 0 };
                      persist(updated);
                    }} placeholder="EUR" className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                  </div>
                  <div>
                    <label className="text-[11px] text-ink-muted block mb-0.5">Renewal</label>
                    <input aria-label="Renewal date" type="date" value={pol.renewal_date} onChange={e => {
                      const updated = [...insurancePolicies];
                      updated[idx] = { ...pol, renewal_date: e.target.value };
                      persist(updated);
                    }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px]" />
                  </div>
                </div>
              </div>
            ))}

            <div className="flex items-center justify-between pt-2 border-t border-divider">
              <span className="text-[13px] text-ink-muted">Total Annual Cost</span>
              <span className="font-serif text-[17px] font-semibold tabular-nums text-ink">
                {fmt(insurancePolicies.reduce((s, p) => s + (p.annual_cost || 0), 0))}
              </span>
            </div>
          </div>
        )}

        {insurancePolicies.length === 0 && (
          <p className="text-[13px] text-ink-muted px-1 md:px-0">
            Add your insurance policies to track costs and identify coverage gaps.
          </p>
        )}
      </div>

      {/* Coverage Analysis */}
      {insurancePolicies.length > 0 && (() => {
        const grossIncome = parseFloat(crGrossIncome) || 0;
        const netIncome = grossIncome * (1 - (parseFloat(crMarginalRate) || 42) / 100);

        const gapRules: { type: string; label: string; need: number; unit: string; explanation: string }[] = [
          { type: 'BU', label: 'Berufsunfähigkeit', need: netIncome * 0.75 / 12, unit: '/mo', explanation: '75% of net monthly income' },
          { type: 'Risikoleben', label: 'Risikolebensversicherung', need: grossIncome * 3, unit: '', explanation: '3x gross annual income' },
          { type: 'Haftpflicht', label: 'Privathaftpflicht', need: 10_000_000, unit: '', explanation: 'Minimum 10M EUR coverage' },
          { type: 'Hausrat', label: 'Hausratversicherung', need: 50_000, unit: '', explanation: 'Typical household contents value' },
          { type: 'KV', label: 'Krankenversicherung', need: 1, unit: '', explanation: 'Mandatory coverage required' },
        ];

        const analysis = gapRules.map(rule => {
          const policies = insurancePolicies.filter(p => p.type === rule.type);
          const hasCoverage = policies.length > 0;
          const totalCoverage = policies.reduce((s, p) => s + (p.coverage || 0), 0);
          const annualCost = policies.reduce((s, p) => s + (p.annual_cost || 0), 0);

          let status: 'adequate' | 'partial' | 'missing' = 'missing';
          if (hasCoverage && rule.need > 1 && totalCoverage >= rule.need * 0.8) status = 'adequate';
          else if (hasCoverage && rule.need > 1 && totalCoverage >= rule.need * 0.5) status = 'partial';
          else if (hasCoverage && rule.need <= 1) status = 'adequate'; // binary check (KV)
          else if (hasCoverage) status = 'partial';

          const gap = rule.need > 1 ? Math.max(0, rule.need - totalCoverage) : 0;
          return { ...rule, hasCoverage, totalCoverage, annualCost, status, gap };
        });

        const adequate = analysis.filter(a => a.status === 'adequate').length;
        const total = analysis.length;
        const protectionScore = analysis.reduce((s: number, a) => s + (a.status === 'adequate' ? 20 : a.status === 'partial' ? 10 : 0), 0);
        const scoreColor = protectionScore >= 80 ? 'text-sage' : protectionScore >= 50 ? 'text-amber' : 'text-claret';
        const ringColor = protectionScore >= 80 ? tc.sage : protectionScore >= 50 ? tc.gold : tc.claret;

        const hasType = (t: string) => insurancePolicies.some(p => p.type === t);
        const warnings: { title: string; detail: string }[] = [];
        if (hasType('Unfall') && hasType('BU')) {
          warnings.push({ title: 'Redundant Unfallversicherung', detail: 'With adequate BU coverage, Unfallversicherung provides little additional benefit. Consider cancelling to save ' + fmt(insurancePolicies.filter(p => p.type === 'Unfall').reduce((s, p) => s + p.annual_cost, 0)) + '/year.' });
        }
        if (hasType('Hausrat')) {
          const hausratCoverage = insurancePolicies.filter(p => p.type === 'Hausrat').reduce((s, p) => s + p.coverage, 0);
          if (hausratCoverage > 100_000) {
            warnings.push({ title: 'Excessive Hausrat coverage', detail: `${fmt(hausratCoverage)} coverage may exceed actual household contents value. Review whether the sum insured reflects reality.` });
          }
        }
        if (hasType('Rechtsschutz')) {
          const rechtsschutzCost = insurancePolicies.filter(p => p.type === 'Rechtsschutz').reduce((s, p) => s + p.annual_cost, 0);
          if (rechtsschutzCost > 500) {
            warnings.push({ title: 'Expensive Rechtsschutz', detail: `${fmt(rechtsschutzCost)}/year is above average. Compare quotes or check if employer provides legal coverage.` });
          }
        }

        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Coverage Analysis</h2>

            <div className="flex items-center gap-4 mb-4 px-1 md:px-0">
              <div className="relative w-16 h-16 shrink-0">
                <svg viewBox="0 0 36 36" className="w-16 h-16 -rotate-90">
                  <circle cx="18" cy="18" r="15.5" fill="none" stroke={tc.divider} strokeWidth="3" />
                  <circle cx="18" cy="18" r="15.5" fill="none" stroke={ringColor} strokeWidth="3" strokeDasharray={`${protectionScore * 0.974} 100`} strokeLinecap="round" />
                </svg>
                <span className={`absolute inset-0 flex items-center justify-center font-serif text-[18px] font-semibold ${scoreColor}`}>{protectionScore}</span>
              </div>
              <div>
                <p className="text-[14px] font-medium text-ink">Protection Score</p>
                <p className="text-[12px] text-ink-muted">{adequate}/{total} essential coverages adequate{grossIncome <= 0 ? ' — enter gross income for accurate thresholds' : ''}</p>
              </div>
            </div>

            <div className="space-y-1.5 px-1 md:px-0">
              {analysis.map(a => (
                <div key={a.type} className="flex items-center justify-between rounded-xl bg-parchment-deep px-3 py-2.5">
                  <div className="flex items-center gap-2.5 min-w-0 flex-1">
                    <span className={`w-2.5 h-2.5 rounded-full shrink-0 ${a.status === 'adequate' ? 'bg-sage' : a.status === 'partial' ? 'bg-amber' : 'bg-claret'}`} />
                    <div className="min-w-0">
                      <p className="text-[14px] text-ink">{a.label}</p>
                      <p className="text-[11px] text-ink-muted">{a.explanation}</p>
                    </div>
                  </div>
                  <div className="text-right shrink-0 ml-3">
                    {a.hasCoverage ? (
                      <>
                        <p className={`text-[13px] font-medium tabular-nums ${a.status === 'adequate' ? 'text-sage' : a.status === 'partial' ? 'text-amber' : 'text-claret'}`}>
                          {a.status === 'adequate' ? 'Adequate' : a.status === 'partial' ? 'Partial' : 'Low'}
                        </p>
                        {a.gap > 0 && <p className="text-[11px] text-claret tabular-nums">gap: {fmt(a.gap)}{a.unit}</p>}
                      </>
                    ) : (
                      <p className="text-[13px] font-medium text-claret">Missing</p>
                    )}
                  </div>
                </div>
              ))}
            </div>

            {warnings.length > 0 && (
              <div className="mt-4 px-1 md:px-0">
                <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Over-Insurance Warnings</p>
                <div className="space-y-1.5">
                  {warnings.map((w, i) => (
                    <div key={i} className="flex items-start gap-2.5 rounded-xl bg-inset border-l-[3px] border-amber px-3 py-2.5">
                      <span className="w-2.5 h-2.5 rounded-full bg-amber shrink-0 mt-1" />
                      <div>
                        <p className="text-[13px] font-medium text-ink">{w.title}</p>
                        <p className="text-[12px] text-ink-muted mt-0.5">{w.detail}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        );
      })()}
    </>
  );
}
