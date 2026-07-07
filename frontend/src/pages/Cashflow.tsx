import { useEffect, useMemo, useState } from 'react';
import { api, type CashflowData } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';
import { useThemeColors } from '../hooks/useThemeColors';

const CATEGORY_LABEL: Record<string, string> = {
  salary: 'Salary',
  interest: 'Interest',
  refund: 'Refund',
  housing: 'Housing',
  utilities: 'Utilities',
  insurance: 'Insurance',
  subscriptions: 'Subscriptions',
  groceries: 'Groceries',
  dining: 'Dining',
  transport: 'Transport',
  health: 'Health',
  entertainment: 'Entertainment',
  shopping: 'Shopping',
  tax: 'Tax',
  investment: 'Investment',
  internal: 'Internal Transfer',
  other: 'Other',
};

const MONTH_LABEL = (ym: string) => {
  const [y, m] = ym.split('-');
  return new Date(Number(y), Number(m) - 1, 1).toLocaleDateString('en-GB', { month: 'short', year: '2-digit' });
};

const fmt = (n: number) => new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(n);

export default function Cashflow() {
  const [data, setData] = useState<CashflowData | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [selectedMonth, setSelectedMonth] = useState<string>('');
  const [appliedAt, setAppliedAt] = useState<number | null>(null);
  const tc = useThemeColors();

  useEffect(() => {
    let cancelled = false;
    api.getCashflow(12)
      .then(d => {
        if (cancelled) return;
        setData(d);
        // Default to most recent month *with activity* — empty months would
        // render an empty donut on first paint.
        const first = d.months.find(m => m.income + m.fixed + m.variable > 0) || d.months[0];
        if (first) setSelectedMonth(first.month);
      })
      .catch(e => { if (!cancelled) setErr(e instanceof Error ? e.message : 'Failed to load cashflow'); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, []);

  const donutData = useMemo(() => {
    if (!data || !selectedMonth) return [];
    return data.buckets
      .filter(b => b.month === selectedMonth && (b.bucket === 'fixed' || b.bucket === 'variable'))
      .map(b => ({ name: CATEGORY_LABEL[b.category] || b.category, value: Math.round(b.amount), bucket: b.bucket }))
      .sort((a, b) => b.value - a.value);
  }, [data, selectedMonth]);

  const incomeBreakdown = useMemo(() => {
    if (!data || !selectedMonth) return [];
    return data.buckets
      .filter(b => b.month === selectedMonth && b.bucket === 'income')
      .map(b => ({ name: CATEGORY_LABEL[b.category] || b.category, value: Math.round(b.amount) }))
      .sort((a, b) => b.value - a.value);
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
      <div className="px-1 md:px-0 py-6 md:py-8">
        <h1 className="font-serif text-title text-ink mb-3">Cashflow</h1>
        <div className="text-[13px] text-ink-muted">Loading…</div>
      </div>
    );
  }

  if (err) {
    return (
      <div className="px-1 md:px-0 py-6 md:py-8">
        <h1 className="font-serif text-title text-ink mb-3">Cashflow</h1>
        <div className="rounded-[8px] border border-claret bg-parchment-deep p-4 text-[13px] text-claret">
          Failed to load: {err}
        </div>
      </div>
    );
  }

  if (!data || data.months.every(m => m.income === 0 && m.fixed === 0 && m.variable === 0)) {
    return (
      <div className="px-1 md:px-0 py-6 md:py-8">
        <h1 className="font-serif text-title text-ink mb-3">Cashflow</h1>
        <div className="rounded-[8px] border border-divider bg-parchment-deep p-6 text-[13px] text-ink-muted">
          <p className="mb-2 text-ink">No cashflow data yet.</p>
          <p>Import checking/savings transactions on the Transactions page. The classifier needs ~3 months of activity for a useful 12-month roll-up.</p>
        </div>
      </div>
    );
  }

  // The 12-month series is newest-first; reverse for the trend sparkline so
  // time flows left-to-right (oldest on the left).
  const chronological = [...data.months].reverse();
  const surplusSeries = chronological.map(m => Math.round(m.net));
  const maxSurplus = Math.max(0, ...surplusSeries);
  const minSurplus = Math.min(0, ...surplusSeries);

  return (
    <div className="px-1 md:px-0 py-6 md:py-8 space-y-6">
      <h1 className="font-serif text-title text-ink">Cashflow</h1>

      {/* KPI ribbon */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
        <div className="rounded-xl bg-parchment-deep p-3">
          <p className="text-[11px] text-ink-muted mb-1">Median monthly income</p>
          <p className="font-serif text-[18px] font-semibold tabular-nums text-sage">{fmt(data.medians.monthly_income)}</p>
        </div>
        <div className="rounded-xl bg-parchment-deep p-3">
          <p className="text-[11px] text-ink-muted mb-1">Median monthly spend</p>
          <p className="font-serif text-[18px] font-semibold tabular-nums text-claret">{fmt(data.medians.monthly_spend)}</p>
        </div>
        <div className="rounded-xl bg-parchment-deep p-3">
          <p className="text-[11px] text-ink-muted mb-1">Median surplus</p>
          <p className={`font-serif text-[18px] font-semibold tabular-nums ${data.medians.monthly_surplus >= 0 ? 'text-forest' : 'text-claret'}`}>
            {fmt(data.medians.monthly_surplus)}
          </p>
        </div>
        <div className="rounded-xl bg-parchment-deep p-3">
          <p className="text-[11px] text-ink-muted mb-1">Annual gross income</p>
          <p className="font-serif text-[18px] font-semibold tabular-nums text-ink">{fmt(data.medians.annual_gross_income)}</p>
        </div>
      </div>

      {/* Apply-to-Planning CTA — single biggest reason for this whole feature
          to exist: calibrate Planning inputs from real data instead of typed
          guesses. */}
      <div className="rounded-xl bg-inset border-l-[3px] border-amber p-4 flex flex-col md:flex-row md:items-center md:justify-between gap-3">
        <div className="text-[13px] text-ink">
          <p className="font-medium text-ink mb-0.5">Calibrate Planning inputs</p>
          <p className="text-ink-muted">
            Apply these medians to <span className="text-ink">monthly invest budget</span>,{' '}
            <span className="text-ink">FIRE expenses</span> (× 12), and{' '}
            <span className="text-ink">gross annual income</span> on the Planning page.
          </p>
        </div>
        <div className="flex items-center gap-3 shrink-0">
          {appliedAt && (
            <span className="text-[11px] text-sage">Applied {new Date(appliedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
          )}
          <button
            onClick={applyToPlanning}
            className="rounded-[6px] bg-forest text-parchment px-4 py-2 text-[13px] font-medium hover:bg-forest-light transition-colors"
          >
            Apply to Planning
          </button>
        </div>
      </div>

      {/* Monthly table */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Monthly Cashflow</h2>
        <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
          Trailing 12 months. Click a row to break down its categories.
        </p>
        <div className="overflow-x-auto px-1 md:px-0">
          <table className="w-full text-[12px] min-w-[560px]">
            <thead>
              <tr className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] border-b border-divider">
                <th className="text-left py-2 font-medium">Month</th>
                <th className="text-right py-2 font-medium">Income</th>
                <th className="text-right py-2 font-medium">Fixed</th>
                <th className="text-right py-2 font-medium">Variable</th>
                <th className="text-right py-2 font-medium">Net</th>
                <th className="text-right py-2 font-medium pl-3">Trend</th>
              </tr>
            </thead>
            <tbody>
              {data.months.map((m, i) => {
                const isSelected = m.month === selectedMonth;
                const ratio = (maxSurplus - minSurplus) > 0
                  ? (m.net - minSurplus) / (maxSurplus - minSurplus)
                  : 0.5;
                return (
                  <tr
                    key={m.month}
                    onClick={() => setSelectedMonth(m.month)}
                    className={`border-b border-divider cursor-pointer transition-colors ${isSelected ? 'bg-parchment-deep' : 'hover:bg-parchment-deep'}`}
                  >
                    <td className="py-1.5 text-ink">{MONTH_LABEL(m.month)}{isSelected && <span className="text-forest text-[10px] ml-1">●</span>}</td>
                    <td className="py-1.5 text-right tabular-nums text-sage">{m.income > 0 ? fmt(m.income) : '—'}</td>
                    <td className="py-1.5 text-right tabular-nums">{m.fixed > 0 ? fmt(m.fixed) : '—'}</td>
                    <td className="py-1.5 text-right tabular-nums">{m.variable > 0 ? fmt(m.variable) : '—'}</td>
                    <td className={`py-1.5 text-right tabular-nums font-medium ${m.net > 0 ? 'text-forest' : m.net < 0 ? 'text-claret' : 'text-ink-muted'}`}>
                      {m.income + m.fixed + m.variable > 0 ? fmt(m.net) : '—'}
                    </td>
                    <td className="py-1.5 pl-3">
                      <div className="h-1.5 w-16 rounded-full bg-parchment-deep relative ml-auto">
                        <div
                          className="absolute inset-y-0 left-0 rounded-full bg-forest"
                          style={{ width: `${Math.max(2, ratio * 100)}%`, opacity: m.income + m.fixed + m.variable > 0 ? 1 : 0.2 }}
                          aria-hidden
                        />
                      </div>
                      <span className="sr-only">{i + 1} months ago net: {m.net}</span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      {/* Donut for selected month */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <div className="flex items-baseline justify-between mb-3 md:mb-4 px-1 md:px-0">
          <h2 className="font-serif text-heading text-ink">Spend Breakdown</h2>
          <span className="text-[12px] text-ink-muted">{MONTH_LABEL(selectedMonth)}</span>
        </div>
        {donutData.length === 0 ? (
          <p className="text-[13px] text-ink-muted px-1 md:px-0">No spend in {MONTH_LABEL(selectedMonth)}.</p>
        ) : (
          <EChartWrapper
            option={{
              tooltip: {
                trigger: 'item' as const,
                formatter: (params: unknown) => {
                  const p = params as { name: string; value: number; percent: number };
                  return `${p.name}<br/>${fmt(p.value)} (${p.percent.toFixed(1)}%)`;
                },
              },
              legend: { type: 'scroll' as const, bottom: 0, textStyle: { color: tc.inkBody, fontSize: 11 } },
              series: [{
                type: 'pie' as const,
                radius: ['40%', '70%'],
                avoidLabelOverlap: true,
                minAngle: 2,
                data: donutData.map(d => ({
                  name: d.name,
                  value: d.value,
                  itemStyle: { color: d.bucket === 'fixed' ? tc.walnut : tc.amber },
                })),
                label: {
                  fontSize: 11, color: tc.inkBody, formatter: (p: unknown) => {
                    const e = p as { name: string; percent: number };
                    return e.percent >= 5 ? `${e.name} ${e.percent.toFixed(0)}%` : '';
                  },
                },
                labelLine: { length: 10, length2: 6 },
                itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2 },
              }],
              graphic: [{
                type: 'text', left: 'center', top: 'center',
                style: {
                  text: fmt(donutData.reduce((s, d) => s + d.value, 0)),
                  fontSize: 16, fontWeight: 600, fill: tc.ink,
                },
              }],
            }}
            height="280px"
          />
        )}
      </div>

      {incomeBreakdown.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-baseline justify-between mb-3 px-1 md:px-0">
            <h2 className="font-serif text-heading text-ink">Income Sources</h2>
            <span className="text-[12px] text-ink-muted">{MONTH_LABEL(selectedMonth)}</span>
          </div>
          <div className="space-y-1 px-1 md:px-0">
            {incomeBreakdown.map((src, i) => (
              <div key={i} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-2">
                <span className="text-[13px] text-ink">{src.name}</span>
                <span className="text-[13px] tabular-nums font-medium text-sage">{fmt(src.value)}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
