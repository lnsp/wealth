import { useState, useEffect, useCallback, useMemo } from 'react';
import { api, type Account, type NetWorthSnapshot, type ProjectionData } from '../api/client';
import { TabBar } from '../components/ui';
import { PlanningProvider, type PlanningState } from '../components/planning/PlanningContext';
import ContributionRouter from '../components/planning/ContributionRouter';
import EstateCalculator from '../components/planning/EstateCalculator';
import FinancialIndependencePlaybook from '../components/planning/FinancialIndependencePlaybook';
import FireCalculator from '../components/planning/FireCalculator';
import HumanCapital from '../components/planning/HumanCapital';
import InsuranceInventory from '../components/planning/InsuranceInventory';
import LiabilityAmortization from '../components/planning/LiabilityAmortization';
import LifeEventSimulator from '../components/planning/LifeEventSimulator';
import MoneyLeftOnTable from '../components/planning/MoneyLeftOnTable';
import OpportunityCosts from '../components/planning/OpportunityCosts';
import Rentenluecke from '../components/planning/Rentenluecke';
import WithdrawalSequence from '../components/planning/WithdrawalSequence';
import WithdrawalSimulation from '../components/planning/WithdrawalSimulation';
import WithdrawalSources from '../components/planning/WithdrawalSources';

type PlanningSubTab = 'fire' | 'growth' | 'protection';
const PLANNING_TABS: { id: PlanningSubTab; label: string }[] = [
  { id: 'fire', label: 'FIRE & Retirement' },
  { id: 'growth', label: 'Growth' },
  { id: 'protection', label: 'Protection' },
];

