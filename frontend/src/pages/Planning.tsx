import { useState, useEffect, useCallback, useMemo } from 'react';
import type { ReactNode } from 'react';
import { api, type Account, type NetWorthSnapshot, type ProjectionData } from '../api/client';
import {
  PageHeader, Section, TabBar, TabPanel, useTabParam, NumberField,
  LoadingState, ErrorState, SectionBoundary,
} from '../components/ui';
import { PlanningProvider, usePlanning, type PensionSource, type PlanningState } from '../components/planning/PlanningContext';
import { readStored, writeStored, toNum } from '../components/planning/shared';
import ContributionRouter from '../components/planning/ContributionRouter';
import EstateCalculator from '../components/planning/EstateCalculator';
import FinancialIndependencePlaybook from '../components/planning/FinancialIndependencePlaybook';
import FireCalculator from '../components/planning/FireCalculator';
import HumanCapital from '../components/planning/HumanCapital';
import InsuranceInventory from '../components/planning/InsuranceInventory';
import LiabilityAmortization from '../components/planning/LiabilityAmortization';
import LifeEventSimulator from '../components/planning/LifeEventSimulator';
import OpportunityCosts from '../components/planning/OpportunityCosts';
import Rentenluecke from '../components/planning/Rentenluecke';
import WithdrawalSequence from '../components/planning/WithdrawalSequence';
import WithdrawalSimulation from '../components/planning/WithdrawalSimulation';
import WithdrawalSources from '../components/planning/WithdrawalSources';

type PlanningSubTab = 'fire' | 'growth' | 'protection';
const TAB_IDS: readonly PlanningSubTab[] = ['fire', 'growth', 'protection'];
const PLANNING_TABS: { id: PlanningSubTab; label: string }[] = [
  { id: 'fire', label: 'FIRE & Retirement' },
  { id: 'growth', label: 'Growth' },
  { id: 'protection', label: 'Protection' },
];

/** String state mirrored to localStorage under `key`. */
function useStored(key: string, initial: string): [string, (v: string) => void] {
  const [value, setValue] = useState<string>(() => readStored(key, initial));
  const set = useCallback((v: string) => { setValue(v); writeStored(key, v); }, [key]);
  return [value, set];
}

/** Whole-number state mirrored to localStorage under `key`. */
function useStoredInt(key: string, initial: number): [number, (v: number) => void] {
  const [value, setValue] = useState<number>(() => parseInt(readStored(key, String(initial)), 10) || initial);
  const set = useCallback((v: number) => { setValue(v); writeStored(key, String(v)); }, [key]);
  return [value, set];
}

/**
 * The inputs several calculators share, in one place and visible on every tab.
 * Individual calculators no longer repeat these fields.
 */
function ProfileSection() {
  const {
    pensionCurrentAge, setPensionCurrentAge, pensionAge, setPensionAge,
    crMarginalRate, setCrMarginalRate, crGrossIncome, setCrGrossIncome,
    crBudget, setCrBudget, crChildren, setCrChildren,
  } = usePlanning();

  return (
    <Section id="profile" title="Your Profile" subtitle="Used by every calculator on all three tabs." first>
      <div className="grid grid-cols-2 md:grid-cols-3 gap-x-3 gap-y-4">
        <NumberField label="Age" unit="years" integer min={0} value={pensionCurrentAge} onValueChange={v => setPensionCurrentAge(Math.max(0, v))} />
        <NumberField label="Retirement age" unit="years" integer min={0} value={pensionAge} onValueChange={v => setPensionAge(Math.max(0, v))} />
        <NumberField label="Marginal tax rate" unit="%" value={toNum(crMarginalRate)} onValueChange={v => setCrMarginalRate(String(v))} />
        <NumberField label="Gross income" unit="€ / year" value={toNum(crGrossIncome)} onValueChange={v => setCrGrossIncome(String(v))} />
        <NumberField label="Monthly budget" unit="€ / month" value={toNum(crBudget)} onValueChange={v => setCrBudget(String(v))} hint="What you can invest each month." />
        <NumberField label="Children" integer min={0} value={toNum(crChildren)} onValueChange={v => setCrChildren(String(Math.max(0, v)))} hint="Counts towards Riester allowances." />
      </div>
    </Section>
  );
}

