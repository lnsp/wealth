import { usePlanning } from './PlanningContext';
import { SubHeading, MetricCard, MetricGrid } from '../ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { toNum } from './shared';
import { BAV_MIN_SUBSIDY_ANNUAL, parsePct, riesterPlan, rurupRoom } from './planningMath';
import {
  BAV_MIN_EMPLOYER_SUBSIDY, BAV_SV_FREE_2026, DEFAULT_MARGINAL_RATE_PCT, DEFAULT_RETURN_PCT,
  RIESTER_GRUNDZULAGE, RIESTER_KINDERZULAGE,
} from './constants';

const COMPOUND_RATE = DEFAULT_RETURN_PCT / 100;

// Quantifies what's being forfeited by NOT using tax-advantaged accounts:
// the statutory minimum bAV employer subsidy and unclaimed Riester
// allowances, compounded at 7 % over 10/30 years. The Rürup deduction is
// shown separately: payouts are taxed later, so it is a deferral, not money
// lost, and it is left out of the totals. 2026 limits are in constants.ts.
//
// Rendered as a block inside the "Money Left on the Table" section
// (OpportunityCosts.tsx) — it has no section header of its own.
export default function MoneyLeftOnTable() {
  const { accounts, crMarginalRate, crChildren, crGrossIncome } = usePlanning();
  const marginalRate = parsePct(crMarginalRate, DEFAULT_MARGINAL_RATE_PCT);
  const numCh = Math.max(0, Math.round(toNum(crChildren)));

  // Any company pension counts: the 15 % subsidy is owed by law even when no
  // match is recorded, so nothing is "forfeited" once a bAV exists.
  const hasBav = accounts.some(a => a.tax_treatment === 'bav');
  const hasRiester = accounts.some(a => a.tax_treatment === 'riester');
  const hasRurup = accounts.some(a => a.tax_treatment === 'rurup');

  const forfeitedMatch = hasBav ? 0 : BAV_MIN_SUBSIDY_ANNUAL;
  const forfeitedRiester = hasRiester ? 0 : riesterPlan(marginalRate, numCh).zulagen;
  const rurupLimit = rurupRoom(toNum(crGrossIncome));
  const rurupDeferral = hasRurup ? 0 : rurupLimit * marginalRate / 100;
  const rurupRelevant = !hasRurup && marginalRate >= 35 && rurupDeferral > 0;

  const totalForfeited = forfeitedMatch + forfeitedRiester;

  const compound = (annual: number, years: number) => {
    let total = 0;
    for (let y = 0; y < years; y++) total = (total + annual) * (1 + COMPOUND_RATE);
    return total;
  };
  const impact10 = compound(totalForfeited, 10);
  const impact30 = compound(totalForfeited, 30);

  const hasAnyGap = forfeitedMatch > 0 || forfeitedRiester > 0 || rurupRelevant;

  return (
    <div>
      <SubHeading>Unused tax-advantaged accounts, per year</SubHeading>
      {!hasAnyGap ? (
        <p className="mt-2 text-[14px] text-ink-body">
          Nothing flagged: you have a company pension (bAV) and a Riester account, and a Rürup deduction is either in use or not worthwhile below a 35 % marginal tax rate.
        </p>
      ) : (
        <>
          <p className="mt-1 text-[13px] text-ink-muted">
            Estimates of what the accounts you don’t have could add each year. Uses the marginal tax rate, children and gross income from Your Profile.
          </p>
          <MetricGrid className="mt-3">
            {forfeitedMatch > 0 && (
              <MetricCard
                label="Company pension subsidy"
                tone="negative"
                value={fmtEUR(forfeitedMatch)}
                sublabel={`No company pension (bAV) is set up. By law your employer adds at least ${fmtPct(BAV_MIN_EMPLOYER_SUBSIDY * 100, { digits: 0 })} of salary converted, on up to ${fmtEUR(BAV_SV_FREE_2026)} a year; many add more.`}
              />
            )}
            {forfeitedRiester > 0 && (
              <MetricCard
                label="Riester allowances"
                tone="negative"
                value={fmtEUR(forfeitedRiester)}
                sublabel={numCh > 0
                  ? `State allowances (Zulagen): ${fmtEUR(RIESTER_GRUNDZULAGE)} basic plus ${fmtEUR(RIESTER_KINDERZULAGE)} for each of ${numCh} ${numCh === 1 ? 'child' : 'children'}. No Riester account.`
                  : `State basic allowance (Grundzulage) of ${fmtEUR(RIESTER_GRUNDZULAGE)}. No Riester account.`}
              />
            )}
            {totalForfeited > 0 && (
              <>
                <MetricCard
                  label="After 10 years"
                  tone={impact10 > 0 ? 'negative' : 'neutral'}
                  value={fmtEUR(impact10)}
                  sublabel={`The allowances above, invested at ${fmtPct(COMPOUND_RATE * 100, { digits: 0 })} a year`}
                />
                <MetricCard
                  label="After 30 years"
                  tone={impact30 > 0 ? 'negative' : 'neutral'}
                  value={fmtEUR(impact30)}
                  sublabel={`The allowances above, invested at ${fmtPct(COMPOUND_RATE * 100, { digits: 0 })} a year`}
                />
              </>
            )}
            {rurupRelevant && (
              <MetricCard
                label="Rürup tax deferral"
                value={fmtEUR(rurupDeferral)}
                sublabel={`Tax you could defer by paying ${fmtEUR(rurupLimit)} into a Rürup pension at ${fmtPct(marginalRate, { digits: 0 })}. Payouts are taxed in full later, so the gain is the difference to your rate in retirement; not counted in the totals.`}
              />
            )}
          </MetricGrid>
        </>
      )}
    </div>
  );
}
