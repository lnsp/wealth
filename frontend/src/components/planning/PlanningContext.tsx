import { createContext, useContext } from 'react';
import type { Account, NetWorthSnapshot, ProjectionData } from '../../api/client';

export interface PensionSource {
  type: string;
  label: string;
  monthly_gross: number;
  rentenpunkte: number;
  start_age: number;
  tax_portion_pct: number;
}

// Shared state accessible to all planning sections. Lives in one place so
// extracting sections doesn't require threading 20 props through every
// component. Sections read what they need via usePlanning().
//
// Every setter persists its value (see pages/Planning.tsx); sections never
// touch localStorage for these fields themselves.
export interface PlanningState {
  // Shared dashboard data
  accounts: Account[];
  allSnapshots: NetWorthSnapshot[];
  totalNetWorth: number;

  // Projection (FIRE Calculator + Life Events read this)
  projData: ProjectionData | null;
  /** The projection request failed; `reloadProjection` retries it. */
  projFailed: boolean;
  reloadProjection: () => void;
  /** Monthly contribution the backend derived from the last 12 months of deposits. */
  projDefaultContrib: number | null;
  projContrib: string;
  setProjContrib: (v: string) => void;
  projReturn: string;
  setProjReturn: (v: string) => void;

  // FIRE inputs — read by FIRE Calculator AND by Contribution Router,
  // Withdrawal Sequence / Simulation and the Playbook.
  fireExpenses: string;
  setFireExpenses: (v: string) => void;
  fireSWR: string;
  setFireSWR: (v: string) => void;
  pensionIncome: string;
  setPensionIncome: (v: string) => void;
  otherIncome: string;
  setOtherIncome: (v: string) => void;

  // "Your Profile" — the inputs several calculators share. Edited in one
  // place at the top of the page (pages/Planning.tsx).
  crMarginalRate: string;
  setCrMarginalRate: (v: string) => void;
  crChildren: string;
  setCrChildren: (v: string) => void;
  /** Monthly investment budget. Net Worth's goal shortfall reads the same localStorage key. */
  crBudget: string;
  setCrBudget: (v: string) => void;
  crGrossIncome: string;
  setCrGrossIncome: (v: string) => void;
  /** Retirement age. */
  pensionAge: number;
  setPensionAge: (v: number) => void;
  /** Current age. */
  pensionCurrentAge: number;
  setPensionCurrentAge: (v: number) => void;

  // Pension gap
  pensionSources: PensionSource[];
  setPensionSources: (v: PensionSource[]) => void;
  pensionNeed: number;
  setPensionNeed: (v: number) => void;
  pensionContrib: string;
  setPensionContrib: (v: string) => void;

  // Growth until retirement, % p.a. — edited in Withdrawal Sources, also
  // used by Withdrawal Simulation.
  growthBrokerage: string;
  setGrowthBrokerage: (v: string) => void;
  growthOther: string;
  setGrowthOther: (v: string) => void;
}

const PlanningCtx = createContext<PlanningState | null>(null);

export const PlanningProvider = PlanningCtx.Provider;

export function usePlanning(): PlanningState {
  const ctx = useContext(PlanningCtx);
  if (!ctx) throw new Error('usePlanning() called outside <PlanningProvider>');
  return ctx;
}
