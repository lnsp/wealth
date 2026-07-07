import { useMemo } from 'react';

/** Reads current CSS custom property values — re-evaluates when dark mode toggles */
export function useThemeColors() {
  const isDark = typeof document !== 'undefined' && document.documentElement.classList.contains('dark');

  return useMemo(() => {
    const get = (name: string, fallback: string) => {
      if (typeof document === 'undefined') return fallback;
      return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
    };
    return {
      forest: get('--color-forest', '#1B3D2F'),
      forestLight: get('--color-forest-light', '#2A5A45'),
      gold: get('--color-gold', '#7A6330'),
      sage: get('--color-sage', '#4A6A4A'),
      claret: get('--color-claret', '#7A3040'),
      amber: get('--color-amber', '#7A5520'),
      walnut: get('--color-walnut', '#7A5C3A'),
      slate: get('--color-slate', '#5E626C'),
      ink: get('--color-ink', '#1C1917'),
      inkBody: get('--color-ink-body', '#44403C'),
      inkMuted: get('--color-ink-muted', '#5E5853'),
      divider: get('--color-divider', '#E5E0D8'),
      parchment: get('--color-parchment', '#FAF9F6'),
      parchmentDeep: get('--color-parchment-deep', '#F5F3EE'),
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isDark]);
}
