import { useMemo } from 'react';
import ReactECharts from 'echarts-for-react';
import type { EChartsOption } from 'echarts';
import { getChartTheme } from './theme';

interface Props {
  option: EChartsOption;
  height?: string;
  className?: string;
}

export default function EChartWrapper({ option, height = '400px', className }: Props) {
  // Re-read CSS vars on each render so dark mode changes are picked up
  const theme = useMemo(() => getChartTheme(), [
    // Re-compute when dark class toggles
    typeof document !== 'undefined' && document.documentElement.classList.contains('dark'),
  ]);

  const mergedOption: EChartsOption = {
    ...option,
    color: option.color || theme.color,
    textStyle: { ...theme.textStyle, ...((option.textStyle as Record<string, unknown>) || {}) },
    tooltip: {
      ...theme.tooltip,
      ...((option.tooltip as Record<string, unknown>) || {}),
    },
    animationDuration: 600,
    animationEasing: 'cubicInOut',
  };

  // Axis defaults: ECharts does not inherit these from a theme object passed per option, so
  // merge them into every axis. Explicit values in the chart's own option still win.
  const withAxisDefaults = (axis: unknown): unknown => {
    const apply = (a: Record<string, unknown>) => {
      const base = (a.type === 'category' ? theme.categoryAxis : theme.valueAxis) as Record<string, Record<string, unknown>>;
      const label = { ...base.axisLabel, ...((a.axisLabel as Record<string, unknown>) || {}) };
      // Chart text never drops below 11px.
      if (typeof label.fontSize === 'number' && label.fontSize < 11) label.fontSize = 11;
      // Category labels thin themselves out instead of colliding on narrow screens.
      if (a.type === 'category' && label.hideOverlap === undefined) label.hideOverlap = true;
      const split = (a.splitLine as Record<string, unknown>) || {};
      return {
        ...a,
        axisLabel: label,
        axisLine: { ...base.axisLine, ...((a.axisLine as Record<string, unknown>) || {}) },
        axisTick: { ...base.axisTick, ...((a.axisTick as Record<string, unknown>) || {}) },
        splitLine: {
          ...(a.type === 'category' ? { show: false } : {}),
          ...split,
          lineStyle: { ...base.splitLine.lineStyle as Record<string, unknown>, ...((split.lineStyle as Record<string, unknown>) || {}) },
        },
        nameTextStyle: { color: theme.valueAxis.axisLabel.color, fontSize: 11, ...((a.nameTextStyle as Record<string, unknown>) || {}) },
      };
    };
    if (Array.isArray(axis)) return axis.map(a => apply(a as Record<string, unknown>));
    if (axis && typeof axis === 'object') return apply(axis as Record<string, unknown>);
    return axis;
  };
  if (option.xAxis) mergedOption.xAxis = withAxisDefaults(option.xAxis) as EChartsOption['xAxis'];
  if (option.yAxis) mergedOption.yAxis = withAxisDefaults(option.yAxis) as EChartsOption['yAxis'];

  // Apply legend defaults if legend exists
  if (option.legend) {
    const legend = (option.legend as Record<string, unknown>) || {};
    mergedOption.legend = {
      ...theme.legend,
      ...legend,
      textStyle: {
        ...theme.legend.textStyle,
        ...((legend.textStyle as Record<string, unknown>) || {}),
      },
      // Themed pager for scrolling legends (otherwise near-invisible in dark mode).
      pageIconColor: theme.legend.textStyle.color,
      pageIconInactiveColor: theme.valueAxis.splitLine.lineStyle.color,
      pageTextStyle: { color: theme.legend.textStyle.color, fontSize: 11 },
    };
  }

  return (
    <ReactECharts
      option={mergedOption}
      style={{ height, width: '100%' }}
      className={className}
      notMerge={true}
    />
  );
}
