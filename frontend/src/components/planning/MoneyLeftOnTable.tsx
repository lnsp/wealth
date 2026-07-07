import { usePlanning } from './PlanningContext';

// Quantifies what's being forfeited by NOT using tax-advantaged accounts:
// missed bAV employer match, unclaimed Riester Zulagen, missed Rürup
// deduction (only flagged if marginal rate ≥35% where it really matters).
// Compounded at 7% over 10/30 years to show the long-tail cost.
export default function MoneyLeftOnTable() {
  const { accounts, crMarginalRate, crChildren, fmt } = usePlanning();
  const marginalRate = parseFloat(crMarginalRate) || 42;
  const numCh = parseInt(crChildren) || 0;

  const bavAccs = accounts.filter(a => a.tax_treatment === 'bav');
  const matchPct = bavAccs.length > 0 ? (bavAccs[0].employer_match_pct ?? 0) : 0;
  const hasBav = bavAccs.length > 0 && matchPct > 0;
  const hasRiester = accounts.some(a => a.tax_treatment === 'riester');
  const hasRurup = accounts.some(a => a.tax_treatment === 'rurup');

  const bavMaxAnnual = 7248;
  const forfeitedMatch = hasBav ? 0 : bavMaxAnnual * 0.5; // 50% employer match assumption when no bAV configured
  const riesterZulagen = 175 + numCh * 300;
  const forfeitedRiester = hasRiester ? 0 : riesterZulagen;
  const rurupDeduction = 27566 * (marginalRate / 100);
  const forfeitedRurup = hasRurup ? 0 : Math.min(rurupDeduction, 27566 * 0.42);

  const totalForfeited = forfeitedMatch + forfeitedRiester;
  const rurupRelevant = !hasRurup && marginalRate >= 35;
  const totalWithRurup = totalForfeited + (rurupRelevant ? forfeitedRurup : 0);

  const compound = (annual: number, years: number) => {
    let total = 0;
    for (let y = 0; y < years; y++) total = (total + annual) * 1.07;
    return total;
  };
  const impact10 = compound(totalWithRurup, 10);
  const impact30 = compound(totalWithRurup, 30);

  const hasAnyGap = forfeitedMatch > 0 || forfeitedRiester > 0 || rurupRelevant;
  if (!hasAnyGap) return null;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Money Left on the Table</h2>
      <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
        Estimated annual benefit you are forfeiting by not using tax-optimized accounts.
      </p>

      <div className="grid grid-cols-2 md:grid-cols-3 gap-2 mb-4 px-1 md:px-0">
        {forfeitedMatch > 0 && (
          <div className="rounded-xl bg-parchment-deep p-3">
            <p className="text-[12px] text-ink-muted mb-1">Forfeited bAV Match</p>
            <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(forfeitedMatch)}/yr</p>
            <p className="text-[11px] text-ink-muted mt-0.5">No bAV account with employer match configured</p>
          </div>
        )}
        {forfeitedRiester > 0 && (
          <div className="rounded-xl bg-parchment-deep p-3">
            <p className="text-[12px] text-ink-muted mb-1">Unclaimed Riester Zulagen</p>
            <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(forfeitedRiester)}/yr</p>
            <p className="text-[11px] text-ink-muted mt-0.5">{numCh > 0 ? `Grundzulage + ${numCh} Kinderzulage(n)` : 'Grundzulage (no Riester account)'}</p>
          </div>
        )}
        {rurupRelevant && (
          <div className="rounded-xl bg-parchment-deep p-3">
            <p className="text-[12px] text-ink-muted mb-1">Missed Rürup Deduction</p>
            <p className="font-serif text-[20px] font-semibold tabular-nums text-amber">{fmt(forfeitedRurup)}/yr</p>
            <p className="text-[11px] text-ink-muted mt-0.5">At {marginalRate}% marginal rate</p>
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-2 px-1 md:px-0">
        <div className="rounded-xl bg-parchment-deep p-3 text-center">
          <p className="text-[12px] text-ink-muted mb-1">10-Year Impact</p>
          <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(impact10)}</p>
          <p className="text-[11px] text-ink-muted mt-0.5">compounded at 7%</p>
        </div>
        <div className="rounded-xl bg-parchment-deep p-3 text-center">
          <p className="text-[12px] text-ink-muted mb-1">30-Year Impact</p>
          <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(impact30)}</p>
          <p className="text-[11px] text-ink-muted mt-0.5">compounded at 7%</p>
        </div>
      </div>
    </div>
  );
}
