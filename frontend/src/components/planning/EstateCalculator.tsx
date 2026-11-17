import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { Section, MetricCard, MetricGrid, AddChip, EmptyState, useConfirm } from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { Assumptions, RemoveButton } from './shared';

const FREIBETRAG: Record<string, number> = { spouse: 500000, child: 400000, grandchild: 200000, sibling: 20000, other: 20000 };
const TAX_RATES: Record<string, number[]> = {
  spouse: [7, 11, 15, 19, 23, 27, 30], child: [7, 11, 15, 19, 23, 27, 30],
  grandchild: [7, 11, 15, 19, 23, 27, 30], sibling: [15, 20, 25, 30, 35, 40, 43], other: [30, 30, 30, 30, 50, 50, 50],
};
const RELATIONS: { id: string; label: string }[] = [
  { id: 'spouse', label: 'Spouse' },
  { id: 'child', label: 'Child' },
  { id: 'grandchild', label: 'Grandchild' },
  { id: 'sibling', label: 'Sibling' },
  { id: 'other', label: 'Other' },
];
const TAX_BRACKETS = [75000, 300000, 600000, 6000000, 13000000, 26000000, Infinity];

// § 10 Abs. 1 S. 6 & § 19 Abs. 1, 3 ErbStG:
// German Erbschaftsteuer applies a flat slab rate (Stufentarif) to the ENTIRE
// rounded taxable acquisition (rounded down to full 100 EUR), subject to
// Härteausgleich (§ 19 Abs. 3 ErbStG) right above each slab threshold so the
// marginal tax on the excess above the previous bracket limit never exceeds
// 50% (where slab rate <= 30%) or 75% (where slab rate > 30%).
function computeTax(taxable: number, relation: string): number {
  const rounded = Math.floor(taxable / 100) * 100;
  if (rounded <= 0) return 0;
  const rates = TAX_RATES[relation] || TAX_RATES.other;
  let idx = TAX_BRACKETS.findIndex(limit => rounded <= limit);
  if (idx < 0) idx = TAX_BRACKETS.length - 1;
  const rate = rates[idx];
  const rawTax = Math.floor(rounded * rate / 100);
  if (idx === 0) return rawTax;

  const prevLimit = TAX_BRACKETS[idx - 1];
  const prevTax = Math.floor(prevLimit * rates[idx - 1] / 100);
  const capFactor = rate <= 30 ? 0.5 : 0.75;
  const mitigatedTax = Math.floor(prevTax + (rounded - prevLimit) * capFactor);
  return Math.min(rawTax, mitigatedTax);
}

// German inheritance tax (Erbschaftsteuer) estimator. The Freibetrag and
// rate ladder come from §15-19 ErbStG — current values as of 2026. We
// don't yet model the lifetime-gift offset (Zehnjahresfrist) that lets
// a Freibetrag re-apply every 10 years; the estimate is a single
// inheritance event at today's NW.
export default function EstateCalculator() {
  const { totalNetWorth } = usePlanning();
  const [confirm, confirmDialog] = useConfirm();
  const [heirs, setHeirs] = useState<{ name: string; relation: string }[]>([]);

  const title = 'Estate Calculator';
  const subtitle = 'An estimate of German inheritance tax (Erbschaftsteuer) if your net worth were passed on today.';

  if (totalNetWorth <= 0) {
    return (
      <Section id="estate" title={title} subtitle={subtitle}>
        <EmptyState
          title="No net worth to pass on yet"
          message="The estimate starts from your current net worth. Import transactions or add an account first."
          action={{ label: 'Go to Settings', href: '/settings' }}
        />
      </Section>
    );
  }

  const sharePerHeir = heirs.length > 0 ? totalNetWorth / heirs.length : 0;
  const results = heirs.map(h => {
    const fb = FREIBETRAG[h.relation] || 20000;
    const taxable = Math.max(sharePerHeir - fb, 0);
    const tax = computeTax(taxable, h.relation);
    return { ...h, share: sharePerHeir, freibetrag: fb, taxable, tax, net: sharePerHeir - tax };
  });
  const totalTax = results.reduce((s, r) => s + r.tax, 0);

  const addHeir = (rel: { id: string; label: string }) => {
    setHeirs(prev => [...prev, { name: `${rel.label} ${prev.filter(h => h.relation === rel.id).length + 1}`, relation: rel.id }]);
  };
  const removeHeir = async (i: number) => {
    if (!(await confirm({ title: `Remove “${heirs[i].name}”?`, body: 'The estate will be split among the remaining heirs.', confirmLabel: 'Remove heir' }))) return;
    setHeirs(prev => prev.filter((_, j) => j !== i));
  };

  return (
    <Section id="estate" title={title} subtitle={subtitle}>
      <div className="flex flex-wrap gap-2">
        {RELATIONS.map(rel => (
          <AddChip key={rel.id} onClick={() => addHeir(rel)}>{rel.label}</AddChip>
        ))}
      </div>

      {heirs.length === 0 && (
        <div className="mt-4">
          <EmptyState
            title="No heirs added"
            message="Add the people who would inherit to estimate the tax each of them would pay."
            action={{ label: 'Add a spouse', onClick: () => addHeir(RELATIONS[0]) }}
          />
        </div>
      )}

      {results.length > 0 && (
        <>
          <ul className="mt-4 border-t border-divider">
            {results.map((r, i) => (
              <li key={i} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-divider py-3">
                <div className="min-w-0 flex-1 basis-56">
                  <p className="text-[15px] font-medium text-ink">{r.name}</p>
                  <p className="text-[13px] text-ink-muted" data-privacy="blur">
                    Share {fmtEUR(r.share)} · tax-free allowance {fmtEUR(r.freibetrag)} · tax {fmtEUR(r.tax)}
                  </p>
                </div>
                <div className="flex items-center gap-4">
                  <div className="text-right">
                    <p className="font-serif text-[18px] text-ink" data-privacy="blur">{fmtEUR(r.net)}</p>
                    <p className="text-[12px] text-ink-muted">received after tax</p>
                  </div>
                  <RemoveButton name={r.name} onClick={() => removeHeir(i)} />
                </div>
              </li>
            ))}
          </ul>
          <MetricGrid className="mt-4">
            <MetricCard label="Total inheritance tax" tone={totalTax > 0 ? 'negative' : 'neutral'} value={fmtEUR(totalTax)} />
            <MetricCard
              label="Share of the estate"
              money={false}
              value={fmtPct(totalNetWorth > 0 ? (totalTax / totalNetWorth) * 100 : 0)}
              sublabel="Tax as a share of net worth"
            />
          </MetricGrid>
        </>
      )}

      <Assumptions>
        <p>Your whole net worth of <span data-privacy="blur">{fmtEUR(totalNetWorth)}</span> is split equally among the heirs you add, in a single inheritance today.</p>
        <p>
          Tax-free allowances (Freibetrag): spouse {fmtEUR(FREIBETRAG.spouse)}, child {fmtEUR(FREIBETRAG.child)}, grandchild {fmtEUR(FREIBETRAG.grandchild)}, sibling and others {fmtEUR(FREIBETRAG.other)}.
        </p>
        <p>Not included: gifts in the last ten years, the family-home exemption, pension allowances (Versorgungsfreibetrag) and business assets.</p>
      </Assumptions>
      {confirmDialog}
    </Section>
  );
}
