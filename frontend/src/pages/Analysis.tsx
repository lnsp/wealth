import { useState, useEffect } from 'react';
import { api, type ETFHoldingEntry, type HoldingRow } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';

export default function Analysis() {
  const [sectors, setSectors] = useState<Record<string, number>>({});
  const [countries, setCountries] = useState<Record<string, number>>({});
  const [overlap, setOverlap] = useState<{ labels: string[]; matrix: number[][] } | null>(null);
  const [holdings, setHoldings] = useState<HoldingRow[]>([]);
  const [loading, setLoading] = useState(true);

  // ETF holdings drill-down
  const [selectedETF, setSelectedETF] = useState<string | null>(null);
  const [etfHoldings, setEtfHoldings] = useState<ETFHoldingEntry[]>([]);
  const [etfName, setEtfName] = useState('');
  const [etfLoading, setEtfLoading] = useState(false);

  useEffect(() => {
    Promise.all([api.getSectors(), api.getCountries(), api.getOverlap(), api.listHoldings()])
      .then(([sec, ctr, ovl, hld]) => {
        setSectors(sec.sectors);
        setCountries(ctr.countries);
        setOverlap(ovl);
        setHoldings(hld.holdings.filter(h => h.asset_class === 'etf'));
      })
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  const loadETFHoldings = (isin: string) => {
    if (selectedETF === isin) {
      setSelectedETF(null);
      setEtfHoldings([]);
      return;
    }
    setSelectedETF(isin);
    setEtfLoading(true);
    api.getETFHoldings(isin)
      .then((data) => {
        setEtfHoldings(data.holdings);
        setEtfName(data.etf_name);
      })
      .catch(console.error)
      .finally(() => setEtfLoading(false));
  };

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-apple-callout text-apple-gray-2">Loading...</div>;
  }

  const sectorEntries = Object.entries(sectors).sort(([, a], [, b]) => b - a);
  const countryEntries = Object.entries(countries).sort(([, a], [, b]) => b - a);

  const sectorChartOption = {
    tooltip: { trigger: 'item' as const, formatter: '{b}: {d}%' },
    series: [{
      type: 'pie' as const,
      radius: ['40%', '70%'],
      data: sectorEntries.map(([name, value]) => ({ name, value: Math.round(value * 100) / 100 })),
      label: { fontSize: 12, color: '#3C3C43' },
      itemStyle: { borderColor: '#fff', borderWidth: 2 },
    }],
  };

  const countryChartOption = {
    tooltip: { trigger: 'axis' as const },
    xAxis: {
      type: 'value' as const,
      axisLabel: { formatter: (v: number) => `${v.toFixed(0)}%`, fontSize: 12, color: '#8E8E93' },
      splitLine: { lineStyle: { color: '#F2F2F7' } },
    },
    yAxis: {
      type: 'category' as const,
      data: countryEntries.slice(0, 15).reverse().map(([name]) => name),
      axisLabel: { fontSize: 12, color: '#3C3C43' },
    },
    series: [{
      type: 'bar' as const,
      data: countryEntries.slice(0, 15).reverse().map(([, value]) => Math.round(value * 100) / 100),
      itemStyle: { borderRadius: [0, 4, 4, 0] },
      barMaxWidth: 20,
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
      axisLabel: { rotate: 45, fontSize: 11, color: '#3C3C43' },
    },
    yAxis: {
      type: 'category' as const,
      data: overlap.labels,
      axisLabel: { fontSize: 11, color: '#3C3C43' },
    },
    visualMap: {
      min: 0,
      max: 100,
      calculable: true,
      orient: 'horizontal' as const,
      left: 'center',
      bottom: 0,
      inRange: { color: ['#F2F2F7', '#34C759'] },
      textStyle: { color: '#8E8E93' },
    },
    series: [{
      type: 'heatmap' as const,
      data: overlap.matrix.flatMap((row, i) =>
        row.map((val, j) => [i, j, Math.round(val * 10) / 10])
      ),
      label: { show: true, fontSize: 11, formatter: (p: { data: number[] }) => `${p.data[2]}%` },
      itemStyle: { borderColor: '#fff', borderWidth: 2 },
    }],
    grid: { left: 100, right: 20, top: 10, bottom: 80 },
  } : null;

  const hasData = sectorEntries.length > 0 || countryEntries.length > 0;

  return (
    <div className="space-y-6">
      <h1 className="text-apple-title1 text-gray-900">Analysis</h1>

      {!hasData && holdings.length === 0 && (
        <div className="apple-card p-12 text-center text-apple-callout text-apple-gray-2">
          No analysis data available yet. Import transactions and wait for ETF metadata to be fetched.
        </div>
      )}

      {sectorEntries.length > 0 && (
        <div className="apple-card p-5">
          <h2 className="text-apple-headline text-gray-900 mb-4">Sector Allocation</h2>
          <EChartWrapper option={sectorChartOption} />
        </div>
      )}

      {countryEntries.length > 0 && (
        <div className="apple-card p-5">
          <h2 className="text-apple-headline text-gray-900 mb-4">Country Allocation</h2>
          <EChartWrapper option={countryChartOption} height="500px" />
        </div>
      )}

      {overlapChartOption && (
        <div className="apple-card p-5">
          <h2 className="text-apple-headline text-gray-900 mb-4">ETF Overlap Matrix</h2>
          <EChartWrapper option={overlapChartOption} height="500px" />
        </div>
      )}

      {holdings.length > 0 && (
        <div className="apple-card p-5">
          <h2 className="text-apple-headline text-gray-900 mb-1">ETF Holdings Breakdown</h2>
          <p className="text-apple-footnote text-apple-gray-1 mb-4">Select an ETF to view its individual constituent positions.</p>

          {/* Segmented-control-style ETF selector */}
          <div className="flex flex-wrap gap-2 mb-5">
            {holdings.map((h) => (
              <button
                key={h.security_isin}
                onClick={() => loadETFHoldings(h.security_isin)}
                className={`rounded-[8px] px-3.5 py-[7px] text-apple-subhead font-medium transition-all duration-150 ${
                  selectedETF === h.security_isin
                    ? 'bg-apple-blue text-white shadow-apple-sm'
                    : 'bg-apple-gray-6 text-gray-700 hover:bg-apple-gray-5 active:bg-apple-gray-4'
                }`}
              >
                {h.name}
              </button>
            ))}
          </div>

          {etfLoading && (
            <div className="py-8 text-center text-apple-callout text-apple-gray-2">Loading holdings...</div>
          )}

          {selectedETF && !etfLoading && etfHoldings.length === 0 && (
            <div className="py-8 text-center text-apple-callout text-apple-gray-2">
              No constituent data available for this ETF yet.
            </div>
          )}

          {selectedETF && !etfLoading && etfHoldings.length > 0 && (
            <div>
              <h3 className="text-apple-subhead font-semibold text-gray-700 mb-3">
                {etfName} — {etfHoldings.length} positions
              </h3>
              <div className="overflow-x-auto -mx-5">
                <table className="w-full">
                  <thead>
                    <tr className="text-left text-apple-caption1 text-apple-gray-1 uppercase tracking-wider">
                      <th className="px-5 pb-2 font-medium">#</th>
                      <th className="px-5 pb-2 font-medium">Name</th>
                      <th className="px-5 pb-2 font-medium">ISIN</th>
                      <th className="px-5 pb-2 font-medium text-right">Weight</th>
                      <th className="px-5 pb-2 font-medium">Sector</th>
                      <th className="px-5 pb-2 font-medium">Country</th>
                    </tr>
                  </thead>
                  <tbody>
                    {etfHoldings.map((entry, i) => (
                      <tr
                        key={entry.isin}
                        className={`transition-colors hover:bg-apple-gray-6/60 ${
                          i < etfHoldings.length - 1 ? 'border-b border-apple-gray-5' : ''
                        }`}
                      >
                        <td className="px-5 py-2.5 text-apple-caption1 text-apple-gray-2 tabular-nums">{i + 1}</td>
                        <td className="px-5 py-2.5 text-apple-subhead font-medium text-gray-900">{entry.name}</td>
                        <td className="px-5 py-2.5 font-mono text-apple-caption1 text-apple-gray-1">{entry.isin}</td>
                        <td className="px-5 py-2.5 text-right">
                          <span className="text-apple-subhead font-medium tabular-nums">{entry.weight.toFixed(2)}%</span>
                          <div className="mt-1 h-1 w-full rounded-full bg-apple-gray-5">
                            <div
                              className="h-1 rounded-full bg-apple-blue transition-all duration-300"
                              style={{ width: `${Math.min(entry.weight * 2, 100)}%` }}
                            />
                          </div>
                        </td>
                        <td className="px-5 py-2.5 text-apple-subhead text-apple-gray-1">{entry.sector || '—'}</td>
                        <td className="px-5 py-2.5 text-apple-subhead text-apple-gray-1">{entry.country || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
