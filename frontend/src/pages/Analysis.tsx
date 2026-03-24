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
      {!hasData && holdings.length === 0 && (
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

      {holdings.length > 0 && (
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900 mb-4">ETF Holdings Breakdown</h2>
          <p className="text-sm text-gray-500 mb-4">Select an ETF to view its individual constituent positions.</p>
          <div className="flex flex-wrap gap-2 mb-4">
            {holdings.map((h) => (
              <button
                key={h.security_isin}
                onClick={() => loadETFHoldings(h.security_isin)}
                className={`rounded-lg px-3 py-1.5 text-sm font-medium transition-colors ${
                  selectedETF === h.security_isin
                    ? 'bg-gray-900 text-white'
                    : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                }`}
              >
                {h.name}
              </button>
            ))}
          </div>

          {etfLoading && (
            <div className="py-8 text-center text-gray-400">Loading holdings...</div>
          )}

          {selectedETF && !etfLoading && etfHoldings.length === 0 && (
            <div className="py-8 text-center text-gray-400">
              No constituent data available for this ETF yet. Holdings are fetched weekly on Sundays.
            </div>
          )}

          {selectedETF && !etfLoading && etfHoldings.length > 0 && (
            <div>
              <h3 className="text-sm font-semibold text-gray-700 mb-2">
                {etfName} — {etfHoldings.length} positions
              </h3>
              <div className="overflow-x-auto">
                <table className="min-w-full text-sm">
                  <thead>
                    <tr className="border-b border-gray-200 text-left text-gray-500">
                      <th className="py-2 pr-4 font-medium">#</th>
                      <th className="py-2 pr-4 font-medium">Name</th>
                      <th className="py-2 pr-4 font-medium">ISIN</th>
                      <th className="py-2 pr-4 font-medium text-right">Weight</th>
                      <th className="py-2 pr-4 font-medium">Sector</th>
                      <th className="py-2 font-medium">Country</th>
                    </tr>
                  </thead>
                  <tbody>
                    {etfHoldings.map((entry, i) => (
                      <tr key={entry.isin} className="border-b border-gray-100 hover:bg-gray-50">
                        <td className="py-2 pr-4 text-gray-400">{i + 1}</td>
                        <td className="py-2 pr-4 font-medium text-gray-900">{entry.name}</td>
                        <td className="py-2 pr-4 font-mono text-xs text-gray-500">{entry.isin}</td>
                        <td className="py-2 pr-4 text-right">
                          <span className="font-medium">{entry.weight.toFixed(2)}%</span>
                          <div className="mt-0.5 h-1 w-full rounded bg-gray-100">
                            <div
                              className="h-1 rounded bg-green-500"
                              style={{ width: `${Math.min(entry.weight * 2, 100)}%` }}
                            />
                          </div>
                        </td>
                        <td className="py-2 pr-4 text-gray-600">{entry.sector || '—'}</td>
                        <td className="py-2 text-gray-600">{entry.country || '—'}</td>
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
