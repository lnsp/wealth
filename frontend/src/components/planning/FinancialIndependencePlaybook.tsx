import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { Section, SubHeading, MetricCard, MetricGrid, ProgressBar, EmptyState } from '../ui';
import { fmtEUR, fmtPct, fmtDate } from '../../utils/format';
import { Assumptions, downloadText, readStored, toNum, writeStored } from './shared';
import { humanCapitalCrossover, parsePct, scheduleByYear } from './planningMath';
import { DEFAULT_MARGINAL_RATE_PCT } from './constants';

const GROWTH_RATE = 0.07;
const DONE_KEY = 'playbook_done';

type Milestone = { label: string; money: boolean; pct: number; projectedDate: string };
type PlanYear = { year: number; age: number; actions: string[]; projectedNW: number };

const actionKey = (year: number, action: string) => `${year}|${action}`;

// Year-by-year action plan combining: year-in-review (last calendar year
// + YTD NW deltas from snapshots), milestone progress bars (the FIRE
// Calculator's target and date, the Human Capital crossover with the same
// inputs, next 100k/250k/500k/1M of cash and investments), and an annual
// checklist (initial setup → recurring Steuererklärung / tax-loss
// harvesting / rebalance triggers → age-gated retirement events).
// Ticked actions are saved per item in localStorage; the export button
// serializes the plan exactly as shown to a .txt download.
export default function FinancialIndependencePlaybook() {
  const {
    accounts, allSnapshots, investableNetWorth, projData, pensionCurrentAge, pensionAge, crBudget, crMarginalRate, crGrossIncome,
    hcSalaryGrowth, hcDiscount, hcPortfolioGrowth, income,
  } = usePlanning();
  const [done, setDone] = useState<Record<string, boolean>>(() => {
    try {
      const parsed: unknown = JSON.parse(readStored(DONE_KEY, '{}'));
      return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? (parsed as Record<string, boolean>) : {};
    } catch {
      return {};
    }
  });
  const toggle = (key: string, checked: boolean) => {
    setDone(prev => {
      const next = { ...prev };
      if (checked) next[key] = true; else delete next[key];
      writeStored(DONE_KEY, JSON.stringify(next));
      return next;
    });
  };

  const now = new Date();
  const thisYear = now.getFullYear();
  const currentAge = pensionCurrentAge;
  const retireAge = pensionAge;
  const grossIncome = toNum(crGrossIncome);
  const monthlyBudget = toNum(crBudget);
  const marginalRate = parsePct(crMarginalRate, DEFAULT_MARGINAL_RATE_PCT);

  // ── Year in review — uses snapshots to surface calendar-year deltas ──
  let review: { lastYearNW: number; lastYearPct: number; ytdNW: number; ytdPct: number } | null = null;
  if (allSnapshots.length > 2) {
    const lastYearStart = new Date(thisYear - 1, 0, 1);
    const lastYearEnd = new Date(thisYear - 1, 11, 31);
    const findClosest = (date: Date) => allSnapshots.reduce((best, s) =>
      Math.abs(new Date(s.date).getTime() - date.getTime()) < Math.abs(new Date(best.date).getTime() - date.getTime()) ? s : best
    );
    const startSnap = findClosest(lastYearStart);
    const endSnap = findClosest(lastYearEnd);
    const currentSnap = allSnapshots[0];
    const lastYearNW = endSnap.total - startSnap.total;
    const ytdNW = currentSnap.total - endSnap.total;
    review = {
      lastYearNW,
      lastYearPct: startSnap.total > 0 ? (lastYearNW / startSnap.total * 100) : 0,
      ytdNW,
      ytdPct: endSnap.total > 0 ? (ytdNW / endSnap.total * 100) : 0,
    };
  }

  // ── Milestones ── (cash and investments: the house and car don't compound)
  const nw = investableNetWorth;
  const milestoneContrib = projData?.contribution ?? monthlyBudget;
  const projectYearsTo = (target: number) => {
    if (nw >= target) return 0;
    let v = nw;
    for (let y = 1; y <= 60; y++) {
      v = (v + milestoneContrib * 12) * (1 + GROWTH_RATE);
      if (v >= target) return y;
    }
    return null;
  };
  const milestones: Milestone[] = [];
  const nextRound = Math.ceil(nw / 50000) * 50000;
  if (nextRound > nw) {
    const yrs = projectYearsTo(nextRound);
    milestones.push({ label: `${fmtEUR(nextRound)} in cash and investments`, money: true, pct: Math.min(100, nw / nextRound * 100), projectedDate: yrs ? `${thisYear + yrs}` : '—' });
  }
  // Financial independence: exactly the FIRE Calculator's target and date.
  const drawdown = projData?.drawdown;
  if (drawdown && drawdown.fire_number > 0) {
    const months = drawdown.months_to_fire ?? (drawdown.fire_date ? drawdown.years_to_fire * 12 : -1);
    const date = months === 0 ? 'Reached'
      : months > 0 ? `${drawdown.fire_date.slice(0, 4)} (age ${currentAge + Math.floor(months / 12)})` : '—';
    milestones.push({ label: 'Financial independence (FIRE Calculator target)', money: false, pct: Math.min(100, nw / drawdown.fire_number * 100), projectedDate: date });
  }
  // Human capital: the Human Capital section's crossover, same inputs.
  if (grossIncome > 0 && retireAge > currentAge) {
    const hc = humanCapitalCrossover({
      income: grossIncome, growth: hcSalaryGrowth / 100, discount: hcDiscount / 100, years: retireAge - currentAge,
      startWealth: nw, monthlyContrib: monthlyBudget, portfolioGrowth: hcPortfolioGrowth / 100, ...scheduleByYear(income),
    });
    const humanNow = hc.series[0]?.human ?? 0;
    if (humanNow > 0) {
      const idx = hc.crossoverIndex;
      milestones.push({
        label: 'Financial capital overtakes human capital',
        money: false,
        pct: Math.min(100, nw / humanNow * 100),
        projectedDate: idx === 0 ? 'Reached' : idx != null ? `${thisYear + idx} (age ${currentAge + idx})` : 'Not before retirement',
      });
    }
  }
  for (const t of [100000, 250000, 500000, 1000000]) {
    if (nw < t) {
      if (t !== nextRound) {
        const yrs = projectYearsTo(t);
        milestones.push({ label: `${fmtEUR(t)} in cash and investments`, money: true, pct: Math.min(100, nw / t * 100), projectedDate: yrs ? `${thisYear + yrs}` : '—' });
      }
      break;
    }
  }

  // ── Annual checklist + projected net worth ──
  const hasBav = accounts.some(a => a.tax_treatment === 'bav');
  const hasRiester = accounts.some(a => a.tax_treatment === 'riester');
  const hasRurup = accounts.some(a => a.tax_treatment === 'rurup');
  // Insurance policies are owned by InsuranceInventory; read the persisted
  // list directly so the playbook's coverage gate works.
  let hasInsurance = false;
  try {
    const persisted: unknown = JSON.parse(readStored('insurance_policies', '[]'));
    hasInsurance = Array.isArray(persisted) && persisted.length > 0;
  } catch { /* corrupted localStorage: leave hasInsurance=false */ }

  const planReady = grossIncome > 0 || monthlyBudget > 0;
  const years: PlanYear[] = [];
  if (planReady) {
    let projNW = investableNetWorth;
    const annualContrib = monthlyBudget * 12;
    for (let y = 0; y <= Math.min(retireAge - currentAge, 30); y++) {
      const age = currentAge + y;
      const calYear = thisYear + y;
      const actions: string[] = [];

      if (y === 0) {
        if (!hasBav) actions.push('Set up a company pension (bAV) with employer match');
        if (!hasRiester) actions.push('Open a Riester account for the state allowances (Zulagen)');
        if (marginalRate >= 35 && !hasRurup) actions.push('Consider a Rürup pension for the tax deduction');
        if (!hasInsurance) actions.push('Review insurance cover: disability, personal liability, term life');
        if (monthlyBudget > 0) actions.push(`Start routing ${fmtEUR(monthlyBudget)} a month as in the Contribution Router`);
        actions.push('Set up the exemption order (Freistellungsauftrag) for 1.000 €');
      }
      if (y > 0) {
        actions.push('Review and adjust savings-plan amounts');
        actions.push(`File the tax return (Steuererklärung) for ${calYear - 1}`);
      }
      if (age === 55) actions.push('Begin retirement withdrawal planning');
      if (age === 60) actions.push('Review Riester and Rürup payout options');
      if (age === 62) actions.push('Company pension: earliest access — review the payout');
      if (age === retireAge) actions.push('Retire — begin the withdrawal phase');
      if (y > 0 && y % 3 === 0) actions.push('Look for tax-loss harvesting opportunities');
      if (y > 0 && y % 5 === 0) actions.push('Rebalance the portfolio');

      projNW = y === 0 ? projNW : (projNW + annualContrib) * (1 + GROWTH_RATE);
      years.push({ year: calYear, age, actions, projectedNW: Math.round(projNW) });
    }
  }
  const display = years.filter((y, i) => i < 10 || y.age === 55 || y.age === 60 || y.age === 62 || y.age === retireAge);
  const planEmpty = display.length === 0;

  const exportPlaybook = () => {
    const lines: string[] = [
      'Financial Independence Playbook',
      '='.repeat(40),
      `Generated: ${fmtDate(now)}`,
      `Cash and investments: ${fmtEUR(investableNetWorth)}`,
      `Monthly budget: ${fmtEUR(monthlyBudget)}`,
      `Current age: ${currentAge} | Retire at: ${retireAge}`,
      `Projection: ${fmtPct(GROWTH_RATE * 100, { digits: 0 })} growth a year`,
      '',
    ];
    for (const y of display) {
      lines.push(`${y.year} (age ${y.age}) — projected cash and investments: ${fmtEUR(y.projectedNW)}`);
      for (const a of y.actions) lines.push(`  [${done[actionKey(y.year, a)] ? 'x' : ' '}] ${a}`);
      lines.push('');
    }
    lines.push(`Generated by Wealth on ${fmtDate(now)}`);
    downloadText(`playbook-${thisYear}.txt`, lines);
  };

  return (
    <Section
      id="playbook"
      title="Financial Independence Playbook"
      subtitle="A year-by-year action plan based on your profile, accounts and goals."
      actions={!planEmpty ? (
        <button type="button" onClick={exportPlaybook} className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">Export playbook</button>
      ) : undefined}
    >
      {review && (
        <div className="mb-7">
          <SubHeading>Year in review</SubHeading>
          <MetricGrid className="mt-3">
            <MetricCard
              label={`${thisYear - 1} change`}
              tone={review.lastYearNW > 0 ? 'positive' : review.lastYearNW < 0 ? 'negative' : 'neutral'}
              value={fmtEUR(review.lastYearNW, { sign: true })}
              sublabel={`${fmtPct(review.lastYearPct, { sign: true })} net worth, including deposits`}
            />
            <MetricCard
              label={`${thisYear} so far`}
              tone={review.ytdNW > 0 ? 'positive' : review.ytdNW < 0 ? 'negative' : 'neutral'}
              value={fmtEUR(review.ytdNW, { sign: true })}
              sublabel={`${fmtPct(review.ytdPct, { sign: true })} net worth, including deposits`}
            />
          </MetricGrid>
        </div>
      )}

      {milestones.length > 0 && (
        <div className="mb-7">
          <SubHeading>Milestones</SubHeading>
          <ul className="mt-2 border-t border-divider">
            {milestones.map(m => (
              <li key={m.label} className="border-b border-divider py-3">
                <div className="mb-1.5 flex flex-wrap items-baseline justify-between gap-x-4">
                  <span className="text-[14px] font-medium text-ink" data-privacy={m.money ? 'blur' : undefined}>{m.label}</span>
                  <span className="text-[13px] text-ink-muted">{m.projectedDate === '—' ? 'Not within 60 years' : m.projectedDate === 'Reached' || m.projectedDate === 'Not before retirement' ? m.projectedDate : `Expected ${m.projectedDate}`}</span>
                </div>
                <div className="flex items-center gap-3">
                  <ProgressBar className="flex-1" value={m.pct} label={`Progress to ${m.label}`} />
                  <span className="shrink-0 text-[13px] text-ink-body" data-privacy="blur">{fmtPct(m.pct, { digits: 0 })}</span>
                </div>
              </li>
            ))}
          </ul>
          <p className="mt-2 text-[13px] text-ink-muted">
            Amount milestones assume you keep adding <span data-privacy="blur">{fmtEUR(milestoneContrib)}</span> a month
            ({projData ? 'your net contributions over the last 12 months' : 'the monthly budget from Your Profile'}) and {fmtPct(GROWTH_RATE * 100, { digits: 0 })} growth a year.
            Financial independence is the FIRE Calculator’s result; the human-capital crossover uses the inputs of the Human Capital section.
          </p>
        </div>
      )}

      <SubHeading>Action plan</SubHeading>
      {!planReady && (
        <div className="mt-3">
          <EmptyState
            title="Your plan needs a profile"
            message="Enter your gross income or monthly budget in Your Profile at the top of this page to generate the playbook."
            action={{ label: 'Go to Your Profile', href: '#profile' }}
          />
        </div>
      )}
      {planReady && planEmpty && (
        <div className="mt-3">
          <EmptyState
            title="No years to plan"
            message="Your retirement age in Your Profile must not be lower than your current age."
            action={{ label: 'Go to Your Profile', href: '#profile' }}
          />
        </div>
      )}
      {!planEmpty && (
        <>
          <p className="mt-1 text-[13px] text-ink-muted">
            Projected cash and investments assume {monthlyBudget > 0
              ? <>the <span data-privacy="blur">{fmtEUR(monthlyBudget)}</span> monthly budget from Your Profile</>
              : <>no further contributions (no monthly budget is set in <a href="#profile" className="font-medium text-forest underline">Your Profile</a>)</>}
            {' '}and {fmtPct(GROWTH_RATE * 100, { digits: 0 })} growth a year. Ticks are saved in this browser.
          </p>
          <ol className="mt-3 flex flex-col gap-3">
            {display.map(y => (
              <li key={y.year} className="rounded-apple border border-divider p-4">
                <div className="mb-1 flex flex-wrap items-baseline justify-between gap-x-4">
                  <h4 className="text-[15px] font-medium text-ink">
                    {y.year} <span className="ml-1 text-[13px] font-normal text-ink-muted">Age {y.age}</span>
                  </h4>
                  <span className="font-serif text-[18px] text-ink" data-privacy="blur">{fmtEUR(y.projectedNW)}</span>
                </div>
                {y.actions.length === 0 ? (
                  <p className="text-[13px] text-ink-muted">Nothing scheduled.</p>
                ) : (
                  <ul>
                    {y.actions.map(a => {
                      const key = actionKey(y.year, a);
                      const checked = !!done[key];
                      return (
                        <li key={a}>
                          <label className="flex min-h-[44px] cursor-pointer items-center gap-3 text-[14px] text-ink-body">
                            <input
                              type="checkbox"
                              className="h-5 w-5 shrink-0 accent-forest"
                              checked={checked}
                              onChange={e => toggle(key, e.target.checked)}
                            />
                            <span className={checked ? 'line-through text-ink-muted' : ''} data-privacy={a.includes('€') ? 'blur' : undefined}>{a}</span>
                          </label>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </li>
            ))}
          </ol>
          <Assumptions>
            <p>Shows the next ten years, then the years you turn 55, 60, 62 and {retireAge}.</p>
          </Assumptions>
        </>
      )}
    </Section>
  );
}
