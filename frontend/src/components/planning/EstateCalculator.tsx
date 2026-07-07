import { useState } from 'react';
import { usePlanning } from './PlanningContext';

const FREIBETRAG: Record<string, number> = { spouse: 500000, child: 400000, grandchild: 200000, sibling: 20000, other: 20000 };
const TAX_RATES: Record<string, number[]> = {
  spouse: [7, 11, 15, 19, 23, 27, 30], child: [7, 11, 15, 19, 23, 27, 30],
  grandchild: [7, 11, 15, 19, 23, 27, 30], sibling: [15, 20, 25, 30, 35, 40, 43], other: [30, 30, 30, 30, 50, 50, 50],
};
const TAX_BRACKETS = [75000, 300000, 600000, 6000000, 13000000, 26000000, Infinity];

function computeTax(taxable: number, relation: string): number {
  if (taxable <= 0) return 0;
  const rates = TAX_RATES[relation] || TAX_RATES.other;
  let remaining = taxable, tax = 0, prev = 0;
  for (let i = 0; i < TAX_BRACKETS.length; i++) {
    const bracket = Math.min(remaining, TAX_BRACKETS[i] - prev);
    tax += bracket * rates[i] / 100;
    remaining -= bracket;
    prev = TAX_BRACKETS[i];
    if (remaining <= 0) break;
  }
  return tax;
}

// German inheritance tax (Erbschaftsteuer) estimator. The Freibetrag and
// rate ladder come from §15-19 ErbStG — current values as of 2026. We
// don't yet model the lifetime-gift offset (Zehnjahresfrist) that lets
// a Freibetrag re-apply every 10 years; the estimate is a single
// inheritance event at today's NW.
export default function EstateCalculator() {
  const { totalNetWorth, fmt } = usePlanning();
  const [heirs, setHeirs] = useState<{ name: string; relation: string }[]>([]);

  if (totalNetWorth <= 0) return null;

  const sharePerHeir = heirs.length > 0 ? totalNetWorth / heirs.length : 0;
  const results = heirs.map(h => {
    const fb = FREIBETRAG[h.relation] || 20000;
    const taxable = Math.max(sharePerHeir - fb, 0);
    const tax = computeTax(taxable, h.relation);
    return { ...h, share: sharePerHeir, freibetrag: fb, taxable, tax, net: sharePerHeir - tax };
  });
  const totalTax = results.reduce((s, r) => s + r.tax, 0);

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Estate Calculator</h2>
      <p className="text-[13px] text-ink-muted mb-3">
        German inheritance tax (Erbschaftsteuer) estimate based on current net worth.
      </p>

      <div className="flex flex-wrap gap-2 mb-3">
        {['spouse', 'child', 'sibling', 'other'].map(rel => (
          <button key={rel} onClick={() => setHeirs(prev => [...prev, { name: `${rel.charAt(0).toUpperCase() + rel.slice(1)} ${prev.filter(h => h.relation === rel).length + 1}`, relation: rel }])}
            className="rounded-[8px] bg-parchment-deep px-3 py-1.5 text-[13px] font-medium text-ink-body hover:bg-divider capitalize">
            + {rel}
          </button>
        ))}
      </div>

      {results.length > 0 && (
        <div className="space-y-2">
          {results.map((r, i) => (
            <div key={i} className="flex items-center justify-between rounded-xl bg-parchment-deep px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="text-[15px] font-medium text-ink">{r.name}</p>
                <p className="text-[12px] text-ink-muted">
                  Share {fmt(r.share)} · Freibetrag {fmt(r.freibetrag)} · Tax {fmt(r.tax)}
                </p>
              </div>
              <div className="text-right shrink-0 ml-2">
                <p className="text-[15px] font-semibold tabular-nums text-sage">{fmt(r.net)}</p>
                <p className="text-[11px] text-ink-muted">net to heir</p>
              </div>
              <button onClick={() => setHeirs(prev => prev.filter((_, j) => j !== i))}
                className="text-claret text-[12px] ml-2 shrink-0">x</button>
            </div>
          ))}
          <p className="text-[13px] text-ink-muted pt-1">
            Total tax: <span className="text-claret font-medium">{fmt(totalTax)}</span>
            {' '}({totalNetWorth > 0 ? ((totalTax / totalNetWorth) * 100).toFixed(1) : 0}% effective rate)
          </p>
        </div>
      )}

      {heirs.length === 0 && (
        <p className="text-[13px] text-ink-muted">
          Add heirs to estimate inheritance tax. Freibetrag: Spouse 500K, Child 400K, Sibling/Other 20K.
        </p>
      )}
    </div>
  );
}