/** One calculator: a render error shows an error band in its place, the rest of the page stays. */
function Guarded({ children }: { children: ReactNode }) {
  return <SectionBoundary>{children}</SectionBoundary>;
}

export default function Planning() {
  // Shared dashboard data — fetched independently of NetWorth so this page
  // stands on its own.
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [allSnapshots, setAllSnapshots] = useState<NetWorthSnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Projection — the FIRE calculator and Life Events read projData.contribution /
  // projData.return_pct as their default basis.
  const [projData, setProjData] = useState<ProjectionData | null>(null);
  const [projFailed, setProjFailed] = useState(false);
  const [projDefaultContrib, setProjDefaultContrib] = useState<number | null>(null);
  const [projContrib, setProjContrib] = useState<string>('');
  const [projReturn, setProjReturn] = useState<string>('');

  // Tab state lives in the URL (?tab=growth).
  const [tab, setTab] = useTabParam<PlanningSubTab>(TAB_IDS, 'fire');

  // FIRE inputs
  const [fireExpenses, setFireExpenses] = useStored('fire_expenses', '');
  const [fireSWR, setFireSWR] = useStored('fire_swr', '3.5');
  const [pensionIncome, setPensionIncome] = useStored('pension_income', '');
  const [otherIncome, setOtherIncome] = useStored('other_income', '');

  // Profile
  const [crMarginalRate, setCrMarginalRate] = useStored('marginal_tax_rate', '42');
  const [crChildren, setCrChildren] = useStored('num_children', '0');
  const [crBudget, setCrBudget] = useStored('monthly_invest_budget', '');
  const [crGrossIncome, setCrGrossIncome] = useStored('gross_annual_income', '');
  const [pensionAge, setPensionAge] = useStoredInt('pension_age', 67);
  const [pensionCurrentAge, setPensionCurrentAge] = useStoredInt('pension_current_age', 35);

  // Pension gap
  const [pensionSources, setPensionSourcesState] = useState<PensionSource[]>(() => {
    try {
      const parsed: unknown = JSON.parse(readStored('pension_sources', '[]'));
      return Array.isArray(parsed) ? (parsed as PensionSource[]) : [];
    } catch {
      return [];
    }
  });
  const setPensionSources = useCallback((v: PensionSource[]) => {
    setPensionSourcesState(v);
    writeStored('pension_sources', JSON.stringify(v));
  }, []);
  const [pensionNeed, setPensionNeed] = useStoredInt('pension_need', 3000);
  const [pensionContrib, setPensionContrib] = useStored('pension_contrib', '');

  // Growth until retirement (Withdrawal Sources + Simulation)
  const [growthBrokerage, setGrowthBrokerage] = useStored('planning_growth_brokerage', '7');
  const [growthOther, setGrowthOther] = useStored('planning_growth_other', '2');

  const loadData = useCallback(async () => {
    try {
      setLoading(true);
      setLoadError(null);
      const [accRes, nwRes] = await Promise.all([
        api.listAccounts(),
        api.getNetWorth(9999),
      ]);
      setAccounts(Array.isArray(accRes?.accounts) ? accRes.accounts : []);
      setAllSnapshots(Array.isArray(nwRes?.snapshots) ? nwRes.snapshots : []);
    } catch (e) {
      console.error('Failed to load planning data:', e);
      setLoadError(e instanceof Error ? e.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  const reloadProjection = useCallback(() => {
    setProjFailed(false);
    api.getProjection().then(d => {
      setProjData(d);
      if (d.contribution != null) {
        setProjDefaultContrib(d.contribution);
        setProjContrib(String(d.contribution));
      }
      if (d.return_pct != null) setProjReturn(String(d.return_pct));
    }).catch(() => setProjFailed(true));
  }, []);

  useEffect(() => { reloadProjection(); }, [reloadProjection]);

  // Re-run projection when FIRE inputs change — FIRE Calculator surfaces the
  // refreshed drawdown from projData immediately.
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

  // Context value — memoize so sections don't re-render on unrelated state
  // changes. Setters are stable references.
  const planningCtx = useMemo<PlanningState>(() => ({
    accounts, allSnapshots, totalNetWorth,
    projData, projFailed, reloadProjection, projDefaultContrib,
    projContrib, setProjContrib, projReturn, setProjReturn,
    fireExpenses, setFireExpenses, fireSWR, setFireSWR,
    pensionIncome, setPensionIncome, otherIncome, setOtherIncome,
    crMarginalRate, setCrMarginalRate, crChildren, setCrChildren,
    crBudget, setCrBudget, crGrossIncome, setCrGrossIncome,
    pensionAge, setPensionAge, pensionCurrentAge, setPensionCurrentAge,
    pensionSources, setPensionSources,
    pensionNeed, setPensionNeed, pensionContrib, setPensionContrib,
    growthBrokerage, setGrowthBrokerage, growthOther, setGrowthOther,
  }), [
    accounts, allSnapshots, totalNetWorth,
    projData, projFailed, reloadProjection, projDefaultContrib, projContrib, projReturn,
    fireExpenses, setFireExpenses, fireSWR, setFireSWR,
    pensionIncome, setPensionIncome, otherIncome, setOtherIncome,
    crMarginalRate, setCrMarginalRate, crChildren, setCrChildren,
    crBudget, setCrBudget, crGrossIncome, setCrGrossIncome,
    pensionAge, setPensionAge, pensionCurrentAge, setPensionCurrentAge,
    pensionSources, setPensionSources,
    pensionNeed, setPensionNeed, pensionContrib, setPensionContrib,
    growthBrokerage, setGrowthBrokerage, growthOther, setGrowthOther,
  ]);

  if (loading) {
    return (
      <div>
        <PageHeader title="Planning" />
        <LoadingState message="Loading planning data" lines={6} />
      </div>
    );
  }

  if (loadError) {
    return (
      <div className="space-y-4">
        <PageHeader title="Planning" />
        <ErrorState message="Planning data couldn’t be loaded." details={loadError} onRetry={loadData} />
      </div>
    );
  }

  return (
    <PlanningProvider value={planningCtx}>
      <div>
        <PageHeader title="Planning" />

        <div className="md:mt-4">
          <TabBar tabs={PLANNING_TABS} activeTab={tab} onTabChange={setTab} ariaLabel="Planning sections" />
        </div>

        <TabPanel id={tab}>
          <Guarded><ProfileSection /></Guarded>

          {tab === 'fire' && (<>
            <Guarded><FireCalculator /></Guarded>
            <Guarded><Rentenluecke /></Guarded>
            <Guarded><WithdrawalSources /></Guarded>
            <Guarded><WithdrawalSequence /></Guarded>
            <Guarded><WithdrawalSimulation /></Guarded>
            <Guarded><FinancialIndependencePlaybook /></Guarded>
          </>)}

          {tab === 'growth' && (<>
            <Guarded><ContributionRouter /></Guarded>
            <Guarded><OpportunityCosts /></Guarded>
            <Guarded><HumanCapital /></Guarded>
            <Guarded><LifeEventSimulator /></Guarded>
          </>)}

          {tab === 'protection' && (<>
            <Guarded><InsuranceInventory /></Guarded>
            <Guarded><EstateCalculator /></Guarded>
            <Guarded><LiabilityAmortization /></Guarded>
          </>)}
        </TabPanel>
      </div>
    </PlanningProvider>
  );
}
