import type { AssetKind } from '../../api/client';

export const fmt = (n: number) =>
  new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(n);
export const fmtCents = (n: number) =>
  new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);
export const fmtSigned = (n: number) => `${n >= 0 ? '+' : '−'}${fmt(Math.abs(n))}`;
export const fmtKm = (n: number) => `${new Intl.NumberFormat('de-DE', { maximumFractionDigits: 0 }).format(n)} km`;
export const fmtPct = (n: number, digits = 1) =>
  `${n.toLocaleString('de-DE', { minimumFractionDigits: digits, maximumFractionDigits: digits })} %`;
export const fmtDate = (d: string) =>
  new Date(d).toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' });
export const fmtMonth = (d: string) =>
  new Date(d).toLocaleDateString('de-DE', { month: '2-digit', year: 'numeric' });
export const today = () => new Date().toISOString().slice(0, 10);

export const KIND_LABEL: Record<AssetKind, string> = {
  car: 'Car', apartment: 'Apartment', house: 'House', land: 'Land', other: 'Other',
};

// Composition colours, distinguishable by lightness as well as hue.
export const PART_COLORS = {
  investments: '#1B3D2F',
  cash: '#5E626C',
  property: '#9DB59D',
  car: '#CDB98A',
  land: '#7A6330',
  other: '#B9A9C9',
};

export function partColor(kind: AssetKind): string {
  if (kind === 'car') return PART_COLORS.car;
  if (kind === 'land') return PART_COLORS.land;
  if (kind === 'other') return PART_COLORS.other;
  return PART_COLORS.property;
}

export const isProperty = (kind: AssetKind) => kind === 'apartment' || kind === 'house';

// Shared input styling (matches the Settings forms).
export const fieldCls = 'w-full min-w-0 py-2 px-2.5 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink';
export const labelCls = 'flex flex-col min-w-0 gap-1 text-[12px] text-ink-muted';
export const cardCls = 'rounded-xl border border-divider bg-white dark:bg-parchment-deep p-4 md:p-5';
