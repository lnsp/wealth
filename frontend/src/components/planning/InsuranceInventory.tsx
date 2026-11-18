import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import {
  Section, MetricCard, MetricGrid, Field, NumberField, AddChip, Chip, ProgressBar,
  EmptyState, useConfirm,
} from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { Assumptions, ItemCard, RemoveButton, readStored, writeStored } from './shared';
import { parsePct } from './planningMath';
import { DEFAULT_MARGINAL_RATE_PCT } from './constants';

type Policy = { type: string; provider: string; annual_cost: number; coverage: number; renewal_date: string };

// Stored `type` keys stay as they are (existing saved policies use them); the UI shows an
// English name with the German term as a gloss.
const POLICY_TYPES: { type: string; label: string }[] = [
  { type: 'BU', label: 'Disability (Berufsunfähigkeit)' },
  { type: 'Risikoleben', label: 'Term life (Risikoleben)' },
  { type: 'Haftpflicht', label: 'Personal liability (Haftpflicht)' },
  { type: 'Hausrat', label: 'Home contents (Hausrat)' },
  { type: 'Rechtsschutz', label: 'Legal expenses (Rechtsschutz)' },
  { type: 'KFZ', label: 'Car (Kfz)' },
  { type: 'KV', label: 'Health (Krankenversicherung)' },
  { type: 'Unfall', label: 'Accident (Unfall)' },
];
const policyLabel = (type: string) => POLICY_TYPES.find(p => p.type === type)?.label ?? type;

const STATUS = {
  adequate: { tone: 'success' as const, text: 'Adequate' },
  partial: { tone: 'warning' as const, text: 'Partial' },
  missing: { tone: 'danger' as const, text: 'Missing' },
};

