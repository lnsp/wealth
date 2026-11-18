import { Fragment, useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type CashflowBucket, type CashflowData, type CashflowMonth } from '../api/client';
import {
  PageHeader, Section, MetricCard, MetricGrid, RankedBars,
  LoadingState, ErrorState, EmptyState, SectionBoundary,
  type RankedBarItem,
} from '../components/ui';
import { fmtEUR, fmtNum, fmtPct, fmtMonth, fmtMonthShort, fmtDateTime, labelFor } from '../utils/format';

/** 'YYYY-MM' → local Date on the 1st, so month labels never shift across time zones. */
const monthDate = (ym: string) => {
  const [y, m] = ym.split('-');
  return new Date(Number(y), Number(m) - 1, 1);
};

const CATEGORY_LABELS: Record<string, string> = {
  internal: 'Own accounts',
  investment: 'Investments',
  loan_principal: 'Loan principal',
  fees: 'Bank fees',
  unclassified: 'Not yet categorised',
};
const categoryLabel = (c: string) => CATEGORY_LABELS[c] ?? labelFor(c);

/** A month counts as imported when any cash row landed in it, transfers included. */
const hasActivity = (m: CashflowMonth) =>
  m.has_data || m.income !== 0 || m.fixed !== 0 || m.variable !== 0 || m.transfer !== 0 || m.unclassified !== 0;

/**
 * Whole-euro cells that add up across the row: net is rounded income minus rounded spend,
 * so 100,40 / 50,60 shows 100 − 51 = 49, not a separately rounded 50.
 */
function rowFigures(m: CashflowMonth) {
  const income = Math.round(m.income);
  const spend = Math.round(m.fixed + m.variable);
  return { income, spend, net: income - spend };
}

/**
 * Position of one month's net on a trend axis that always includes zero, in % of the track.
 * Example: nets 3.900, 3.563 and −500 span −500…3.900, so zero sits at 11,4 %;
 * 3.900 runs from 11,4 % to 100 %, −500 from 0 % to 11,4 %.
 */
function trendBar(net: number, min: number, max: number): { left: number; width: number; zero: number } {
  const lo = Math.min(0, min);
  const hi = Math.max(0, max);
  const span = hi - lo;
  if (span <= 0 || net === 0) return { left: 0, width: 0, zero: span > 0 ? (-lo / span) * 100 : 0 };
  const zero = (-lo / span) * 100;
  const at = ((net - lo) / span) * 100;
  const width = Math.max(1, Math.abs(at - zero));
  return net > 0
    ? { left: zero, width: Math.min(width, 100 - zero), zero }
    : { left: Math.max(0, zero - width), width: Math.min(width, zero), zero };
}

/**
 * What "Apply to Planning" writes. A non-positive typical net is a budget of 0, not
 * "keep the old value"; with no spending measured the FIRE figure is cleared (null).
 */
function planningValues(m: CashflowData['medians']): { investBudget: number; fireExpenses: number | null } {
  return {
    investBudget: Math.max(0, Math.round(m.monthly_surplus)),
    fireExpenses: m.monthly_spend > 0 ? Math.round(m.monthly_spend * 12) : null,
  };
}

/** Whole-euro figure without the symbol (the table caption names the unit). */
const plain = (n: number) => fmtNum(n);
const signed = (n: number) => `${n > 0 ? '+' : ''}${fmtNum(n)}`;
const netTone = (n: number) => (n > 0 ? 'text-sage' : n < 0 ? 'text-claret' : 'text-ink');

// Month, Income, Spend, Net fit a phone; desktop adds a Trend column.
const GRID = 'grid grid-cols-[86px_1fr_1fr_1fr] md:grid-cols-[130px_1fr_1fr_1fr_140px] items-center';

type RowGroup =
  | { kind: 'month'; month: CashflowMonth }
  | { kind: 'gap'; newest: string; oldest: string };

/** Merge buckets by label into ranked items. Shares only where every item is positive. */
function toItems(list: CashflowBucket[], withSign = false): { items: RankedBarItem[]; total: number } {
  const merged = new Map<string, number>();
  for (const b of list) {
    const label = categoryLabel(b.category);
    merged.set(label, (merged.get(label) ?? 0) + b.amount);
  }
  const entries = [...merged.entries()].filter(([, v]) => Math.round(v) !== 0).sort((a, b) => Math.abs(b[1]) - Math.abs(a[1]));
  const total = entries.reduce((s, [, v]) => s + v, 0);
  const allPositive = !withSign && entries.every(([, v]) => v > 0);
  return {
    total,
    items: entries.map(([label, value]) => ({
      label,
      value,
      display: fmtEUR(value, { sign: withSign }),
      share: allPositive && total > 0 ? fmtPct((value / total) * 100) : undefined,
    })),
  };
}

