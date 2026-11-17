import type { AssetKind } from '../../api/client';
import { fmtNum } from '../../utils/format';

// Number, money and date formatting lives in utils/format.ts. Only helpers that are
// specific to the Assets views stay here.

export const fmtKm = (n: number) => `${fmtNum(n)} km`;
export const today = () => new Date().toISOString().slice(0, 10);

export const KIND_LABEL: Record<AssetKind, string> = {
  car: 'Car', apartment: 'Apartment', house: 'House', land: 'Land', other: 'Other',
};

// Composition colours come from the theme tokens, so the bar re-tunes with dark mode.
// Identity is always carried by the labelled list next to the bar, never by colour alone.
export const PART_COLORS = {
  investments: 'var(--color-forest)',
  cash: 'var(--color-slate)',
  property: 'var(--color-sage)',
  car: 'var(--color-gold)',
  land: 'var(--color-walnut)',
  other: 'var(--color-amber)',
};

export function partColor(kind: AssetKind): string {
  if (kind === 'car') return PART_COLORS.car;
  if (kind === 'land') return PART_COLORS.land;
  if (kind === 'other') return PART_COLORS.other;
  return PART_COLORS.property;
}

export const isProperty = (kind: AssetKind) => kind === 'apartment' || kind === 'house';

/** Hairline card on the page background (style guide `apple-card`). */
export const cardCls = 'apple-card p-4 md:p-5';
/** Serif heading inside a card. */
export const cardTitleCls = 'font-serif text-[20px] leading-7 font-medium text-ink';
/** Text link sized as a touch target. */
export const linkBtnCls = 'inline-flex items-center min-h-[44px] md:min-h-[32px] text-[13px] font-medium text-forest hover:underline';
/** Auto-fitting form grid. */
export const formGrid = (min = 190) => ({ gridTemplateColumns: `repeat(auto-fit, minmax(min(100%, ${min}px), 1fr))` });
