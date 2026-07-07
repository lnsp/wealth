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

  // Apply legend defaults if legend exists
  if (option.legend) {
    mergedOption.legend = {
      ...theme.legend,
      ...((option.legend as Record<string, unknown>) || {}),
      textStyle: {
        ...theme.legend.textStyle,
        ...(((option.legend as Record<string, unknown>)?.textStyle as Record<string, unknown>) || {}),
      },
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
