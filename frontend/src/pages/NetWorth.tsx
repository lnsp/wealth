import { useState, useEffect, useCallback } from 'react';
import { api, type Account, type NetWorthSnapshot, type ImportResult } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';
import CsvUploader from '../components/CsvUploader';
import AccountCard from '../components/AccountCard';

export default function NetWorth() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [snapshots, setSnapshots] = useState<NetWorthSnapshot[]>([]);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [loading, setLoading] = useState(true);

  const loadData = useCallback(async () => {
    try {
      const [accRes, nwRes] = await Promise.all([
        api.listAccounts(),
        api.getNetWorth(),
      ]);
      setAccounts(accRes.accounts);
      setSnapshots(nwRes.snapshots);
    } catch (e) {
      console.error('Failed to load data:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  const handleImport = useCallback((result: ImportResult) => {
    setImportResult(result);
    loadData();
  }, [loadData]);

  const totalNetWorth = snapshots.length > 0 ? snapshots[0].total : 0;
  const prevNetWorth = snapshots.length > 1 ? snapshots[1].total : totalNetWorth;
  const change = totalNetWorth - prevNetWorth;
  const changePct = prevNetWorth !== 0 ? (change / prevNetWorth) * 100 : 0;

  const chartOption = {
    tooltip: { trigger: 'axis' as const },
    xAxis: {
      type: 'category' as const,
      data: [...snapshots].reverse().map((s) => s.date),
      axisLabel: { fontSize: 11 },
    },
    yAxis: {
      type: 'value' as const,
      axisLabel: {
        formatter: (v: number) => `${(v / 1000).toFixed(0)}k`,
        fontSize: 11,
      },
    },
    series: [
      {
        name: 'Cash',
        type: 'line' as const,
        stack: 'total',
        areaStyle: { opacity: 0.4 },
        data: [...snapshots].reverse().map((s) => s.cash_component),
        smooth: true,
      },
      {
        name: 'Investments',
        type: 'line' as const,
        stack: 'total',
        areaStyle: { opacity: 0.4 },
        data: [...snapshots].reverse().map((s) => s.investment_component),
        smooth: true,
      },
    ],
    grid: { left: 60, right: 20, top: 20, bottom: 30 },
  };

  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-gray-400">Loading...</div>;
  }

  return (
    <div className="space-y-6">
      {/* Hero KPI */}
      <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
        <p className="text-sm text-gray-500 mb-1">Total Net Worth</p>
        <p className="text-3xl font-bold text-gray-900">{fmt(totalNetWorth)}</p>
        {snapshots.length > 1 && (
          <p className={`text-sm mt-1 ${change >= 0 ? 'text-green-600' : 'text-red-600'}`}>
            {change >= 0 ? '+' : ''}{fmt(change)} ({changePct >= 0 ? '+' : ''}{changePct.toFixed(2)}%)
          </p>
        )}
      </div>

      {/* Net Worth Chart */}
      {snapshots.length > 0 && (
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900 mb-4">Net Worth Over Time</h2>
          <EChartWrapper option={chartOption} />
        </div>
      )}

      {/* Account Cards */}
      {accounts.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-gray-900 mb-3">Accounts</h2>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {accounts.map((acc) => (
              <AccountCard key={acc.id} account={acc} />
            ))}
          </div>
        </div>
      )}

      {/* CSV Upload */}
      <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
        <h2 className="text-lg font-semibold text-gray-900 mb-4">Import CSV</h2>
        <CsvUploader accounts={accounts} onImportComplete={handleImport} />
        {importResult && (
          <div className="mt-4 rounded-lg bg-green-50 border border-green-200 p-3 text-sm text-green-700">
            Imported {importResult.imported} transactions, skipped {importResult.skipped}.
            {importResult.new_securities.length > 0 && (
              <span> New securities: {importResult.new_securities.join(', ')}</span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