export default function Planning() {
  // Shared dashboard data — fetched independently of NetWorth so this page
  // stands on its own (no shared parent state, no defaultTab dance).
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [allSnapshots, setAllSnapshots] = useState<NetWorthSnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Projection — Planning's FIRE Calculator + Withdrawal sections read
  // projData.contribution / projData.return_pct as their default basis. We
  // own a local copy here so the Projection tab and Planning don't fight.
  const [projData, setProjData] = useState<ProjectionData | null>(null);
  const [projContrib, setProjContrib] = useState<string>('');
  const [projReturn, setProjReturn] = useState<string>('');

  // Sub-tab pill nav
  const [planningSubTab, setPlanningSubTab] = useState<PlanningSubTab>(() => (localStorage.getItem('planning_subtab') as PlanningSubTab) || 'fire');

  // FIRE inputs
  const [fireExpenses, setFireExpenses] = useState<string>(() => localStorage.getItem('fire_expenses') || '');
  const [fireSWR, setFireSWR] = useState<string>(() => localStorage.getItem('fire_swr') || '3.5');
  const [pensionIncome, setPensionIncome] = useState<string>(() => localStorage.getItem('pension_income') || '');
  const [otherIncome, setOtherIncome] = useState<string>(() => localStorage.getItem('other_income') || '');

  // Contribution Router
  const [crMarginalRate, setCrMarginalRate] = useState<string>(() => localStorage.getItem('marginal_tax_rate') || '42');
  const [crChildren, setCrChildren] = useState<string>(() => localStorage.getItem('num_children') || '0');
  const [crBudget, setCrBudget] = useState<string>(() => localStorage.getItem('monthly_invest_budget') || '');
  const [crGrossIncome, setCrGrossIncome] = useState<string>(() => localStorage.getItem('gross_annual_income') || '');

  // Rentenlücke (pension gap)
  const [pensionSources, setPensionSources] = useState<{ type: string; label: string; monthly_gross: number; rentenpunkte: number; start_age: number; tax_portion_pct: number }[]>(() => {
    try { return JSON.parse(localStorage.getItem('pension_sources') || '[]'); } catch { return []; }
  });
  const [pensionAge, setPensionAge] = useState<number>(() => parseInt(localStorage.getItem('pension_age') || '67') || 67);
  const [pensionCurrentAge, setPensionCurrentAge] = useState<number>(() => parseInt(localStorage.getItem('pension_current_age') || '35') || 35);
  const [pensionNeed, setPensionNeed] = useState<number>(() => parseInt(localStorage.getItem('pension_need') || '3000') || 3000);
  const [pensionContrib, setPensionContrib] = useState<string>(() => localStorage.getItem('pension_contrib') || '');

  // (Insurance Inventory, Estate heirs, Opportunity Cost, Rentenlücke
  // result: owned by their respective section components — see
  // components/planning/.)

  const loadData = useCallback(async () => {
    try {
      setLoadError(null);
      const [accRes, nwRes] = await Promise.all([
        api.listAccounts(),
        api.getNetWorth(9999),
      ]);
      setAccounts(accRes.accounts || []);
      setAllSnapshots(nwRes.snapshots || []);
    } catch (e) {
      console.error('Failed to load planning data:', e);
      setLoadError(e instanceof Error ? e.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  useEffect(() => {
    // Opportunity-cost fetch lives in the <OpportunityCosts /> component
    // now; Planning only kicks off the projection fetch (consumed by FIRE
    // Calculator + the Withdrawal sections via PlanningContext).
    api.getProjection().then(d => {
      setProjData(d);
      if (d.contribution != null) setProjContrib(String(d.contribution));
      if (d.return_pct != null) setProjReturn(String(d.return_pct));
    }).catch(() => {});
  }, []);

  // Re-run projection when FIRE inputs change — FIRE Calculator surfaces the
  // refreshed milestone/sensitivity bands from projData immediately.
  useEffect(() => {
    if (!projContrib && !projReturn) return;
    const timer = setTimeout(() => {
      const c = parseFloat(projContrib);
      const r = parseFloat(projReturn);
      const rawExp = parseFloat(fireExpenses) || 0;
      const income = (parseFloat(pensionIncome) || 0) + (parseFloat(otherIncome) || 0);
      const gap = Math.max(rawExp - income, 0);
      const sw = parseFloat(fireSWR) || 3.5;
      const mr = parseFloat(crMarginalRate);
      const marginalDecimal = !isNaN(mr) && mr >= 0 && mr <= 100 ? mr / 100 : undefined;
      if (!isNaN(c) && !isNaN(r)) {
        api.getProjection(c, r, gap > 0 ? gap : undefined, gap > 0 ? sw : undefined, marginalDecimal).then(setProjData).catch(() => {});
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [projContrib, projReturn, fireExpenses, fireSWR, pensionIncome, otherIncome, crMarginalRate]);

  const totalNetWorth = allSnapshots.length > 0 ? allSnapshots[0].total : 0;
  const fmt = (n: number) => new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  // Context value — memoize so sections don't re-render on unrelated state
  // changes. Setters from useState are already stable references.
  const planningCtx = useMemo<PlanningState>(() => ({
    accounts, allSnapshots, totalNetWorth,
    projData, projContrib, setProjContrib, projReturn, setProjReturn,
    fireExpenses, setFireExpenses, fireSWR, setFireSWR,
    pensionIncome, setPensionIncome, otherIncome, setOtherIncome,
    crMarginalRate, setCrMarginalRate, crChildren, setCrChildren,
    crBudget, setCrBudget, crGrossIncome, setCrGrossIncome,
    pensionSources, setPensionSources,
    pensionAge, setPensionAge, pensionCurrentAge, setPensionCurrentAge,
    pensionNeed, setPensionNeed, pensionContrib, setPensionContrib,
    fmt,
  }), [
    accounts, allSnapshots, totalNetWorth,
    projData, projContrib, projReturn,
    fireExpenses, fireSWR, pensionIncome, otherIncome,
    crMarginalRate, crChildren, crBudget, crGrossIncome,
    pensionSources, pensionAge, pensionCurrentAge, pensionNeed, pensionContrib,
  ]);

  if (loading) {
    return (
      <div className="px-1 md:px-0 py-6 md:py-8">
        <h1 className="font-serif text-title text-ink mb-3">Planning</h1>
        <div className="text-[13px] text-ink-muted">Loading…</div>
      </div>
    );
  }

  if (loadError) {
    return (
      <div className="px-1 md:px-0 py-6 md:py-8">
        <h1 className="font-serif text-title text-ink mb-3">Planning</h1>
        <div className="rounded-[8px] border border-claret bg-parchment-deep p-4 text-[13px] text-claret flex items-center justify-between">
          <span>Failed to load planning data: {loadError}</span>
          <button onClick={loadData} className="text-forest text-[13px] font-medium shrink-0">Retry</button>
        </div>
      </div>
    );
  }

  return (
    <PlanningProvider value={planningCtx}>
    <div className="px-1 md:px-0 py-6 md:py-8 space-y-6">
      <h1 className="font-serif text-title text-ink">Planning</h1>

      {/* Sub-tab navigation — splits 15 sections into FIRE/Growth/Protection
          so the page no longer requires 12 screens of scrolling. */}
      <TabBar
        tabs={PLANNING_TABS}
        activeTab={planningSubTab}
        onTabChange={(t) => { setPlanningSubTab(t); localStorage.setItem('planning_subtab', t); }}
      />

      {planningSubTab === 'fire' && <FireCalculator />}

      {planningSubTab === 'growth' && (<>
        <ContributionRouter />
        <MoneyLeftOnTable />
        <HumanCapital />
      </>)}

      {planningSubTab === 'protection' && <InsuranceInventory />}

      {planningSubTab === 'fire' && <Rentenluecke />}

      {planningSubTab === 'protection' && <EstateCalculator />}

      {planningSubTab === 'growth' && (<>
        <OpportunityCosts />
        <LifeEventSimulator />
      </>)}

      {planningSubTab === 'protection' && <LiabilityAmortization />}

      {planningSubTab === 'fire' && (<>
        <WithdrawalSources />
        <WithdrawalSequence />
        <WithdrawalSimulation />
        <FinancialIndependencePlaybook />
      </>)}

    </div>
    </PlanningProvider>
  );
}
