import { createContext, useContext } from 'react';
import type { Account, NetWorthSnapshot, ProjectionData } from '../../api/client';

// Shared state accessible to all planning sections. Lives in one place so
// extracting sections doesn't require threading 20 props through every
// component. Sections read what they need via usePlanning().
export interface PlanningState {
  // Shared dashboard data
  accounts: Account[];
  allSnapshots: NetWorthSnapshot[];
  totalNetWorth: number;

  // Projection (FIRE Calculator + Withdrawal sections read this)
  projData: ProjectionData | null;
  projContrib: string;
  setProjContrib: (v: string) => void;
  projReturn: string;
  setProjReturn: (v: string) => void;

  // FIRE inputs — read by FIRE Calculator AND by Withdrawal Sequence /
  // Withdrawal Simulation / Financial Independence Playbook, so state has
  // to live above the sections.
  fireExpenses: string;
  setFireExpenses: (v: string) => void;
  fireSWR: string;
  setFireSWR: (v: string) => void;
  pensionIncome: string;
  setPensionIncome: (v: string) => void;
  otherIncome: string;
  setOtherIncome: (v: string) => void;

  // Contribution Router — Goal Priority shortfall on Net Worth Overview
  // reads crBudget too (currently via localStorage), so keep the writer
  // here.
  crMarginalRate: string;
  setCrMarginalRate: (v: string) => void;
  crChildren: string;
  setCrChildren: (v: string) => void;
  crBudget: string;
  setCrBudget: (v: string) => void;
  crGrossIncome: string;
  setCrGrossIncome: (v: string) => void;

  // Rentenlücke — pensionAge/CurrentAge feed Withdrawal Sources/Sequence/
  // Simulation, so it has to be shared.
  pensionSources: { type: string; label: string; monthly_gross: number; rentenpunkte: number; start_age: number; tax_portion_pct: number }[];
  setPensionSources: React.Dispatch<React.SetStateAction<{ type: string; label: string; monthly_gross: number; rentenpunkte: number; start_age: number; tax_portion_pct: number }[]>>;
  pensionAge: number;
  setPensionAge: (v: number) => void;
  pensionCurrentAge: number;
  setPensionCurrentAge: (v: number) => void;
  pensionNeed: number;
  setPensionNeed: (v: number) => void;
  pensionContrib: string;
  setPensionContrib: (v: string) => void;

  // Theme + formatters — passed through so sections don't all re-derive.
  fmt: (n: number) => string;
}

const PlanningCtx = createContext<PlanningState | null>(null);

export const PlanningProvider = PlanningCtx.Provider;

export function usePlanning(): PlanningState {
  const ctx = useContext(PlanningCtx);
  if (!ctx) throw new Error('usePlanning() called outside <PlanningProvider>');
  return ctx;
}
