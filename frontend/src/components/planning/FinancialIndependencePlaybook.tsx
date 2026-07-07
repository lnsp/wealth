import { usePlanning } from './PlanningContext';

// Year-by-year action plan combining: year-in-review (last calendar year
// + YTD NW deltas from snapshots), milestone progress bars (FIRE number,
// human-capital crossover, next 100k/250k/500k/1M), and an annual
// checklist (initial setup → recurring Steuererklärung / tax-loss
// harvesting / rebalance triggers → age-gated retirement events).
// The export button serializes the full playbook to a .txt download.
export default function FinancialIndependencePlaybook() {
  const { accounts, allSnapshots, totalNetWorth, projData, pensionCurrentAge, pensionAge, crBudget, crMarginalRate, crGrossIncome, fireExpenses, fireSWR, fmt } = usePlanning();

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Financial Independence Playbook</h2>
      <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
        Year-by-year action plan based on your current situation and goals.
      </p>

      {/* Year-in-Review — uses recent snapshots to surface calendar-year deltas */}
      {allSnapshots.length > 2 && (() => {
        const now = new Date();
        const lastYearStart = new Date(now.getFullYear() - 1, 0, 1);
        const lastYearEnd = new Date(now.getFullYear() - 1, 11, 31);
        const findClosest = (date: Date) => allSnapshots.reduce((best, s) =>
          Math.abs(new Date(s.date).getTime() - date.getTime()) < Math.abs(new Date(best.date).getTime() - date.getTime()) ? s : best
        );
        const startSnap = findClosest(lastYearStart);
        const endSnap = findClosest(lastYearEnd);
        const currentSnap = allSnapshots[0];
        const lastYearNW = endSnap.total - startSnap.total;
        const lastYearPct = startSnap.total > 0 ? (lastYearNW / startSnap.total * 100) : 0;
        const ytdNW = currentSnap.total - endSnap.total;
        const ytdPct = endSnap.total > 0 ? (ytdNW / endSnap.total * 100) : 0;
        return (
          <div className="mb-4 px-1 md:px-0">
            <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Year in Review</p>
            <div className="grid grid-cols-2 gap-2">
              <div className="rounded-xl bg-parchment-deep p-3 text-center">
                <p className="text-[12px] text-ink-muted mb-1">{now.getFullYear() - 1} Change</p>
                <p className={`font-serif text-[18px] font-semibold tabular-nums ${lastYearNW >= 0 ? 'text-sage' : 'text-claret'}`}>{lastYearNW >= 0 ? '+' : ''}{fmt(lastYearNW)}</p>
                <p className="text-[11px] text-ink-muted">{lastYearPct >= 0 ? '+' : ''}{lastYearPct.toFixed(1)}%</p>
              </div>
              <div className="rounded-xl bg-parchment-deep p-3 text-center">
                <p className="text-[12px] text-ink-muted mb-1">{now.getFullYear()} YTD</p>
                <p className={`font-serif text-[18px] font-semibold tabular-nums ${ytdNW >= 0 ? 'text-sage' : 'text-claret'}`}>{ytdNW >= 0 ? '+' : ''}{fmt(ytdNW)}</p>
                <p className="text-[11px] text-ink-muted">{ytdPct >= 0 ? '+' : ''}{ytdPct.toFixed(1)}%</p>
              </div>
            </div>
          </div>
        );
      })()}

      {/* Milestones */}
      {(() => {
        const nw = totalNetWorth;
        const monthlyContrib = projData?.contribution || parseFloat(crBudget) || 0;
        const annualContrib = monthlyContrib * 12;
        const rate = 0.07;

        const projectYearsTo = (target: number) => {
          if (nw >= target) return 0;
          let v = nw;
          for (let y = 1; y <= 60; y++) {
            v = (v + annualContrib) * (1 + rate);
            if (v >= target) return y;
          }
          return null;
        };

        const currentAge = pensionCurrentAge;
        const fireExpAnnual = parseFloat(fireExpenses || '0');
        const fireSWRVal = parseFloat(fireSWR) || 3.5;
        const fireTarget = fireExpAnnual > 0 ? fireExpAnnual / (fireSWRVal / 100) : 0;

        type Milestone = { label: string; pct: number; projectedDate: string };
        const milestones: Milestone[] = [];

        const nextRound = Math.ceil(nw / 50000) * 50000;
        if (nextRound > nw) {
          const yrs = projectYearsTo(nextRound);
          milestones.push({ label: `${fmt(nextRound)} Net Worth`, pct: Math.min(100, nw / nextRound * 100), projectedDate: yrs ? `${new Date().getFullYear() + yrs}` : '—' });
        }
        if (fireTarget > 0) {
          const yrs = projectYearsTo(fireTarget);
          milestones.push({ label: 'FIRE Number', pct: Math.min(100, nw / fireTarget * 100), projectedDate: yrs ? `${new Date().getFullYear() + yrs} (age ${currentAge + yrs})` : nw >= fireTarget ? 'Reached' : '—' });
        }
        const grossIncome = parseFloat(crGrossIncome) || 0;
        if (grossIncome > 0) {
          let humanCap = 0;
          const retireAge = pensionAge;
          for (let y = 1; y <= retireAge - currentAge; y++) humanCap += grossIncome * Math.pow(1.02, y) / Math.pow(1.03, y);
          const crossYrs = projectYearsTo(humanCap);
          milestones.push({ label: 'Crossover (Financial > Human)', pct: Math.min(100, nw / humanCap * 100), projectedDate: crossYrs ? `${new Date().getFullYear() + crossYrs}` : nw >= humanCap ? 'Reached' : '60+' });
        }
        for (const t of [100000, 250000, 500000, 1000000]) {
          if (nw < t) {
            const yrs = projectYearsTo(t);
            milestones.push({ label: `${fmt(t)} Milestone`, pct: Math.min(100, nw / t * 100), projectedDate: yrs ? `${new Date().getFullYear() + yrs}` : '—' });
            break;
          }
        }

        if (milestones.length === 0) return null;
        return (
          <div className="mb-4 px-1 md:px-0">
            <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Milestones</p>
            <div className="space-y-2">
              {milestones.map((m, i) => (
                <div key={i} className="rounded-xl bg-parchment-deep p-3">
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="text-[13px] font-medium text-ink">{m.label}</span>
                    <span className="text-[12px] text-ink-muted tabular-nums">{m.projectedDate}</span>
                  </div>
                  <div className="flex items-center gap-3">
                    <div className="flex-1 h-2 rounded-full bg-divider overflow-hidden">
                      <div className={`h-full rounded-full transition-all duration-500 ${m.pct >= 100 ? 'bg-sage' : m.pct >= 50 ? 'bg-forest' : 'bg-amber'}`} style={{ width: `${Math.min(m.pct, 100)}%` }} />
                    </div>
                    <span className="text-[11px] tabular-nums text-ink-muted shrink-0">{m.pct.toFixed(0)}%</span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        );
      })()}

      {/* Annual checklist + projected NW */}
      {(() => {
        const currentAge = pensionCurrentAge;
        const retireAge = pensionAge;
        const grossIncome = parseFloat(crGrossIncome) || 0;
        const monthlyBudget = parseFloat(crBudget) || 0;
        const marginalRate = parseFloat(crMarginalRate) || 42;
        const hasBav = accounts.some(a => a.tax_treatment === 'bav');
        const hasRiester = accounts.some(a => a.tax_treatment === 'riester');
        const hasRurup = accounts.some(a => a.tax_treatment === 'rurup');
        // Insurance policies are owned by InsuranceInventory now; read the
        // persisted list directly so the playbook's coverage gate works.
        let hasInsurance = false;
        try {
          const persisted = JSON.parse(localStorage.getItem('insurance_policies') || '[]') as unknown[];
          hasInsurance = Array.isArray(persisted) && persisted.length > 0;
        } catch { /* corrupted localStorage: leave hasInsurance=false */ }

        if (grossIncome <= 0 && monthlyBudget <= 0) {
          return <p className="text-[13px] text-ink-muted px-1 md:px-0">Enter gross income and monthly budget above to generate your playbook.</p>;
        }

        const years: { year: number; age: number; actions: string[]; projectedNW: number }[] = [];
        let projNW = totalNetWorth;
        const annualContrib = monthlyBudget * 12;
        const returnRate = 0.07;

        for (let y = 0; y <= Math.min(retireAge - currentAge, 30); y++) {
          const age = currentAge + y;
          const calYear = new Date().getFullYear() + y;
          const actions: string[] = [];

          if (y === 0) {
            if (!hasBav) actions.push('Set up bAV with employer match');
            if (!hasRiester) actions.push('Open Riester account for Zulagen');
            if (marginalRate >= 35 && !hasRurup) actions.push('Consider Rürup for tax deduction');
            if (!hasInsurance) actions.push('Review insurance coverage (BU, Haftpflicht, Risikoleben)');
            if (monthlyBudget > 0) actions.push(`Start ${fmt(monthlyBudget)}/mo contribution routing`);
            actions.push('Set up Freistellungsauftrag (1.000 EUR)');
          }
          if (y > 0) {
            actions.push('Review and adjust Sparplan amounts');
            actions.push(`File Steuererklärung for ${calYear - 1}`);
          }
          if (age === 55) actions.push('Begin retirement withdrawal planning');
          if (age === 60) actions.push('Review Riester/Rürup payout options');
          if (age === 62) actions.push('bAV earliest access — review payout');
          if (age === retireAge) actions.push('Retire — begin withdrawal phase');
          if (y > 0 && y % 3 === 0) actions.push('Scan for tax-loss harvesting opportunities');
          if (y > 0 && y % 5 === 0) actions.push('Rebalance portfolio allocation');

          projNW = y === 0 ? projNW : (projNW + annualContrib) * (1 + returnRate);
          years.push({ year: calYear, age, actions, projectedNW: Math.round(projNW) });
        }

        const display = years.filter((y, i) => i < 10 || y.age === 55 || y.age === 60 || y.age === 62 || y.age === retireAge);

        return (
          <div className="space-y-3 px-1 md:px-0">
            {monthlyBudget === 0 && (
              <p className="text-[11px] text-amber mb-1">Projections assume market growth only (no contributions). Set monthly budget in Contribution Router for accurate projections.</p>
            )}
            {display.map(y => (
              <div key={y.year} className="rounded-xl bg-parchment-deep p-3">
                <div className="flex items-center justify-between mb-2">
                  <div>
                    <span className="text-[14px] font-medium text-ink">{y.year}</span>
                    <span className="text-[12px] text-ink-muted ml-2">Age {y.age}</span>
                  </div>
                  <span className="font-serif text-[15px] tabular-nums text-forest font-medium">{fmt(y.projectedNW)}</span>
                </div>
                <div className="space-y-1">
                  {y.actions.map((a, i) => (
                    <label key={i} className="flex items-start gap-2 text-[12px] text-ink-body cursor-pointer">
                      <input type="checkbox" className="mt-0.5 accent-forest" />
                      <span>{a}</span>
                    </label>
                  ))}
                </div>
              </div>
            ))}
          </div>
        );
      })()}

      <div className="mt-4 px-1 md:px-0">
        <button
          onClick={() => {
            const currentAge = pensionCurrentAge;
            const retireAge = pensionAge;
            const monthlyBudget = parseFloat(crBudget) || 0;
            const nw = totalNetWorth;
            const lines: string[] = [
              `Financial Independence Playbook`,
              `${'='.repeat(40)}`,
              `Generated: ${new Date().toLocaleDateString('de-DE')}`,
              `Current Net Worth: ${fmt(nw)}`,
              `Monthly Budget: ${fmt(monthlyBudget)}`,
              `Current Age: ${currentAge} | Retire at: ${retireAge}`,
              '',
            ];
            let projNW = nw;
            const annualContrib = monthlyBudget * 12;
            for (let y = 0; y <= Math.min(retireAge - currentAge, 30); y++) {
              const age = currentAge + y;
              const calYear = new Date().getFullYear() + y;
              projNW = y === 0 ? projNW : (projNW + annualContrib) * 1.07;
              const show = y < 10 || age === 55 || age === 60 || age === 62 || age === retireAge;
              if (show) {
                lines.push(`${calYear} (Age ${age}) — Projected: ${fmt(Math.round(projNW))}`);
                if (y === 0) {
                  lines.push(`  [ ] Set up contribution routing (${fmt(monthlyBudget)}/mo)`);
                  lines.push(`  [ ] Review insurance coverage`);
                  lines.push(`  [ ] Set up Freistellungsauftrag`);
                }
                if (y > 0) lines.push(`  [ ] File Steuererklärung for ${calYear - 1}`);
                if (y > 0) lines.push(`  [ ] Review and adjust Sparplan amounts`);
                if (y > 0 && y % 3 === 0) lines.push(`  [ ] Scan for tax-loss harvesting`);
                if (y > 0 && y % 5 === 0) lines.push(`  [ ] Rebalance portfolio`);
                if (age === 55) lines.push(`  [ ] Begin retirement withdrawal planning`);
                if (age === 60) lines.push(`  [ ] Review Riester/Rürup payout options`);
                if (age === 62) lines.push(`  [ ] bAV earliest access — review payout`);
                if (age === retireAge) lines.push(`  [ ] Retire — begin withdrawal phase`);
                lines.push('');
              }
            }
            lines.push(`Generated by Wealth on ${new Date().toLocaleDateString('de-DE')}`);
            const blob = new Blob([lines.join('\n')], { type: 'text/plain' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url; a.download = `playbook-${new Date().getFullYear()}.txt`;
            a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
          }}
          className="apple-btn-secondary text-[13px] px-4 py-1.5"
        >
          Export Playbook
        </button>
      </div>
    </div>
  );
}