function Chevron({ open }: { open: boolean }) {
  return (
    <svg className="w-3.5 h-3.5 shrink-0 text-ink-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={open ? 'M6 9l6 6 6-6' : 'M9 6l6 6-6 6'} />
    </svg>
  );
}

export default function Cashflow() {
  const [data, setData] = useState<CashflowData | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [selectedMonth, setSelectedMonth] = useState<string>('');
  const [appliedAt, setAppliedAt] = useState<number | null>(null);

  const load = useCallback(() => {
    let cancelled = false;
    setLoading(true);
    setErr(null);
    api.getCashflow(12)
      .then(d => {
        if (cancelled) return;
        setData(d);
        // Open the most recent month that has activity.
        const first = (d.months || []).find(hasActivity);
        setSelectedMonth(first ? first.month : '');
      })
      .catch(e => { if (!cancelled) setErr(e instanceof Error ? e.message : 'Failed to load cashflow'); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => load(), [load]);

  const months = useMemo(() => data?.months ?? [], [data]);

  // Collapse runs of months without imported data into one line.
  const rows = useMemo<RowGroup[]>(() => {
    const out: RowGroup[] = [];
    for (const m of months) { // newest first
      if (hasActivity(m)) {
        out.push({ kind: 'month', month: m });
        continue;
      }
      const last = out[out.length - 1];
      if (last && last.kind === 'gap') last.oldest = m.month;
      else out.push({ kind: 'gap', newest: m.month, oldest: m.month });
    }
    return out;
  }, [months]);

  const breakdown = useMemo(() => {
    const buckets = (data?.buckets ?? []).filter(b => b.month === selectedMonth);
    return {
      spend: toItems(buckets.filter(b => b.bucket === 'fixed' || b.bucket === 'variable')),
      income: toItems(buckets.filter(b => b.bucket === 'income')),
      notCounted: toItems(buckets.filter(b => b.bucket === 'transfer' || b.bucket === 'unclassified'), true),
    };
  }, [data, selectedMonth]);

  const applyToPlanning = () => {
    if (!data) return;
    const v = planningValues(data.medians);
    try {
      localStorage.setItem('monthly_invest_budget', String(v.investBudget));
      if (v.fireExpenses === null) localStorage.removeItem('fire_expenses');
      else localStorage.setItem('fire_expenses', String(v.fireExpenses));
      // Gross income is not written: take-home pay is after tax and social
      // contributions, and Planning's insurance and human-capital figures need gross.
    } catch { /* storage unavailable */ }
    setAppliedAt(Date.now());
  };

  if (loading) {
    return (
      <div>
        <PageHeader title="Cashflow" />
        <LoadingState message="Loading cashflow" lines={6} />
      </div>
    );
  }

  if (err) {
    return (
      <div className="space-y-4">
        <PageHeader title="Cashflow" />
        <ErrorState message="Cashflow couldn’t be loaded." details={err} onRetry={load} />
      </div>
    );
  }

  if (!data || !months.some(hasActivity)) {
    return (
      <div className="space-y-4">
        <PageHeader title="Cashflow" />
        <EmptyState
          title="No cashflow data yet"
          message="Import current-account or savings transactions. About three months of activity are needed for a useful 12-month summary."
          action={{ label: 'Import transactions', href: '/transactions' }}
        />
      </div>
    );
  }

  const med = data.medians;
  const savingsRate = med.savings_rate_pct;
  const surplus = Math.round(med.monthly_surplus);
  const plan = planningValues(med);
  const canApply = med.months > 0;
  const running = months.find(m => m.partial);
  const unclassifiedTotal = months.reduce((s, m) => s + m.unclassified, 0);
  const unconverted = data.unconverted_currencies ?? [];

  // Trend bar (desktop only): each month's net on an axis anchored at zero.
  const nets = rows.flatMap(r => (r.kind === 'month' ? [rowFigures(r.month).net] : []));
  const maxNet = Math.max(0, ...nets);
  const minNet = Math.min(0, ...nets);

  return (
    <div>
      <PageHeader title="Cashflow" />

      <MetricGrid className="mt-2 md:mt-6">
        <MetricCard label="Typical income" value={canApply ? fmtEUR(med.monthly_income) : '—'} />
        <MetricCard label="Typical spend" value={canApply ? fmtEUR(med.monthly_spend) : '—'} />
        <MetricCard
          label="Typical net"
          value={canApply ? fmtEUR(surplus, { sign: true }) : '—'}
          tone={!canApply ? 'neutral' : surplus > 0 ? 'positive' : surplus < 0 ? 'negative' : 'neutral'}
        />
        <MetricCard
          label="Savings rate"
          value={savingsRate === null ? '—' : fmtPct(savingsRate)}
          tone={savingsRate !== null && savingsRate < 0 ? 'negative' : 'neutral'}
          money={false}
        />
      </MetricGrid>
      <p className="mt-3 text-[13px] text-ink-muted max-w-2xl">
        {canApply
          ? `Medians over the ${med.months} complete ${med.months === 1 ? 'month' : 'months'} with imported income in the past ${med.window_months}`
          : `No complete month in the past ${med.window_months} has imported income yet, so there is no typical month`}
        {running ? `; ${fmtMonth(monthDate(running.month))} is still running and is left out` : ''}. Typical net is
        the median month’s net. The savings rate is total net as a share of total income over those months.
        Transfers between your own accounts and into investments count as neither income nor spending.
      </p>
      {unclassifiedTotal > 0.5 && (
        <p className="mt-2 text-[13px] text-ink-body max-w-2xl">
          <span data-privacy="blur">{fmtEUR(unclassifiedTotal)}</span> of incoming payments matched no category and
          is not counted as income.{' '}
          <Link to="/transactions" className="font-medium text-forest underline underline-offset-2">Categorise them on Transactions</Link>.
        </p>
      )}
      {unconverted.length > 0 && (
        <p className="mt-2 text-[13px] text-ink-body max-w-2xl">
          Transactions in {unconverted.join(', ')} have no exchange rate yet and are left out.
        </p>
      )}

      <SectionBoundary>
        <Section title="Monthly Cashflow" subtitle="Select a month to open its spending. Amounts in EUR.">
          <div className="relative rounded-apple border border-divider overflow-hidden">
            <div className={`${GRID} bg-inset border-b border-divider py-2 pl-1.5 pr-2.5 text-right`} aria-hidden="true">
              <span className="label-caps text-left pl-[18px]">Month</span>
              <span className="label-caps">Income</span>
              <span className="label-caps">Spend</span>
              <span className="label-caps">Net</span>
              <span className="label-caps hidden md:block">Trend</span>
            </div>
            {rows.map((row, idx) => {
              const lastRow = idx === rows.length - 1;
              if (row.kind === 'gap') {
                const range = row.newest === row.oldest
                  ? fmtMonthShort(monthDate(row.newest))
                  : `${fmtMonthShort(monthDate(row.oldest))} to ${fmtMonthShort(monthDate(row.newest))}`;
                return (
                  <div
                    key={`gap-${row.newest}`}
                    className={`flex items-center justify-between gap-3 min-h-[44px] pl-3 pr-1 text-[13px] text-ink-muted ${lastRow ? '' : 'border-b border-divider'}`}
                  >
                    <span>{range}: nothing imported</span>
                    <Link to="/transactions" className="inline-flex items-center min-h-[44px] px-2 font-medium text-forest underline">
                      Import
                    </Link>
                  </div>
                );
              }
              const m = row.month;
              const open = m.month === selectedMonth;
              const fig = rowFigures(m);
              const panelId = `cashflow-${m.month}`;
              const bar = trendBar(fig.net, minNet, maxNet);
              return (
                <Fragment key={m.month}>
                  <button
                    type="button"
                    aria-expanded={open}
                    aria-controls={panelId}
                    onClick={() => setSelectedMonth(open ? '' : m.month)}
                    className={`${GRID} relative w-full min-h-[44px] pl-1.5 pr-2.5 text-right text-[14px] transition-colors hover:bg-inset ${
                      open ? 'bg-inset text-ink font-semibold' : 'text-ink-body'
                    } ${lastRow && !open ? '' : 'border-b border-divider'}`}
                  >
                    <span className="inline-flex items-center gap-1 text-left whitespace-nowrap">
                      <Chevron open={open} />
                      <span>
                        {fmtMonthShort(monthDate(m.month))}
                        {m.partial && <span className="block text-[12px] leading-4 font-normal text-ink-muted">to date</span>}
                        <span className="sr-only">{m.partial ? ', month still running' : ''}, income</span>
                      </span>
                    </span>
                    <span data-privacy="blur">{plain(fig.income)}<span className="sr-only"> €, spend</span></span>
                    <span data-privacy="blur">{plain(fig.spend)}<span className="sr-only"> €, net</span></span>
                    <span data-privacy="blur" className={netTone(fig.net)}>{signed(fig.net)}<span className="sr-only"> €</span></span>
                    <span className="hidden md:block pl-6" aria-hidden="true">
                      <span className="relative block h-2 rounded-[2px] bg-divider overflow-hidden">
                        {minNet < 0 && <span className="absolute inset-y-0 w-px bg-ink-muted" style={{ left: `${bar.zero}%` }} />}
                        <span
                          className={`absolute inset-y-0 ${fig.net < 0 ? 'bg-claret' : 'bg-forest'}`}
                          style={{ left: `${bar.left}%`, width: `${bar.width}%` }}
                        />
                      </span>
                    </span>
                  </button>
                  {open && (
                    <div
                      id={panelId}
                      role="region"
                      aria-label={`Breakdown for ${fmtMonth(monthDate(m.month))}`}
                      className={`bg-inset px-3 pt-3.5 pb-4 grid gap-x-10 gap-y-5 md:grid-cols-2 ${lastRow ? '' : 'border-b border-divider'}`}
                    >
                      <div className="flex flex-col gap-3 min-w-0">
                        <h3 className="label-caps">
                          Spending in {fmtMonth(monthDate(m.month))} · <span data-privacy="blur">{fmtEUR(breakdown.spend.total)}</span>
                        </h3>
                        {breakdown.spend.items.length > 0
                          ? <RankedBars items={breakdown.spend.items} />
                          : <p className="text-[13px] text-ink-body">No spending recorded in this month.</p>}
                      </div>
                      <div className="flex flex-col gap-3 min-w-0">
                        <h3 className="label-caps">
                          Income in {fmtMonth(monthDate(m.month))} · <span data-privacy="blur">{fmtEUR(breakdown.income.total)}</span>
                        </h3>
                        {breakdown.income.items.length > 0
                          ? <RankedBars items={breakdown.income.items} />
                          : <p className="text-[13px] text-ink-body">No income recorded in this month.</p>}
                      </div>
                      {breakdown.notCounted.items.length > 0 && (
                        <div className="flex flex-col gap-3 min-w-0 md:col-span-2">
                          <h3 className="label-caps">Not counted as income or spending</h3>
                          <RankedBars items={breakdown.notCounted.items} />
                          <p className="text-[13px] text-ink-muted max-w-2xl">
                            Money moved between your own accounts or into and out of investments, and loan principal.
                            {m.unclassified > 0.5 && (
                              <> Incoming payments that matched no category stay here until you{' '}
                                <Link to="/transactions" className="font-medium text-forest underline underline-offset-2">categorise them</Link>.
                              </>
                            )}
                          </p>
                        </div>
                      )}
                    </div>
                  )}
                </Fragment>
              );
            })}
          </div>
        </Section>
      </SectionBoundary>

      <SectionBoundary>
        <Section
          title="Calibrate Planning"
          subtitle="Use these figures as the starting inputs on the Planning page instead of typed estimates."
        >
          <div className="rounded-apple border border-divider px-4 py-4 flex flex-col md:flex-row md:items-center md:justify-between gap-4">
            <div className="flex flex-col gap-3">
              <ul className="text-[14px] text-ink-body space-y-1">
                <li>
                  Monthly invest budget: <span className="text-ink" data-privacy="blur">{fmtEUR(plan.investBudget)}</span>{' '}
                  {surplus < 0 ? '(typical net is negative, so nothing is left to invest)' : '(typical net)'}
                </li>
                <li>
                  FIRE expenses:{' '}
                  {plan.fireExpenses === null
                    ? 'cleared (no spending recorded)'
                    : <><span className="text-ink" data-privacy="blur">{fmtEUR(plan.fireExpenses)}</span> per year (typical spend × 12)</>}
                </li>
              </ul>
              <p className="text-[13px] text-ink-muted max-w-xl">
                Annual take-home pay: <span className="text-ink-body" data-privacy="blur">{fmtEUR(med.annual_take_home)}</span>{' '}
                (salary per month with income × 12). It is after tax and social contributions, so it does not
                replace the gross income you enter on Planning.
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-3 shrink-0">
              <span role="status" className="text-[13px] text-ink-muted">
                {appliedAt ? `Applied ${fmtDateTime(appliedAt)}` : canApply ? '' : 'Needs a complete month with income.'}
              </span>
              <button type="button" onClick={applyToPlanning} disabled={!canApply} className="apple-btn-secondary min-h-[44px]">
                Apply to Planning
              </button>
            </div>
          </div>
        </Section>
      </SectionBoundary>
    </div>
  );
}
