import { Link } from 'react-router-dom';
import { EmptyState, MetricCard, MetricGrid, Section, SectionBoundary } from '../../components/ui';
import { fmtDate, fmtEUR, fmtMonth, fmtPct, labelFor } from '../../utils/format';
import { Loaded, Money, ShareBars, Statement, arr, getJson, num, plural, useLoad } from './shared';
import type { LoadState } from './shared';

interface SpendingData {
  /** Category keys are the Cashflow page's (housing, groceries, …). */
  categories?: { category: string; total: number; count: number; avg_monthly: number }[] | null;
  subscriptions?: { name: string; amount: number; occurrences: number; cadence?: string; last_charged?: string; annual_cost: number }[] | null;
  total_income: number;
  total_expenses: number;
  savings_rate_pct: number;
  /** Last 12 calendar months ending this month; null when they hold no income. */
  savings_rate_pct_12m: number | null;
  /** Months of data inside that window. */
  window_months: number;
  window_from?: string;
  window_to?: string;
  avg_monthly_expense: number;
  /** Calendar months from the first to the latest checking/savings transaction, empty months included. */
  months_analyzed: number;
  span_from?: string;
  span_to?: string;
  unclassified_inflow?: number;
  unclassified_inflow_count?: number;
}

/** "2026-05" → "May 2026", without time-zone shifts. */
const monthLabel = (ym?: string) => {
  if (!ym) return '';
  const [y, m] = ym.split('-');
  return fmtMonth(new Date(Number(y), Number(m) - 1, 1));
};

const categoryLabel = (c: string) => (c === 'internal' ? 'Internal transfer' : labelFor(c));

/**
 * Which savings rate to headline. The last 12 calendar months when they hold data
 * and are shorter than the whole history; otherwise the whole history.
 * Example: 24 months of data, rate 30 % in the last 12 and 16.7 % overall →
 * "Savings rate, last 12 months: 30 %", sublabel "Dec 2025 to Nov 2026 · All time: 16,7 %".
 */
function savingsRateHeadline(d: SpendingData): { label: string; value: number; sublabel: string } {
  const months = num(d.months_analyzed);
  const windowed = d.savings_rate_pct_12m != null && num(d.window_months) > 0 && num(d.window_months) < months;
  if (windowed) {
    return {
      label: 'Savings rate, last 12 months',
      value: num(d.savings_rate_pct_12m),
      sublabel: `${monthLabel(d.window_from)} to ${monthLabel(d.window_to)} · All time: ${fmtPct(d.savings_rate_pct)}`,
    };
  }
  return {
    label: 'Savings rate',
    value: num(d.savings_rate_pct),
    sublabel: d.savings_rate_pct_12m == null
      ? `Over ${plural(months, 'month', 'months')}, none in the last 12`
      : `Over ${plural(months, 'month', 'months')}`,
  };
}

/**
 * What the Cashflow page does not show: the savings rate, long-run spending by category
 * and detected subscriptions. Same accounts and categories as the Cashflow page.
 */
export default function SpendingTab() {
  const spending = useLoad(() => getJson<SpendingData>('/api/analysis/spending'));
  return (
    <>
      <p className="mt-5 text-[14px] text-ink-body">
        Savings rate, long-run spending by category and recurring charges, from your checking and savings accounts with the categories of the{' '}
        <Link to="/cashflow" className="font-medium text-forest underline underline-offset-2">Cashflow page</Link>, where month-by-month income and spending are shown.
        Transfers between your own accounts and into investments count as neither income nor spending.
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
          const headline = savingsRateHeadline(d);
          const span = d.span_from && d.span_to ? `${monthLabel(d.span_from)} to ${monthLabel(d.span_to)}` : 'All time';
          const unclassified = num(d.unclassified_inflow_count);
          return (
            <>
              <MetricGrid>
                <MetricCard money={false} label={headline.label} value={fmtPct(headline.value)}
                  tone={headline.value < 0 ? 'negative' : 'neutral'} sublabel={headline.sublabel} />
                <MetricCard label="Average monthly spending" value={fmtEUR(d.avg_monthly_expense)}
                  sublabel={`Over ${plural(months, 'month', 'months')}, empty months included`} />
                <MetricCard label="Total income" value={fmtEUR(d.total_income)} sublabel={span} />
                <MetricCard label="Total spending" value={fmtEUR(d.total_expenses)} sublabel={span} />
              </MetricGrid>
              {unclassified > 0 && (
                <p className="mt-3 text-[13px] text-ink-muted">
                  <Money value={d.unclassified_inflow} /> from {plural(unclassified, 'incoming payment', 'incoming payments')} matched no category and is not counted as income: it often is money moved between your own accounts.{' '}
                  <Link to="/transactions" className="font-medium text-forest underline underline-offset-2">Categorise it</Link> if it is income.
                </p>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

function Categories({ spending }: { spending: LoadState<SpendingData> }) {
  return (
    <Section title="Spending by Category" subtitle="Average per month from your first to your latest transaction, months without spending included. Largest first.">
      <Loaded state={spending} lines={5}>
        {d => {
          const list = arr(d?.categories).filter(c => num(c.total) > 0);
          if (list.length === 0) return <Statement>No categorised spending yet.</Statement>;
          const total = list.reduce((s, c) => s + c.total, 0);
          if (list.length === 1) {
            return <Statement>All recorded spending falls into 1 category: {categoryLabel(list[0].category)}, about <Money value={list[0].avg_monthly} /> a month.</Statement>;
          }
          return (
            <ShareBars items={list.map(c => ({
              label: categoryLabel(c.category),
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
    <Section title="Detected Subscriptions" subtitle="The same payee charging the same amount at least three times at a regular interval. The yearly cost follows from that interval; charges that have stopped are left out.">
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
                    <span className="text-ink-muted">
                      {s.cadence ? ` · ${s.cadence}` : ''} · charged {plural(num(s.occurrences), 'time', 'times')}
                      {s.last_charged ? `, last on ${fmtDate(s.last_charged)}` : ''}
                    </span>
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
