import { Link } from 'react-router-dom';
import { EmptyState, MetricCard, MetricGrid, Section, SectionBoundary } from '../../components/ui';
import { fmtEUR, fmtPct } from '../../utils/format';
import { Loaded, Money, ShareBars, Statement, arr, getJson, num, plural, useLoad } from './shared';
import type { LoadState } from './shared';

interface SpendingData {
  categories?: { category: string; total: number; count: number; avg_monthly: number }[] | null;
  subscriptions?: { name: string; amount: number; occurrences: number; annual_cost: number }[] | null;
  total_income: number;
  total_expenses: number;
  savings_rate_pct: number;
  savings_rate_pct_12m: number;
  window_months: number;
  avg_monthly_expense: number;
  months_analyzed: number;
}

/**
 * What the Cashflow page does not show: the savings rate, all-time spending by category
 * and detected subscriptions. Monthly income and spending charts live on Cashflow only.
 */
export default function SpendingTab() {
  const spending = useLoad(() => getJson<SpendingData>('/api/analysis/spending'));
  return (
    <>
      <p className="mt-5 text-[14px] text-ink-body">
        Savings rate, long-run spending by category and recurring charges. Month-by-month income and spending are on the{' '}
        <Link to="/cashflow" className="font-medium text-forest underline underline-offset-2">Cashflow page</Link>.
      </p>
      <SectionBoundary><SavingsRate spending={spending} /></SectionBoundary>
      <SectionBoundary><Categories spending={spending} /></SectionBoundary>
      <SectionBoundary><Subscriptions spending={spending} /></SectionBoundary>
    </>
  );
}

function NoSpending() {
  return (
    <EmptyState
      title="No spending data yet"
      message="Import the transactions of a current account to see your savings rate, spending by category and subscriptions."
      action={{ label: 'Import transactions', href: '/transactions' }}
    />
  );
}

function SavingsRate({ spending }: { spending: LoadState<SpendingData> }) {
  return (
    <Section title="Savings Rate" subtitle="The share of recorded income left after recorded spending.">
      <Loaded state={spending} lines={3}>
        {d => {
          const months = num(d?.months_analyzed);
          if (months <= 0) return <NoSpending />;
          const windowed = num(d.window_months) > 0 && num(d.window_months) < months;
          return (
            <MetricGrid>
              <MetricCard money={false} label={windowed ? `Savings rate, last ${plural(d.window_months, 'month', 'months')}` : 'Savings rate'}
                value={fmtPct(d.savings_rate_pct_12m)} tone={num(d.savings_rate_pct_12m) < 0 ? 'negative' : 'neutral'}
                sublabel={windowed ? `All time: ${fmtPct(d.savings_rate_pct)}` : `Over ${plural(months, 'month', 'months')}`} />
              <MetricCard label="Average monthly spending" value={fmtEUR(d.avg_monthly_expense)} sublabel={`Over ${plural(months, 'month', 'months')}`} />
              <MetricCard label="Total income" value={fmtEUR(d.total_income)} sublabel="All time" />
              <MetricCard label="Total spending" value={fmtEUR(d.total_expenses)} sublabel="All time" />
            </MetricGrid>
          );
        }}
      </Loaded>
    </Section>
  );
}

function Categories({ spending }: { spending: LoadState<SpendingData> }) {
  return (
    <Section title="Spending by Category" subtitle="Average per month over the whole history, largest first.">
      <Loaded state={spending} lines={5}>
        {d => {
          const list = arr(d?.categories).filter(c => num(c.total) > 0);
          if (list.length === 0) return <Statement>No categorised spending yet.</Statement>;
          const total = list.reduce((s, c) => s + c.total, 0);
          if (list.length === 1) {
            return <Statement>All recorded spending falls into 1 category: {list[0].category}, about <Money value={list[0].avg_monthly} /> a month.</Statement>;
          }
          return (
            <ShareBars items={list.map(c => ({
              label: c.category,
              value: c.total,
              money: true,
              display: `${fmtEUR(c.avg_monthly)} / month`,
              share: fmtPct(total > 0 ? (c.total / total) * 100 : 0),
            }))} />
          );
        }}
      </Loaded>
    </Section>
  );
}

function Subscriptions({ spending }: { spending: LoadState<SpendingData> }) {
  return (
    <Section title="Detected Subscriptions" subtitle="The same payee charging the same amount three times or more.">
      <Loaded state={spending} lines={3}>
        {d => {
          const list = arr(d?.subscriptions);
          if (list.length === 0) return <Statement>No recurring charges detected.</Statement>;
          return (
            <ul className="divide-y divide-divider border-y border-divider">
              {list.map(s => (
                <li key={`${s.name}-${s.amount}`} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-2 text-[13px]">
                  <span className="min-w-0">
                    <span className="font-medium text-ink capitalize">{s.name}</span>
                    <span className="text-ink-muted"> · charged {plural(num(s.occurrences), 'time', 'times')}</span>
                  </span>
                  <span className="text-ink-body">
                    <Money value={s.amount} cents className="text-ink" /> each · <Money value={s.annual_cost} /> a year
                  </span>
                </li>
              ))}
            </ul>
          );
        }}
      </Loaded>
    </Section>
  );
}
