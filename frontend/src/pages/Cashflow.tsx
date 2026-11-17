import { Fragment, useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, type CashflowData, type CashflowMonth } from '../api/client';
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

const categoryLabel = (c: string) => (c === 'internal' ? 'Internal transfer' : labelFor(c));

const hasActivity = (m: CashflowMonth) => m.income !== 0 || m.fixed !== 0 || m.variable !== 0;

/** Whole-euro figure without the symbol (the table caption names the unit). */
const plain = (n: number) => fmtNum(Math.round(n));
const signed = (n: number) => {
  const r = Math.round(n);
  return `${r > 0 ? '+' : ''}${fmtNum(r)}`;
};
const netTone = (n: number) => (Math.round(n) > 0 ? 'text-sage' : Math.round(n) < 0 ? 'text-claret' : 'text-ink');

// Month, Income, Spend, Net fit a phone; desktop adds a Trend column.
const GRID = 'grid grid-cols-[86px_1fr_1fr_1fr] md:grid-cols-[130px_1fr_1fr_1fr_140px] items-center';

type RowGroup =
  | { kind: 'month'; month: CashflowMonth }
  | { kind: 'gap'; newest: string; oldest: string };

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
    const toItems = (list: typeof buckets): { items: RankedBarItem[]; total: number } => {
      const merged = new Map<string, number>();
      for (const b of list) {
        const label = categoryLabel(b.category);
        merged.set(label, (merged.get(label) ?? 0) + b.amount);
      }
      const positive = [...merged.entries()].filter(([, v]) => Math.round(v) > 0).sort((a, b) => b[1] - a[1]);
      const total = positive.reduce((s, [, v]) => s + v, 0);
      return {
        total,
        items: positive.map(([label, value]) => ({
          label,
          value,
          display: fmtEUR(value),
          share: fmtPct(total > 0 ? (value / total) * 100 : 0),
        })),
      };
    };
    return {
      spend: toItems(buckets.filter(b => b.bucket === 'fixed' || b.bucket === 'variable')),
      income: toItems(buckets.filter(b => b.bucket === 'income')),
    };
  }, [data, selectedMonth]);

  const applyToPlanning = () => {
    if (!data) return;
    const m = data.medians;
    if (m.monthly_surplus > 0) localStorage.setItem('monthly_invest_budget', String(Math.round(m.monthly_surplus)));
    if (m.monthly_spend > 0) localStorage.setItem('fire_expenses', String(Math.round(m.monthly_spend * 12)));
    if (m.annual_gross_income > 0) localStorage.setItem('gross_annual_income', String(Math.round(m.annual_gross_income)));
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
  // The server takes its medians over the months that have income.
  const medianMonths = months.filter(m => m.income > 0).length;
  const savingsRate = med.monthly_income > 0 ? (med.monthly_surplus / med.monthly_income) * 100 : null;
  const surplus = Math.round(med.monthly_surplus);

  // Trend bar (desktop only): each month's net within the range of the window.
  const nets = months.filter(hasActivity).map(m => m.net);
  const maxNet = Math.max(0, ...nets);
  const minNet = Math.min(0, ...nets);

  return (
    <div>
      <PageHeader title="Cashflow" />

      <MetricGrid className="mt-2 md:mt-6">
        <MetricCard label="Typical income" value={fmtEUR(med.monthly_income)} />
        <MetricCard label="Typical spend" value={fmtEUR(med.monthly_spend)} />
        <MetricCard
          label="Typical net"
          value={fmtEUR(surplus, { sign: true })}
          tone={surplus > 0 ? 'positive' : surplus < 0 ? 'negative' : 'neutral'}
        />
        <MetricCard
          label="Savings rate"
          value={savingsRate === null ? '—' : fmtPct(savingsRate)}
          tone={savingsRate !== null && savingsRate < 0 ? 'negative' : 'neutral'}
          money={false}
        />
      </MetricGrid>
      <p className="mt-3 text-[13px] text-ink-muted max-w-2xl">
        Monthly medians over the {medianMonths} {medianMonths === 1 ? 'month' : 'months'} with imported income in the
        past {months.length}. Typical net is typical income minus typical spend, and the savings rate is typical net
        as a share of typical income.
      </p>

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
              const spend = m.fixed + m.variable;
              const panelId = `cashflow-${m.month}`;
              const ratio = maxNet - minNet > 0 ? (m.net - minNet) / (maxNet - minNet) : 0.5;
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
                        <span className="sr-only">, income</span>
                      </span>
                    </span>
                    <span data-privacy="blur">{plain(m.income)}<span className="sr-only"> €, spend</span></span>
                    <span data-privacy="blur">{plain(spend)}<span className="sr-only"> €, net</span></span>
                    <span data-privacy="blur" className={netTone(m.net)}>{signed(m.net)}<span className="sr-only"> €</span></span>
                    <span className="hidden md:block pl-6" aria-hidden="true">
                      <span className="block h-2 rounded-[2px] bg-divider overflow-hidden">
                        <span className="block h-full bg-forest" style={{ width: `${Math.max(2, Math.min(100, ratio * 100))}%` }} />
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
            <ul className="text-[14px] text-ink-body space-y-1">
              <li>Monthly invest budget: <span className="text-ink" data-privacy="blur">{fmtEUR(Math.max(0, med.monthly_surplus))}</span> (typical net)</li>
              <li>FIRE expenses: <span className="text-ink" data-privacy="blur">{fmtEUR(med.monthly_spend * 12)}</span> per year (typical spend × 12)</li>
              <li>Annual income: <span className="text-ink" data-privacy="blur">{fmtEUR(med.annual_gross_income)}</span> (income received in the past {months.length} months)</li>
            </ul>
            <div className="flex flex-wrap items-center gap-3 shrink-0">
              <span role="status" className="text-[13px] text-ink-muted">
                {appliedAt ? `Applied ${fmtDateTime(appliedAt)}` : ''}
              </span>
              <button type="button" onClick={applyToPlanning} className="apple-btn-secondary min-h-[44px]">
                Apply to Planning
              </button>
            </div>
          </div>
        </Section>
      </SectionBoundary>
    </div>
  );
}
