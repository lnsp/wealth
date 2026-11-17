import { usePlanning } from './PlanningContext';
import { SubHeading, MetricCard, MetricGrid } from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';

const BAV_MAX_ANNUAL = 7248;
const ASSUMED_MATCH = 0.5;
const RURUP_MAX_ANNUAL = 27566;
const COMPOUND_RATE = 0.07;

// Quantifies what's being forfeited by NOT using tax-advantaged accounts:
// missed bAV employer match, unclaimed Riester Zulagen, missed Rürup
// deduction (only flagged if marginal rate ≥35% where it really matters).
// Compounded at 7% over 10/30 years to show the long-tail cost.
//
// Rendered as a block inside the "Money Left on the Table" section
// (OpportunityCosts.tsx) — it has no section header of its own.
export default function MoneyLeftOnTable() {
  const { accounts, crMarginalRate, crChildren } = usePlanning();
  const marginalRate = parseFloat(crMarginalRate) || 42;
  const numCh = parseInt(crChildren, 10) || 0;

  const bavAccs = accounts.filter(a => a.tax_treatment === 'bav');
  const matchPct = bavAccs.length > 0 ? (bavAccs[0].employer_match_pct ?? 0) : 0;
  const hasBav = bavAccs.length > 0 && matchPct > 0;
  const hasRiester = accounts.some(a => a.tax_treatment === 'riester');
  const hasRurup = accounts.some(a => a.tax_treatment === 'rurup');

  const forfeitedMatch = hasBav ? 0 : BAV_MAX_ANNUAL * ASSUMED_MATCH; // 50% employer match assumption when no bAV configured
  const riesterZulagen = 175 + numCh * 300;
  const forfeitedRiester = hasRiester ? 0 : riesterZulagen;
  const rurupDeduction = RURUP_MAX_ANNUAL * (marginalRate / 100);
  const forfeitedRurup = hasRurup ? 0 : Math.min(rurupDeduction, RURUP_MAX_ANNUAL * 0.42);

  const totalForfeited = forfeitedMatch + forfeitedRiester;
  const rurupRelevant = !hasRurup && marginalRate >= 35;
  const totalWithRurup = totalForfeited + (rurupRelevant ? forfeitedRurup : 0);

  const compound = (annual: number, years: number) => {
    let total = 0;
    for (let y = 0; y < years; y++) total = (total + annual) * (1 + COMPOUND_RATE);
    return total;
  };
  const impact10 = compound(totalWithRurup, 10);
  const impact30 = compound(totalWithRurup, 30);

  const hasAnyGap = forfeitedMatch > 0 || forfeitedRiester > 0 || rurupRelevant;

  return (
    <div>
      <SubHeading>Unused tax-advantaged accounts, per year</SubHeading>
      {!hasAnyGap ? (
        <p className="mt-2 text-[14px] text-ink-body">
          Nothing flagged: you have a company pension (bAV) with an employer match and a Riester account, and a Rürup deduction is either in use or not worthwhile below a 35 % marginal tax rate.
        </p>
      ) : (
        <>
          <p className="mt-1 text-[13px] text-ink-muted">
            Upper-bound estimates of what the accounts you don’t have could add each year. Uses the marginal tax rate and children from Your Profile.
          </p>
          <MetricGrid className="mt-3">
            {forfeitedMatch > 0 && (
              <MetricCard
                label="Company pension match"
                tone="negative"
                value={fmtEUR(forfeitedMatch)}
                sublabel={`No company pension (bAV) with an employer match is set up. Assumes your employer would add ${fmtPct(ASSUMED_MATCH * 100, { digits: 0 })} on the ${fmtEUR(BAV_MAX_ANNUAL)} yearly maximum.`}
              />
            )}
            {forfeitedRiester > 0 && (
              <MetricCard
                label="Riester allowances"
                tone="negative"
                value={fmtEUR(forfeitedRiester)}
                sublabel={numCh > 0
                  ? `State allowances (Zulagen): 175 € basic plus 300 € for each of ${numCh} ${numCh === 1 ? 'child' : 'children'}. No Riester account.`
                  : 'State basic allowance (Grundzulage) of 175 €. No Riester account.'}
              />
            )}
            {rurupRelevant && (
              <MetricCard
                label="Rürup tax deduction"
                tone="negative"
                value={fmtEUR(forfeitedRurup)}
                sublabel={`Tax saved by paying in the ${fmtEUR(RURUP_MAX_ANNUAL)} yearly maximum at a ${fmtPct(Math.min(marginalRate, 42), { digits: 0 })} rate. No Rürup account.`}
              />
            )}
            <MetricCard
              label="After 10 years"
              tone={impact10 > 0 ? 'negative' : 'neutral'}
              value={fmtEUR(impact10)}
              sublabel={`If the yearly total were invested at ${fmtPct(COMPOUND_RATE * 100, { digits: 0 })} a year`}
            />
            <MetricCard
              label="After 30 years"
              tone={impact30 > 0 ? 'negative' : 'neutral'}
              value={fmtEUR(impact30)}
              sublabel={`If the yearly total were invested at ${fmtPct(COMPOUND_RATE * 100, { digits: 0 })} a year`}
            />
          </MetricGrid>
        </>
      )}
    </div>
  );
}