// Policy roster + Coverage Analysis (Protection Score, gap detection, and
// over-insurance warnings). Bundled in one component because the Gap
// Analysis ingests the same policies array — splitting them would require
// hoisting the array into context and is not worth it.
export default function InsuranceInventory() {
  const { crGrossIncome, crMarginalRate } = usePlanning();
  const [confirm, confirmDialog] = useConfirm();
  const [insurancePolicies, setInsurancePolicies] = useState<Policy[]>(() => {
    try {
      const parsed: unknown = JSON.parse(readStored('insurance_policies', '[]'));
      return Array.isArray(parsed) ? (parsed as Policy[]) : [];
    } catch {
      return [];
    }
  });

  const persist = (updated: Policy[]) => {
    setInsurancePolicies(updated);
    writeStored('insurance_policies', JSON.stringify(updated));
  };
  const add = (type: string) => persist([...insurancePolicies, { type, provider: '', annual_cost: 0, coverage: 0, renewal_date: '' }]);
  const update = (idx: number, patch: Partial<Policy>) => persist(insurancePolicies.map((p, i) => (i === idx ? { ...p, ...patch } : p)));
  const remove = async (idx: number) => {
    const name = policyLabel(insurancePolicies[idx].type);
    if (!(await confirm({ title: `Remove “${name}”?`, body: 'The provider, cost and cover you entered for this policy will be deleted.', confirmLabel: 'Remove policy' }))) return;
    persist(insurancePolicies.filter((_, i) => i !== idx));
  };

  const totalAnnualCost = insurancePolicies.reduce((s, p) => s + (p.annual_cost || 0), 0);

  return (
    <>
      <Section id="insurance" title="Insurance Inventory" subtitle="Your policies with their yearly cost, cover and renewal date. Saved in this browser only.">
        <div className="flex flex-wrap gap-2">
          {POLICY_TYPES.map(p => (
            <AddChip key={p.type} onClick={() => add(p.type)}>{p.label}</AddChip>
          ))}
        </div>

        {insurancePolicies.length === 0 && (
          <div className="mt-4">
            <EmptyState
              title="No policies yet"
              message="Add your insurance policies to track what they cost and to check for gaps in your cover."
              action={{ label: 'Add personal liability', onClick: () => add('Haftpflicht') }}
            />
          </div>
        )}

        {insurancePolicies.length > 0 && (
          <>
            <div className="mt-4 flex flex-col gap-3">
              {insurancePolicies.map((pol, idx) => (
                <ItemCard key={idx}>
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <span className="min-w-0 text-[15px] font-medium text-ink">{policyLabel(pol.type)}</span>
                    <RemoveButton name={policyLabel(pol.type)} onClick={() => remove(idx)} />
                  </div>
                  <div className="grid grid-cols-1 gap-x-3 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
                    <Field label="Provider" type="text" value={pol.provider} onChange={e => update(idx, { provider: e.target.value })} autoComplete="off" />
                    <NumberField label="Annual cost" unit="€ / year" value={pol.annual_cost || 0} onValueChange={v => update(idx, { annual_cost: Math.max(0, v) })} />
                    {pol.type === 'BU' ? (
                      <NumberField label="Monthly benefit" unit="€ / month" value={pol.coverage || 0} onValueChange={v => update(idx, { coverage: Math.max(0, v) })} />
                    ) : (
                      <NumberField label="Sum insured" unit="€" value={pol.coverage || 0} onValueChange={v => update(idx, { coverage: Math.max(0, v) })} />
                    )}
                    <Field label="Renewal date" type="date" value={pol.renewal_date} onChange={e => update(idx, { renewal_date: e.target.value })} />
                  </div>
                </ItemCard>
              ))}
            </div>
            <MetricGrid className="mt-4">
              <MetricCard
                className="col-span-2"
                label="Total annual cost"
                value={fmtEUR(totalAnnualCost)}
                sublabel={`${insurancePolicies.length} ${insurancePolicies.length === 1 ? 'policy' : 'policies'}`}
              />
            </MetricGrid>
          </>
        )}
        {confirmDialog}
      </Section>

      {/* Coverage Analysis */}
      {insurancePolicies.length > 0 && (() => {
        const grossIncome = parseFloat(crGrossIncome) || 0;
        const marginalRate = parsePct(crMarginalRate, DEFAULT_MARGINAL_RATE_PCT);
        const netIncome = grossIncome * (1 - marginalRate / 100);

        const gapRules: { type: string; label: string; need: number; unit: string; explanation: string }[] = [
          { type: 'BU', label: 'Disability (Berufsunfähigkeit)', need: netIncome * 0.75 / 12, unit: ' a month', explanation: 'Monthly benefit of 75 % of your net monthly income' },
          { type: 'Risikoleben', label: 'Term life (Risikoleben)', need: grossIncome * 3, unit: '', explanation: 'Sum insured of three times your gross yearly income' },
          { type: 'Haftpflicht', label: 'Personal liability (Haftpflicht)', need: 10_000_000, unit: '', explanation: 'Sum insured of at least 10 million €' },
          { type: 'Hausrat', label: 'Home contents (Hausrat)', need: 50_000, unit: '', explanation: 'Sum insured of 50.000 €, a typical household' },
          { type: 'KV', label: 'Health (Krankenversicherung)', need: 1, unit: '', explanation: 'Compulsory: a policy must exist' },
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

        const hasType = (t: string) => insurancePolicies.some(p => p.type === t);
        const sumOf = (t: string, key: 'annual_cost' | 'coverage') => insurancePolicies.filter(p => p.type === t).reduce((s, p) => s + (p[key] || 0), 0);
        const warnings: { title: string; detail: string }[] = [];
        if (hasType('Unfall') && hasType('BU')) {
          warnings.push({ title: 'Accident insurance may be redundant', detail: `With disability insurance in place, accident insurance (Unfallversicherung) adds little. Cancelling it would save ${fmtEUR(sumOf('Unfall', 'annual_cost'))} a year.` });
        }
        if (hasType('Hausrat')) {
          const hausratCoverage = sumOf('Hausrat', 'coverage');
          if (hausratCoverage > 100_000) {
            warnings.push({ title: 'Home contents cover looks high', detail: `${fmtEUR(hausratCoverage)} may be more than your household contents are worth. Check that the sum insured reflects reality.` });
          }
        }
        if (hasType('Rechtsschutz')) {
          const rechtsschutzCost = sumOf('Rechtsschutz', 'annual_cost');
          if (rechtsschutzCost > 500) {
            warnings.push({ title: 'Legal expenses insurance is expensive', detail: `${fmtEUR(rechtsschutzCost)} a year is above average (over 500 €). Compare quotes, or check whether your employer provides legal cover.` });
          }
        }

        return (
          <Section id="coverage" title="Coverage Analysis" subtitle="Your policies checked against common rules of thumb for the five essential covers.">
            <MetricGrid>
              <MetricCard
                className="col-span-2"
                label="Protection score"
                money={false}
                value={`${protectionScore} of 100`}
                sublabel={`${adequate} of ${total} essential covers adequate. Each adequate cover scores 20, each partial one 10.`}
              />
            </MetricGrid>
            <ProgressBar className="mt-3" value={protectionScore} label="Protection score" />
            {grossIncome <= 0 && (
              <p className="mt-3 text-[13px] text-ink-body">
                <Chip tone="warning" className="mr-2">Incomplete</Chip>
                Disability and term-life cover can’t be judged without your gross income. <a href="#profile" className="font-medium text-forest underline">Enter it in Your Profile</a>.
              </p>
            )}

            <ul className="mt-4 border-t border-divider">
              {analysis.map(a => (
                <li key={a.type} className="flex items-start justify-between gap-x-4 border-b border-divider py-3">
                  <div className="min-w-0 flex-1">
                    <p className="text-[14px] font-medium text-ink">{a.label}</p>
                    <p className="text-[13px] text-ink-muted">Rule of thumb: {a.explanation.charAt(0).toLowerCase() + a.explanation.slice(1)}.</p>
                  </div>
                  <div className="flex shrink-0 flex-col items-end gap-1 text-right">
                    <Chip tone={STATUS[a.status].tone}>{STATUS[a.status].text}</Chip>
                    {a.hasCoverage && a.gap > 0 && (
                      <span className="text-[13px] text-ink-body" data-privacy="blur">{fmtEUR(a.gap)}{a.unit} short</span>
                    )}
                  </div>
                </li>
              ))}
            </ul>

            {warnings.length > 0 && (
              <div className="mt-6">
                <h3 className="label-caps mb-2">Possibly over-insured</h3>
                <ul className="border-t border-divider">
                  {warnings.map(w => (
                    <li key={w.title} className="border-b border-divider py-3">
                      <p className="flex flex-wrap items-center gap-2 text-[14px] font-medium text-ink">
                        <Chip tone="warning">Review</Chip>{w.title}
                      </p>
                      <p className="mt-1 text-[13px] text-ink-body" data-privacy="blur">{w.detail}</p>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <Assumptions>
              <p>
                Net income is estimated as the gross income from Your Profile minus your {fmtPct(marginalRate, { digits: 0 })} marginal tax rate, which understates it; treat the disability figure as a lower bound.
              </p>
              <p>A cover counts as adequate from 80 % of the rule of thumb and as partial from 50 %.</p>
            </Assumptions>
          </Section>
        );
      })()}
    </>
  );
}
