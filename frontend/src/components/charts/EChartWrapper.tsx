import ReactECharts from 'echarts-for-react';
import type { EChartsOption } from 'echarts';
import { chartTheme } from './theme';

interface Props {
  option: EChartsOption;
  height?: string;
  className?: string;
}

export default function EChartWrapper({ option, height = '400px', className }: Props) {
  const mergedOption = {
    ...option,
    color: option.color || chartTheme.color,
    textStyle: { ...chartTheme.textStyle, ...((option.textStyle as Record<string, unknown>) || {}) },
    animationDuration: 600,
    animationEasing: 'cubicInOut',
  };

  return (
    <ReactECharts
      option={mergedOption}
      style={{ height, width: '100%' }}
      className={className}
      notMerge={true}
    />
  );
}
