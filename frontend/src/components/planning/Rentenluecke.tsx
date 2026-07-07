import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';

type PensionResult = {
  sources: { type: string; label: string; monthly_gross: number; monthly_net: number; start_age: number }[];
  total_monthly_net: number; portfolio_net: number; projected_portfolio: number;
  monthly_contrib: number; gap: number; gap_annual: number; sparplan_to_close: number;
  sensitivity: { age: number; pension_net: number; portfolio_net: number; total_net: number; gap: number }[];
  rentenwert: number;
};

const PENSION_TEMPLATES = [
  { type: 'gesetzliche', label: 'Gesetzliche Rente', rp: 30 },
  { type: 'bav', label: 'bAV', rp: 0 },
  { type: 'riester', label: 'Riester', rp: 0 },
  { type: 'ruerup', label: 'Rürup', rp: 0 },
  { type: 'private', label: 'Private RV', rp: 0 },
  { type: 'other', label: 'Sonstige', rp: 0 },
];

// "Pension gap" — Germany's standard retirement-planning lens. Compares
// projected income from gesetzliche Rente / bAV / Riester / Rürup against
// a monthly spending target and surfaces the gap that has to come from
// the private portfolio. Backend at POST /api/portfolio/pension-gap does
// the actuarial math (Rentenwert, Besteuerungsanteil, …).
export default function Rentenluecke() {
  const { pensionCurrentAge, setPensionCurrentAge, pensionAge, setPensionAge, pensionNeed, setPensionNeed, pensionContrib, setPensionContrib, pensionSources, setPensionSources, fmt } = usePlanning();
  const tc = useThemeColors();
  const [pensionResult, setPensionResult] = useState<PensionResult | null>(null);
  const [pensionLoading, setPensionLoading] = useState(false);

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Rentenlücke</h2>
      <p className="text-[13px] text-ink-muted mb-3">Compare your retirement income sources against your target spending.</p>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-2 mb-3">
        <div>
          <label className="text-[11px] text-ink-muted">Current age</label>
          <input aria-label="Current age" type="number" value={pensionCurrentAge} onChange={e => { const v = parseInt(e.target.value) || 35; setPensionCurrentAge(v); localStorage.setItem('pension_current_age', String(v)); }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
        <div>
          <label className="text-[11px] text-ink-muted">Retire at</label>
          <input aria-label="Retirement age" type="number" value={pensionAge} onChange={e => { const v = parseInt(e.target.value) || 67; setPensionAge(v); localStorage.setItem('pension_age', String(v)); }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
        <div>
          <label className="text-[11px] text-ink-muted">Need/mo</label>
          <input aria-label="Monthly need" type="number" value={pensionNeed} onChange={e => { const v = parseInt(e.target.value) || 3000; setPensionNeed(v); localStorage.setItem('pension_need', String(v)); }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
        <div>
          <label className="text-[11px] text-ink-muted">Invest/mo</label>
          <input aria-label="Monthly investment" type="number" placeholder="auto" value={pensionContrib} onChange={e => { setPensionContrib(e.target.value); localStorage.setItem('pension_contrib', e.target.value); }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5 mb-3">
        {PENSION_TEMPLATES.map(t => (
          <button key={t.type} onClick={() => {
            const updated = [...pensionSources, { type: t.type, label: t.label, monthly_gross: t.type === 'gesetzliche' ? 0 : 500, rentenpunkte: t.rp, start_age: pensionAge, tax_portion_pct: 0 }];
            setPensionSources(updated);
            localStorage.setItem('pension_sources', JSON.stringify(updated));
          }} className="rounded-[6px] bg-parchment-deep px-2.5 py-1 text-[12px] font-medium text-ink-body hover:bg-divider">
            + {t.label}
          </button>
        ))}
      </div>

      {pensionSources.length > 0 && (
        <div className="space-y-2 mb-3">
          {pensionSources.map((src, i) => (
            <div key={i} className="rounded-xl bg-parchment-deep px-3 py-2">
              <div className="flex items-center justify-between mb-1.5">
                <span className="text-[15px] font-medium text-ink">{src.label}</span>
                <button onClick={() => {
                  const updated = pensionSources.filter((_, j) => j !== i);
                  setPensionSources(updated);
                  localStorage.setItem('pension_sources', JSON.stringify(updated));
                }} className="text-claret text-[12px]">Remove</button>
              </div>
              <div className="grid grid-cols-2 gap-2">
                {src.type === 'gesetzliche' ? (
                  <div>
                    <label className="text-[11px] text-ink-muted">Rentenpunkte</label>
                    <input aria-label="Rentenpunkte" type="number" step="0.1" value={src.rentenpunkte} onChange={e => {
                      const updated = [...pensionSources]; updated[i] = { ...src, rentenpunkte: parseFloat(e.target.value) || 0 };
                      setPensionSources(updated); localStorage.setItem('pension_sources', JSON.stringify(updated));
                    }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                  </div>
                ) : (
                  <div>
                    <label className="text-[11px] text-ink-muted">Monthly gross</label>
                    <input aria-label="Monthly gross pension" type="number" value={src.monthly_gross} onChange={e => {
                      const updated = [...pensionSources]; updated[i] = { ...src, monthly_gross: parseFloat(e.target.value) || 0 };
                      setPensionSources(updated); localStorage.setItem('pension_sources', JSON.stringify(updated));
                    }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                  </div>
                )}
                <div>
                  <label className="text-[11px] text-ink-muted">Start age</label>
                  <input aria-label="Start age" type="number" value={src.start_age} onChange={e => {
                    const updated = [...pensionSources]; updated[i] = { ...src, start_age: parseInt(e.target.value) || 67 };
                    setPensionSources(updated); localStorage.setItem('pension_sources', JSON.stringify(updated));
                  }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                </div>
              </div>
            </div>
          ))}

          <button onClick={async () => {
            setPensionLoading(true);
            try {
              const res = await fetch('/api/portfolio/pension-gap', {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ sources: pensionSources, retirement_age: pensionAge, monthly_need: pensionNeed, current_age: pensionCurrentAge, monthly_contrib: parseFloat(pensionContrib) || 0 }),
              });
              setPensionResult(await res.json());
            } catch { /* ignore */ } finally { setPensionLoading(false); }
          }} className="w-full apple-btn-primary" disabled={pensionLoading}>
            {pensionLoading ? 'Calculating...' : 'Calculate Rentenlücke'}
          </button>
        </div>
      )}

      {pensionResult && (
        <div className="space-y-3">
          <div className="rounded-xl bg-parchment-deep p-3">
            <div className="flex items-baseline justify-between mb-2">
              <span className="text-[15px] font-medium text-ink">Monthly Income vs Need</span>
              <span className="text-[12px] text-ink-muted">Target: {fmt(pensionResult.gap + pensionResult.total_monthly_net + pensionResult.portfolio_net)}/mo</span>
            </div>
            <div className="w-full h-6 rounded-full bg-divider overflow-hidden flex">
              {pensionResult.sources.map((s, i) => {
                const total = pensionNeed;
                const pct = Math.min((s.monthly_net / total) * 100, 100);
                const colors = ['bg-forest', 'bg-slate', 'bg-walnut', 'bg-amber', 'bg-sage', 'bg-claret'];
                return <div key={i} className={`${colors[i % colors.length]} h-full`} style={{ width: `${pct}%` }} title={`${s.label}: ${fmt(s.monthly_net)}`} />;
              })}
              {pensionResult.portfolio_net > 0 && (
                <div className="bg-sage h-full" style={{ width: `${Math.min((pensionResult.portfolio_net / pensionNeed) * 100, 100)}%` }} title={`Portfolio: ${fmt(pensionResult.portfolio_net)}`} />
              )}
            </div>
            <div className="flex flex-wrap gap-x-3 gap-y-1 mt-2 text-[11px]">
              {pensionResult.sources.map((s, i) => {
                const colors = ['text-forest', 'text-slate', 'text-walnut', 'text-amber', 'text-sage', 'text-claret'];
                return <span key={i} className={colors[i % colors.length]}>{s.label}: {fmt(s.monthly_net)}</span>;
              })}
              <span className="text-sage">Portfolio: {fmt(pensionResult.portfolio_net)}</span>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-2">
            <div className="rounded-lg bg-parchment-deep p-2.5">
              <p className="text-[11px] text-ink-muted">Monthly Gap</p>
              <p className={`text-[13px] font-semibold tabular-nums ${pensionResult.gap <= 0 ? 'text-sage' : 'text-claret'}`}>
                {pensionResult.gap <= 0 ? 'Covered!' : `${fmt(pensionResult.gap)}/mo`}
              </p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-2.5">
              <p className="text-[11px] text-ink-muted">Extra Sparplan</p>
              <p className="text-[13px] font-semibold tabular-nums text-ink">
                {pensionResult.sparplan_to_close <= 0 ? '—' : `${fmt(pensionResult.sparplan_to_close)}/mo`}
              </p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-2.5">
              <p className="text-[11px] text-ink-muted">Projected Portfolio</p>
              <p className="text-[13px] font-semibold tabular-nums text-ink">{fmt(pensionResult.projected_portfolio)}</p>
              {pensionResult.monthly_contrib > 0 && (
                <p className="text-[11px] text-ink-muted">@ {fmt(pensionResult.monthly_contrib)}/mo</p>
              )}
            </div>
            <div className="rounded-lg bg-parchment-deep p-2.5">
              <p className="text-[11px] text-ink-muted">Portfolio Withdrawal</p>
              <p className="text-[13px] font-semibold tabular-nums text-ink">{fmt(pensionResult.portfolio_net)}/mo net</p>
            </div>
          </div>

          {pensionResult.sensitivity && pensionResult.sensitivity.length > 0 && (
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] font-medium text-ink mb-2">Retirement Age Sensitivity</p>
              <EChartWrapper option={{
                tooltip: { trigger: 'axis' as const },
                legend: { data: ['Pension', 'Portfolio', 'Gap'], bottom: 0, textStyle: { fontSize: 10 } },
                grid: { top: 10, right: 10, bottom: 30, left: 40 },
                xAxis: { type: 'category' as const, data: pensionResult.sensitivity.map(s => String(s.age)), axisLabel: { fontSize: 10 } },
                yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, formatter: (v: number) => `${Math.round(v/1000)}K` } },
                series: [
                  { name: 'Pension', type: 'bar' as const, stack: 'income', data: pensionResult.sensitivity.map(s => s.pension_net), itemStyle: { color: tc.forest } },
                  { name: 'Portfolio', type: 'bar' as const, stack: 'income', data: pensionResult.sensitivity.map(s => s.portfolio_net), itemStyle: { color: tc.sage } },
                  { name: 'Gap', type: 'line' as const, data: pensionResult.sensitivity.map(s => Math.max(s.gap, 0)), itemStyle: { color: tc.claret }, lineStyle: { type: 'dashed' as const } },
                ],
              }} height="200px" />
            </div>
          )}
        </div>
      )}

      {pensionSources.length === 0 && !pensionResult && (
        <p className="text-[13px] text-ink-muted">
          Add pension sources to calculate your Rentenlücke. Your Renteninformation shows your Rentenpunkte.
        </p>
      )}
    </div>
  );
}
