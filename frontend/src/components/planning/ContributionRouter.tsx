import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';

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
  const { accounts, fireExpenses, crBudget, setCrBudget, crMarginalRate, setCrMarginalRate, crChildren, setCrChildren, fmt } = usePlanning();
  const tc = useThemeColors();

  const marginalRate = parseFloat(crMarginalRate) || 42;
  const numChildren = parseInt(crChildren) || 0;
  const monthlyBudget = parseFloat(crBudget) || 0;
  const reserveMonths = 6;

  const totalCash = accounts.reduce((s, a) => s + (a.cash_balance ?? a.balance ?? 0), 0);
  const monthlyExpenses = parseFloat(fireExpenses || '0') / 12;
  const reserveTarget = monthlyExpenses * reserveMonths;
  const reserveGap = Math.max(0, reserveTarget - totalCash);

  const bavAccounts = accounts.filter(a => a.tax_treatment === 'bav');
  const bavMatchPct = bavAccounts.length > 0 ? (bavAccounts[0].employer_match_pct ?? 0) : 0;
  const bavMonthly = Math.min(BAV_MAX_ANNUAL / 12, monthlyBudget > 0 ? monthlyBudget * 0.3 : BAV_MAX_ANNUAL / 12);

  const hasRiester = accounts.some(a => a.tax_treatment === 'riester');
  const riesterMonthly = RIESTER_MAX_ANNUAL / 12;
  const riesterZulagen = RIESTER_GRUNDZULAGE + numChildren * RIESTER_KINDERZULAGE;
  const riesterBenefitPct = hasRiester ? Math.round((riesterZulagen / RIESTER_MAX_ANNUAL) * 100 + marginalRate * 0.5) : 0;

  const hasRurup = accounts.some(a => a.tax_treatment === 'rurup');
  const rurupMonthly = RURUP_MAX_ANNUAL / 12;
  const rurupBenefitPct = hasRurup ? Math.round(marginalRate * 0.9) : 0;

  const tiers: { label: string; monthly: number; benefit: string; liquidity: string; color: string }[] = [];
  if (reserveGap > 0) tiers.push({ label: 'Emergency Reserve', monthly: Math.min(reserveGap, monthlyBudget > 0 ? monthlyBudget * 0.2 : reserveGap / 6), benefit: 'Safety net', liquidity: 'Immediate', color: tc.slate });
  if (bavAccounts.length > 0 && bavMatchPct > 0) tiers.push({ label: 'bAV Match', monthly: bavMonthly, benefit: `${bavMatchPct}% free match`, liquidity: 'Age 62+', color: tc.forest });
  if (hasRiester) tiers.push({ label: 'Riester Zulagen', monthly: riesterMonthly, benefit: `${riesterBenefitPct}% eff. benefit`, liquidity: 'Age 62+', color: tc.sage });
  if (hasRurup) tiers.push({ label: 'Rürup Deduction', monthly: rurupMonthly, benefit: `${rurupBenefitPct}% tax benefit`, liquidity: 'Age 62+', color: tc.gold });
  const allocatedMonthly = tiers.reduce((s, t) => s + t.monthly, 0);
  const brokerageMonthly = monthlyBudget > 0 ? Math.max(0, monthlyBudget - allocatedMonthly) : 0;
  if (brokerageMonthly > 0 || tiers.length === 0) tiers.push({ label: 'Taxable Brokerage', monthly: brokerageMonthly, benefit: '~18.5% eff. tax', liquidity: 'Anytime', color: tc.walnut });

  const waterfallOption = tiers.length > 0 ? {
    tooltip: { trigger: 'axis' as const },
    xAxis: { type: 'category' as const, data: tiers.map(t => t.label), axisLabel: { fontSize: 11, color: tc.inkMuted, rotate: 30 }, axisLine: { show: false }, axisTick: { show: false } },
    yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => `${Math.round(v)}`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
    series: [{ type: 'bar' as const, data: tiers.map(t => ({ value: Math.round(t.monthly), itemStyle: { color: t.color, borderRadius: [4, 4, 0, 0] } })), barMaxWidth: 50 }],
    grid: { left: 50, right: 16, top: 10, bottom: 60 },
  } : null;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Contribution Router</h2>
      <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">Optimal monthly allocation order based on your account types and tax situation.</p>

      <div className="grid grid-cols-2 md:grid-cols-3 gap-2 mb-4 px-1 md:px-0">
        <div>
          <label className="text-[11px] text-ink-muted block mb-1">Monthly Budget</label>
          <input aria-label="Monthly budget" type="number" value={crBudget} onChange={e => { setCrBudget(e.target.value); localStorage.setItem('monthly_invest_budget', e.target.value); }} placeholder="e.g. 1500" className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
        <div>
          <label className="text-[11px] text-ink-muted block mb-1">Marginal Tax Rate</label>
          <div className="flex items-center gap-1">
            <input aria-label="Marginal tax rate" type="number" value={crMarginalRate} onChange={e => { setCrMarginalRate(e.target.value); localStorage.setItem('marginal_tax_rate', e.target.value); }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
            <span className="text-[13px] text-ink-muted">%</span>
          </div>
        </div>
        <div>
          <label className="text-[11px] text-ink-muted block mb-1">Children (Riester)</label>
          <input aria-label="Number of children" type="number" min="0" value={crChildren} onChange={e => { setCrChildren(e.target.value); localStorage.setItem('num_children', e.target.value); }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
      </div>

      {waterfallOption && monthlyBudget > 0 && <EChartWrapper option={waterfallOption} height="220px" />}

      {tiers.length > 0 && monthlyBudget > 0 && (
        <div className="mt-3 px-1 md:px-0">
          <table className="w-full text-[13px]">
            <thead>
              <tr className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] border-b border-divider">
                <th className="text-left py-2 font-medium">Priority</th>
                <th className="text-right py-2 font-medium">Monthly</th>
                <th className="text-right py-2 font-medium">Benefit</th>
                <th className="text-right py-2 font-medium hidden md:table-cell">Liquidity</th>
              </tr>
            </thead>
            <tbody>
              {tiers.map((t, i) => (
                <tr key={i} className="border-b border-divider">
                  <td className="py-2 text-ink">{i + 1}. {t.label}</td>
                  <td className="py-2 text-right tabular-nums font-medium">{fmt(t.monthly)}</td>
                  <td className="py-2 text-right text-sage">{t.benefit}</td>
                  <td className="py-2 text-right text-ink-muted hidden md:table-cell">{t.liquidity}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {monthlyBudget === 0 && (
        <p className="text-[13px] text-ink-muted px-1 md:px-0">Enter your monthly investment budget to see the optimal routing.</p>
      )}

      {monthlyBudget > 0 && tiers.length > 0 && (
        <div className="mt-4 px-1 md:px-0">
          <button
            onClick={() => {
              const year = new Date().getFullYear();
              const lines = [
                `Contribution Routing Checklist ${year}`,
                `${'='.repeat(40)}`,
                `Monthly Budget: ${fmt(monthlyBudget)}`,
                `Marginal Tax Rate: ${marginalRate}%`,
                `Children: ${numChildren}`,
                '',
                'Priority Order:',
                ...tiers.map((t, i) => `  ${i + 1}. ${t.label}: ${fmt(t.monthly)}/mo — ${t.benefit} (${t.liquidity})`),
                '',
                'Action Items:',
                ...tiers.map((t, i) => `  [ ] ${i + 1}. Set up ${fmt(t.monthly)}/mo to ${t.label}`),
                '',
                `Total: ${fmt(tiers.reduce((s, t) => s + t.monthly, 0))}/mo`,
                '',
                `Generated by Wealth on ${new Date().toLocaleDateString('de-DE')}`,
              ];
              const blob = new Blob([lines.join('\n')], { type: 'text/plain' });
              const url = URL.createObjectURL(blob);
              const a = document.createElement('a');
              a.href = url; a.download = `contribution-checklist-${year}.txt`;
              a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
            }}
            className="apple-btn-secondary text-[13px] px-4 py-1.5"
          >
            Export Checklist
          </button>
        </div>
      )}
    </div>
  );
}
