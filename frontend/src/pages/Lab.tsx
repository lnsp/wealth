import { useState, useEffect } from 'react';
import { api, type TaxLot } from '../api/client';
import { TabBar } from '../components/ui';

type LabTab = 'sell' | 'dca' | 'projection';

const fmt = (n: number) =>
  new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

export default function Lab() {
  const [activeTab, setActiveTab] = useState<LabTab>('sell');

  // Sell simulator state
  const [taxLots, setTaxLots] = useState<TaxLot[]>([]);
  const [sellAmounts, setSellAmounts] = useState<Record<string, string>>({});
  const [sellResults, setSellResults] = useState<{ results: { isin: string; name: string; sell_amount: number; cost_basis: number; realized_gain: number; teilfreistellung: number; taxable_gain: number; estimated_tax: number; net_proceeds: number; lots_consumed: number; is_equity_fund: boolean }[]; total_tax: number; total_proceeds: number } | null>(null);

  // DCA state
  const [savingsPlans, setSavingsPlans] = useState<{ plans: { isin: string; name: string; monthly_amount: number; executions: number; total_invested: number; total_shares: number; avg_cost_basis: number; current_value: number; dca_return_pct: number; lump_sum_value: number; lump_sum_return_pct: number; dca_advantage_eur: number }[]; total_monthly: number } | null>(null);

  // Projection state
  const [contrib, setContrib] = useState('2000');
  const [returnPct, setReturnPct] = useState('7');
  const [projResult, setProjResult] = useState<{ projection_end?: number; milestones?: { year: number; years_from_now: number; projected_value: number; growth: number }[] } | null>(null);

  useEffect(() => {
    api.getTaxLots().then(r => setTaxLots(r.lots || [])).catch(() => {});
    api.getSavingsPlans().then(setSavingsPlans).catch(() => {});
  }, []);

  // Sell simulator helpers
  const isins = [...new Set(taxLots.map(l => l.isin))];
  const nameMap: Record<string, string> = {};
  const valueMap: Record<string, number> = {};
  const plMap: Record<string, number> = {};
  taxLots.forEach(l => {
    nameMap[l.isin] = l.name;
    valueMap[l.isin] = (valueMap[l.isin] || 0) + l.current_value;
    plMap[l.isin] = (plMap[l.isin] || 0) + l.unrealized_pl;
  });
  const losingISINs = isins.filter(isin => plMap[isin] < 0);

  const runSellSim = () => {
    const requests = isins
      .filter(isin => parseFloat(sellAmounts[isin] || '0') > 0)
      .map(isin => ({ isin, amount_eur: parseFloat(sellAmounts[isin]) }));
    if (requests.length === 0) return;
    api.simulateSell(requests).then(setSellResults).catch(console.error);
  };

  const harvestLosses = () => {
    const newAmounts: Record<string, string> = {};
    losingISINs.forEach(isin => { newAmounts[isin] = String(Math.round(valueMap[isin])); });
    setSellAmounts(newAmounts);
    const requests = losingISINs.map(isin => ({ isin, amount_eur: Math.round(valueMap[isin]) }));
    if (requests.length > 0) api.simulateSell(requests).then(setSellResults).catch(console.error);
  };

  const runProjection = () => {
    const c = parseFloat(contrib) || 0;
    const r = parseFloat(returnPct) || 7;
    api.getProjection(c, r).then(setProjResult).catch(console.error);
  };

  useEffect(() => { runProjection(); }, []); // initial load

  return (
    <div className="space-y-6">
      <TabBar tabs={[{ id: 'sell' as LabTab, label: 'Sell Simulator' }, { id: 'dca' as LabTab, label: 'DCA Analysis' }, { id: 'projection' as LabTab, label: 'Projection' }]} activeTab={activeTab} onTabChange={setActiveTab} />

      {/* --- SELL SIMULATOR --- */}
      {activeTab === 'sell' && (
        <div className="space-y-4">
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <div className="flex items-center justify-between mb-3">
              <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">What-If Sell Simulator</h2>
              {losingISINs.length > 0 && (
                <button onClick={harvestLosses}
                  className="rounded-[8px] bg-parchment-deep border border-amber text-amber px-3 py-1.5 text-[12px] font-medium hover:bg-parchment-deep transition-colors">
                  Harvest Losses ({fmt(losingISINs.reduce((s, i) => s + Math.abs(plMap[i]), 0))})
                </button>
              )}
            </div>
            <p className="text-[13px] text-ink-muted mb-4">
              Enter EUR amounts per security to preview FIFO-based tax impact before selling.
            </p>

            <div className="space-y-2 mb-4">
              {isins.sort((a, b) => (valueMap[b] || 0) - (valueMap[a] || 0)).map(isin => (
                <div key={isin} className="flex items-center gap-2">
                  <span className="text-[13px] text-ink truncate flex-1 min-w-0">{nameMap[isin]}</span>
                  <span className={`text-[11px] shrink-0 tabular-nums ${plMap[isin] >= 0 ? 'text-sage' : 'text-claret'}`}>
                    {plMap[isin] >= 0 ? '+' : ''}{fmt(plMap[isin])}
                  </span>
                  <input type="number" placeholder="0" value={sellAmounts[isin] || ''}
                    onChange={e => setSellAmounts(prev => ({ ...prev, [isin]: e.target.value }))}
                    className="w-24 rounded-[8px] border border-divider bg-parchment text-ink px-2 py-1.5 text-[13px] tabular-nums text-right shrink-0" />
                  <span className="text-[11px] text-ink-muted shrink-0">€</span>
                </div>
              ))}
            </div>

            <button onClick={runSellSim}
              className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-4 py-2 text-[13px] font-medium w-full">
              Preview Tax Impact
            </button>
          </div>

          {sellResults && sellResults.results && sellResults.results.length > 0 && (
            <div className="border-t border-divider pt-6 py-3 md:py-5">
              <div className="grid grid-cols-3 gap-3 mb-4">
                <div className="rounded-xl bg-parchment-deep p-3 text-center">
                  <p className="text-[12px] text-ink-muted mb-1">Proceeds</p>
                  <p className="text-[15px] font-semibold tabular-nums text-sage">{fmt(sellResults.total_proceeds)}</p>
                </div>
                <div className="rounded-xl bg-parchment-deep p-3 text-center">
                  <p className="text-[12px] text-ink-muted mb-1">Tax</p>
                  <p className="text-[15px] font-semibold tabular-nums text-claret">{fmt(sellResults.total_tax)}</p>
                </div>
                <div className="rounded-xl bg-parchment-deep p-3 text-center">
                  <p className="text-[12px] text-ink-muted mb-1">Effective</p>
                  <p className="text-[15px] font-semibold tabular-nums">
                    {sellResults.total_proceeds + sellResults.total_tax > 0
                      ? ((sellResults.total_tax / (sellResults.total_proceeds + sellResults.total_tax)) * 100).toFixed(1) : '0'}%
                  </p>
                </div>
              </div>
              <div className="space-y-2">
                {sellResults.results.map(r => (
                  <div key={r.isin} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-2">
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium text-ink truncate">{r.name}</p>
                      <p className="text-[11px] text-ink-muted">
                        Sell {fmt(r.sell_amount)} · Gain {fmt(r.realized_gain)} · {r.lots_consumed} lots
                      </p>
                    </div>
                    <div className="text-right shrink-0 ml-2">
                      <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(r.estimated_tax)}</p>
                      <p className="text-[11px] text-ink-muted">tax</p>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* --- DCA ANALYSIS --- */}
      {activeTab === 'dca' && savingsPlans && (
        <div className="space-y-4">
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">DCA vs Lump-Sum</h2>
            <p className="text-[13px] text-ink-muted mb-4">
              How your savings plans performed compared to investing everything on day one.
              {savingsPlans.total_monthly > 0 && ` Total: ${fmt(savingsPlans.total_monthly)}/month across ${savingsPlans.plans.length} plans.`}
            </p>

            <div className="space-y-3">
              {savingsPlans.plans.map(p => {
                const dcaWins = p.dca_advantage_eur >= 0;
                return (
                  <div key={p.isin} className="rounded-xl bg-parchment-deep p-3">
                    <div className="flex items-center justify-between mb-2">
                      <p className="text-[15px] font-medium text-ink truncate flex-1">{p.name}</p>
                      <span className={`text-[13px] font-semibold tabular-nums ${p.dca_return_pct >= 0 ? 'text-sage' : 'text-claret'}`}>
                        {p.dca_return_pct >= 0 ? '+' : ''}{p.dca_return_pct}%
                      </span>
                    </div>
                    <div className="grid grid-cols-3 gap-2 text-center">
                      <div>
                        <p className="text-[11px] text-ink-muted">Invested</p>
                        <p className="text-[12px] font-medium tabular-nums">{fmt(p.total_invested)}</p>
                      </div>
                      <div>
                        <p className="text-[11px] text-ink-muted">DCA Value</p>
                        <p className="text-[12px] font-medium tabular-nums">{fmt(p.current_value)}</p>
                      </div>
                      <div>
                        <p className="text-[11px] text-ink-muted">Lump-Sum</p>
                        <p className="text-[12px] font-medium tabular-nums">{fmt(p.lump_sum_value)}</p>
                      </div>
                    </div>
                    <p className={`text-[12px] font-medium mt-2 text-center ${dcaWins ? 'text-sage' : 'text-claret'}`}>
                      DCA {dcaWins ? 'wins' : 'loses'} by {fmt(Math.abs(p.dca_advantage_eur))}
                    </p>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      )}

      {/* --- PROJECTION SANDBOX --- */}
      {activeTab === 'projection' && (
        <div className="space-y-4">
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Projection Sandbox</h2>
            <p className="text-[13px] text-ink-muted mb-4">
              Explore different contribution and return scenarios.
            </p>

            <div className="flex flex-col sm:flex-row gap-3 mb-4">
              <div className="flex items-center gap-2 flex-1">
                <label className="text-[12px] text-ink-muted shrink-0">Monthly</label>
                <input type="number" value={contrib} onChange={e => setContrib(e.target.value)}
                  className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-1.5 text-[16px] tabular-nums" />
                <span className="text-[12px] text-ink-muted">€</span>
              </div>
              <div className="flex items-center gap-2 flex-1">
                <label className="text-[12px] text-ink-muted shrink-0">Return</label>
                <input type="number" value={returnPct} onChange={e => setReturnPct(e.target.value)} step="0.5"
                  className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-1.5 text-[16px] tabular-nums" />
                <span className="text-[12px] text-ink-muted">%</span>
              </div>
              <button onClick={runProjection}
                className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-4 py-2 text-[16px] font-medium whitespace-nowrap">
                Calculate
              </button>
            </div>

            {projResult?.milestones && projResult.milestones.length > 0 && (
              <div className="space-y-2">
                {projResult.milestones.map(m => (
                  <div key={m.year} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-2">
                    <div>
                      <span className="text-[15px] font-medium text-ink">{m.year}</span>
                      <span className="text-[12px] text-ink-muted ml-1">+{m.years_from_now}y</span>
                    </div>
                    <div className="text-right">
                      <p className="text-[15px] font-semibold tabular-nums">{fmt(m.projected_value)}</p>
                      <p className="text-[11px] text-sage tabular-nums">+{fmt(m.growth)} growth</p>
                    </div>
                  </div>
                ))}
                {projResult.projection_end && (
                  <p className="text-[13px] text-ink-muted text-center mt-2">
                    30-year target: <span className="font-semibold text-ink">{fmt(projResult.projection_end)}</span>
                  </p>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
