// Heritage club chart theme — reads CSS custom properties for dark mode support

function getCSSVar(name: string, fallback: string): string {
  if (typeof document === 'undefined') return fallback;
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

export function getChartTheme() {
  const forest = getCSSVar('--color-forest', '#1B3D2F');
  const gold = getCSSVar('--color-gold', '#7A6330');
  const sage = getCSSVar('--color-sage', '#4A6A4A');
  const walnut = getCSSVar('--color-walnut', '#7A5C3A');
  const slate = getCSSVar('--color-slate', '#5E626C');
  const claret = getCSSVar('--color-claret', '#7A3040');
  const inkBody = getCSSVar('--color-ink-body', '#44403C');
  const inkMuted = getCSSVar('--color-ink-muted', '#5E5853');
  const divider = getCSSVar('--color-divider', '#E5E0D8');
  const parchment = getCSSVar('--color-parchment', '#FAF9F6');

  return {
    color: [forest, gold, sage, walnut, slate, claret],
    backgroundColor: 'transparent',
    textStyle: {
      fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, sans-serif',
      color: inkBody,
      fontSize: 13,
    },
    title: {
      textStyle: {
        color: getCSSVar('--color-ink', '#1C1917'),
        fontSize: 20,
        fontWeight: 500,
        fontFamily: '"EB Garamond", Georgia, serif',
      },
    },
    tooltip: {
      backgroundColor: parchment,
      borderColor: divider,
      borderWidth: 1,
      textStyle: {
        fontFamily: 'Inter, sans-serif',
        color: inkBody,
        fontSize: 13,
      },
      extraCssText: 'border-radius: 3px; box-shadow: none;',
    },
    legend: {
      textStyle: {
        fontFamily: 'Inter, sans-serif',
        color: inkBody,
        fontSize: 12,
      },
      itemGap: 16,
      icon: 'circle',
      itemWidth: 8,
      itemHeight: 8,
    },
    categoryAxis: {
      axisLabel: {
        fontFamily: 'Inter, sans-serif',
        color: inkMuted,
        fontSize: 11,
      },
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: {
        lineStyle: { color: divider, type: 'dashed' as const },
      },
    },
    valueAxis: {
      axisLabel: {
        fontFamily: 'Inter, sans-serif',
        color: inkMuted,
        fontSize: 11,
      },
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: {
        lineStyle: { color: divider, type: 'dashed' as const },
      },
    },
    line: {
      lineStyle: { width: 2 },
      symbolSize: 0,
      areaStyle: { opacity: 0.08 },
    },
    pie: {
      itemStyle: { borderWidth: 0 },
      label: {
        fontFamily: 'Inter, sans-serif',
        fontSize: 12,
        color: inkBody,
      },
      labelLine: {
        lineStyle: { color: inkMuted },
      },
    },
  };
}

// Static export for backward compatibility (light mode defaults)
export const chartTheme = getChartTheme();
