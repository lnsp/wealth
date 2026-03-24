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
      axisLabel: { fontSize: 12, color: '#8E8E93' },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value' as const,
      axisLabel: {
        formatter: (v: number) => `${(v / 1000).toFixed(0)}k`,
        fontSize: 12,
        color: '#8E8E93',
      },
      splitLine: { lineStyle: { color: '#F2F2F7' } },
    },
    series: [
      {
        name: 'Cash',
        type: 'line' as const,
        stack: 'total',
        areaStyle: { opacity: 0.3 },
        data: [...snapshots].reverse().map((s) => s.cash_component),
        smooth: 0.4,
        showSymbol: false,
        lineStyle: { width: 2 },
      },
      {
        name: 'Investments',
        type: 'line' as const,
        stack: 'total',
        areaStyle: { opacity: 0.3 },
        data: [...snapshots].reverse().map((s) => s.investment_component),
        smooth: 0.4,
        showSymbol: false,
        lineStyle: { width: 2 },
      },
    ],
    grid: { left: 55, right: 16, top: 16, bottom: 28 },
  };

  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-apple-callout text-apple-gray-2">Loading...</div>;
  }

  return (
    <div className="space-y-6">
      {/* Page title */}
      <h1 className="text-apple-title1 text-gray-900">Net Worth</h1>

      {/* Hero KPI */}
      <div className="apple-card p-6">
        <p className="text-apple-footnote text-apple-gray-1 mb-1">Total Net Worth</p>
        <p className="text-[34px] font-bold tracking-tight text-gray-900 leading-tight">{fmt(totalNetWorth)}</p>
        {snapshots.length > 1 && (
          <p className={`text-apple-subhead font-medium mt-1 ${change >= 0 ? 'text-apple-green' : 'text-apple-red'}`}>
            {change >= 0 ? '+' : ''}{fmt(change)} ({changePct >= 0 ? '+' : ''}{changePct.toFixed(2)}%)
          </p>
        )}
      </div>

      {/* Net Worth Chart */}
      {snapshots.length > 0 && (
        <div className="apple-card p-5">
          <h2 className="text-apple-headline text-gray-900 mb-4">Net Worth Over Time</h2>
          <EChartWrapper option={chartOption} />
        </div>
      )}

      {/* Account Cards */}
      {accounts.length > 0 && (
        <div>
          <h2 className="text-apple-headline text-gray-900 mb-3">Accounts</h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {accounts.map((acc) => (
              <AccountCard key={acc.id} account={acc} />
            ))}
          </div>
        </div>
      )}

      {/* CSV Upload */}
      <div className="apple-card p-5">
        <h2 className="text-apple-headline text-gray-900 mb-4">Import CSV</h2>
        <CsvUploader accounts={accounts} onImportComplete={handleImport} />
        {importResult && (
          <div className="mt-4 rounded-apple bg-apple-green/8 px-4 py-3 text-apple-subhead text-apple-green">
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
