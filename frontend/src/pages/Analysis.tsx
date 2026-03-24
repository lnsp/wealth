import { useState, useEffect } from 'react';
import { api } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';

export default function Analysis() {
  const [sectors, setSectors] = useState<Record<string, number>>({});
  const [countries, setCountries] = useState<Record<string, number>>({});
  const [overlap, setOverlap] = useState<{ labels: string[]; matrix: number[][] } | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    Promise.all([api.getSectors(), api.getCountries(), api.getOverlap()])
      .then(([sec, ctr, ovl]) => {
        setSectors(sec.sectors);
        setCountries(ctr.countries);
        setOverlap(ovl);
      })
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-gray-400">Loading...</div>;
  }

  const sectorEntries = Object.entries(sectors).sort(([, a], [, b]) => b - a);
  const countryEntries = Object.entries(countries).sort(([, a], [, b]) => b - a);

  const sectorChartOption = {
    tooltip: { trigger: 'item' as const, formatter: '{b}: {d}%' },
    series: [{
      type: 'pie' as const,
      radius: ['40%', '70%'],
      data: sectorEntries.map(([name, value]) => ({ name, value: Math.round(value * 100) / 100 })),
      label: { fontSize: 11 },
    }],
  };

  const countryChartOption = {
    tooltip: { trigger: 'axis' as const },
    xAxis: {
      type: 'value' as const,
      axisLabel: { formatter: (v: number) => `${v.toFixed(0)}%`, fontSize: 11 },
    },
    yAxis: {
      type: 'category' as const,
      data: countryEntries.slice(0, 15).reverse().map(([name]) => name),
      axisLabel: { fontSize: 11 },
    },
    series: [{
      type: 'bar' as const,
      data: countryEntries.slice(0, 15).reverse().map(([, value]) => Math.round(value * 100) / 100),
    }],
    grid: { left: 80, right: 20, top: 10, bottom: 30 },
  };

  const overlapChartOption = overlap && overlap.labels.length > 0 ? {
    tooltip: {
      formatter: (params: { data: number[] }) => {
        const [x, y, val] = params.data;
        return `${overlap.labels[x]} vs ${overlap.labels[y]}: ${val.toFixed(1)}%`;
      },
    },
    xAxis: {
      type: 'category' as const,
      data: overlap.labels,
      axisLabel: { rotate: 45, fontSize: 10 },
    },
    yAxis: {
      type: 'category' as const,
      data: overlap.labels,
      axisLabel: { fontSize: 10 },
    },
    visualMap: {
      min: 0,
      max: 100,
      calculable: true,
      orient: 'horizontal' as const,
      left: 'center',
      bottom: 0,
      inRange: { color: ['#f0fdf4', '#16a34a'] },
    },
    series: [{
      type: 'heatmap' as const,
      data: overlap.matrix.flatMap((row, i) =>
        row.map((val, j) => [i, j, Math.round(val * 10) / 10])
      ),
      label: { show: true, fontSize: 10, formatter: (p: { data: number[] }) => `${p.data[2]}%` },
    }],
    grid: { left: 100, right: 20, top: 10, bottom: 80 },
  } : null;

  const hasData = sectorEntries.length > 0 || countryEntries.length > 0;

  return (
    <div className="space-y-6">
      {!hasData && (
        <div className="rounded-xl bg-white p-12 shadow-sm border border-gray-200 text-center text-gray-400">
          No analysis data available yet. Import transactions and wait for ETF metadata to be fetched.
        </div>
      )}

      {sectorEntries.length > 0 && (
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900 mb-4">Sector Allocation</h2>
          <EChartWrapper option={sectorChartOption} />
        </div>
      )}

      {countryEntries.length > 0 && (
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900 mb-4">Country Allocation</h2>
          <EChartWrapper option={countryChartOption} height="500px" />
        </div>
      )}

      {overlapChartOption && (
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900 mb-4">ETF Overlap Matrix</h2>
          <EChartWrapper option={overlapChartOption} height="500px" />
        </div>
      )}
    </div>
  );
}
