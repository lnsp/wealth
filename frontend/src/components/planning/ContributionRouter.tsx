import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import { Section, SubHeading, EmptyState } from '../ui';
import { fmtEUR, fmtPct, fmtNum, fmtAxis, fmtDate } from '../../utils/format';
import { Assumptions, ScrollTable, downloadText } from './shared';

// Limits from §3 Nr. 63 EStG (bAV), §82 EStG (Riester), §10 EStG (Rürup).
// 2026 values; update annually.
const BAV_MAX_ANNUAL = 7248;
const RIESTER_MAX_ANNUAL = 2100;
const RIESTER_GRUNDZULAGE = 175;
const RIESTER_KINDERZULAGE = 300;
const RURUP_MAX_ANNUAL = 27566;

// Builds the optimal monthly allocation waterfall: Emergency Reserve →
// bAV match (free money) → Riester (Zulagen + tax deduction) → Rürup
// (tax deduction) → Taxable Brokerage. Tier order matters because each
// step "borrows" from monthlyBudget before the next is sized.
export default function ContributionRouter() {
  const { accounts, fireExpenses, crBudget, crMarginalRate, crChildren } = usePlanning();
  const tc = useThemeColors();

  const marginalRate = parseFloat(crMarginalRate) || 42;
  const numChildren = parseInt(crChildren, 10) || 0;
  const monthlyBudget = parseFloat(crBudget) || 0;
  const reserveMonths = 6;

  const totalCash = accounts.reduce((s, a) => {
    if (!a.is_active) return s;
    if (a.type === 'checking' || a.type === 'savings') {
      return s + Math.max(0, a.balance ?? 0);
    }
    if (a.type === 'brokerage' || a.type === 'crypto') {
      return s + Math.max(0, a.cash_balance ?? 0);
    }
    return s;
  }, 0);
  const monthlyExpenses = parseFloat(fireExpenses || '0') / 12;
  const reserveTarget = monthlyExpenses * reserveMonths;
  const reserveGap = Math.max(0, reserveTarget - totalCash);

  const bavAccounts = accounts.filter(a => a.tax_treatment === 'bav' && a.is_active);
  const bavMatchPct = bavAccounts.length > 0 ? (bavAccounts[0].employer_match_pct ?? 0) : 0;

  const hasRiester = accounts.some(a => a.tax_treatment === 'riester' && a.is_active);
  const riesterZulagen = RIESTER_GRUNDZULAGE + numChildren * RIESTER_KINDERZULAGE;
  const riesterBenefitPct = hasRiester ? Math.round((riesterZulagen / RIESTER_MAX_ANNUAL) * 100 + marginalRate * 0.5) : 0;

  const hasRurup = accounts.some(a => a.tax_treatment === 'rurup' && a.is_active);
  const rurupBenefitPct = hasRurup ? Math.round(marginalRate * 0.9) : 0;

  const tiers: { label: string; short: string; monthly: number; benefit: string; liquidity: string; color: string }[] = [];
  let remainingBudget = monthlyBudget;
  const takeFromBudget = (desired: number): number => {
    if (monthlyBudget <= 0) return desired;
    const alloc = Math.max(0, Math.min(remainingBudget, desired));
    remainingBudget = Math.max(0, remainingBudget - alloc);
    return alloc;
  };

  if (reserveGap > 0) {
    const desired = monthlyBudget > 0 ? Math.min(reserveGap, monthlyBudget * 0.2) : reserveGap / 6;
    const alloc = takeFromBudget(desired);
    if (alloc > 0) tiers.push({ label: 'Emergency reserve', short: 'Reserve', monthly: alloc, benefit: 'Safety net', liquidity: 'Anytime', color: tc.slate });
  }
  if (bavAccounts.length > 0 && bavMatchPct > 0) {
    const desired = monthlyBudget > 0 ? Math.min(BAV_MAX_ANNUAL / 12, monthlyBudget * 0.3) : BAV_MAX_ANNUAL / 12;
    const alloc = takeFromBudget(desired);
    if (alloc > 0) tiers.push({ label: 'Company pension (bAV)', short: 'Company pension', monthly: alloc, benefit: `Employer adds ${fmtPct(bavMatchPct, { digits: 0 })}`, liquidity: 'From age 62', color: tc.forest });
  }
  if (hasRiester) {
    const alloc = takeFromBudget(RIESTER_MAX_ANNUAL / 12);
    if (alloc > 0) tiers.push({ label: 'Riester pension', short: 'Riester', monthly: alloc, benefit: `About ${fmtPct(riesterBenefitPct, { digits: 0 })} from allowances and tax relief`, liquidity: 'From age 62', color: tc.sage });
  }
  if (hasRurup) {
    const alloc = takeFromBudget(RURUP_MAX_ANNUAL / 12);
    if (alloc > 0) tiers.push({ label: 'Rürup pension', short: 'Rürup', monthly: alloc, benefit: `About ${fmtPct(rurupBenefitPct, { digits: 0 })} tax relief`, liquidity: 'From age 62', color: tc.gold });
  }
  const brokerageMonthly = monthlyBudget > 0 ? remainingBudget : 0;
  if (brokerageMonthly > 0 || tiers.length === 0) {
    tiers.push({ label: 'Brokerage account', short: 'Brokerage', monthly: brokerageMonthly, benefit: 'About 18,5 % tax on gains', liquidity: 'Anytime', color: tc.walnut });
  }

  const waterfallOption = {
    tooltip: { trigger: 'axis' as const, valueFormatter: (v: unknown) => fmtEUR(Number(v)) },
    xAxis: { type: 'category' as const, data: tiers.map(t => t.short), axisLabel: { fontSize: 11, rotate: 30, interval: 0 } },
    yAxis: { type: 'value' as const, name: '€ / month', min: 0, axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
    series: [{ name: 'Monthly amount', type: 'bar' as const, data: tiers.map(t => ({ value: Math.round(t.monthly), itemStyle: { color: t.color, borderRadius: [2, 2, 0, 0] } })), barMaxWidth: 50 }],
    grid: { left: 48, right: 16, top: 32, bottom: 60 },
  };

  const exportChecklist = () => {
    const year = new Date().getFullYear();
    downloadText(`contribution-checklist-${year}.txt`, [
      `Contribution routing checklist ${year}`,
      `${'='.repeat(40)}`,
      `Monthly budget: ${fmtEUR(monthlyBudget)}`,
      `Marginal tax rate: ${fmtPct(marginalRate, { digits: 0 })}`,
      `Children: ${numChildren}`,
      '',
      'Priority order:',
      ...tiers.map((t, i) => `  ${i + 1}. ${t.label}: ${fmtEUR(t.monthly)} a month — ${t.benefit} (${t.liquidity})`),
      '',
      'Action items:',
      ...tiers.map((t, i) => `  [ ] ${i + 1}. Set up ${fmtEUR(t.monthly)} a month to ${t.label}`),
      '',
      `Total: ${fmtEUR(tiers.reduce((s, t) => s + t.monthly, 0))} a month`,
      '',
      `Generated by Wealth on ${fmtDate(new Date())}`,
    ]);
  };

  return (
    <Section
      id="contribution-router"
      title="Contribution Router"
      subtitle="Where each month’s savings should go first, given your accounts and tax rate."
    >
      {monthlyBudget <= 0 ? (
        <EmptyState
          title="Monthly budget needed"
          message="Enter your monthly budget in Your Profile at the top of this page to see how to split it."
          action={{ label: 'Go to Your Profile', href: '#profile' }}
        />
      ) : (
        <>
          <SubHeading>Monthly amount by destination, in priority order</SubHeading>
          <EChartWrapper option={waterfallOption} height="240px" />

          <div className="mt-3">
            <ScrollTable label="Contribution priorities">
              <table className="apple-table">
                <thead>
                  <tr>
                    <th scope="col">Priority</th>
                    <th scope="col" className="num">Monthly</th>
                    <th scope="col">Why</th>
                    <th scope="col" className="hidden md:table-cell">Access</th>
                  </tr>
                </thead>
                <tbody>
                  {tiers.map((t, i) => (
                    <tr key={t.label}>
                      <td className="text-ink">{i + 1}. {t.label}</td>
                      <td className="num font-medium text-ink whitespace-nowrap" data-privacy="blur">{fmtEUR(t.monthly)}</td>
                      <td>{t.benefit}</td>
                      <td className="hidden md:table-cell">{t.liquidity}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </ScrollTable>
          </div>

          <div className="mt-4">
            <button type="button" onClick={exportChecklist} className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">
              Export checklist
            </button>
          </div>

          <Assumptions>
            <p>
              Uses the <span data-privacy="blur">{fmtEUR(monthlyBudget)}</span> monthly budget, {fmtPct(marginalRate, { digits: 0 })} marginal tax rate and {numChildren} {numChildren === 1 ? 'child' : 'children'} from Your Profile.
            </p>
            <p>
              Emergency reserve: {fmtNum(reserveMonths)} months of expenses
              {monthlyExpenses > 0
                ? <> (<span data-privacy="blur">{fmtEUR(reserveTarget)}</span>, from the annual expenses in the FIRE Calculator), filled with at most 20 % of the budget.</>
                : <>. No annual expenses are entered in the FIRE Calculator, so no reserve is planned.</>}
            </p>
            <p>
              Yearly maximums: company pension {fmtEUR(BAV_MAX_ANNUAL)} (at most 30 % of the budget), Riester {fmtEUR(RIESTER_MAX_ANNUAL)}, Rürup {fmtEUR(RURUP_MAX_ANNUAL)}.
              Only account types you already have are included; whatever is left goes to the brokerage account.
            </p>
          </Assumptions>
        </>
      )}
    </Section>
  );
}
