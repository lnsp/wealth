import { useState, useEffect } from 'react';
import { api, type HoldingRow } from '../api/client';
import HoldingsTable from '../components/HoldingsTable';

export default function Portfolio() {
  const [holdings, setHoldings] = useState<HoldingRow[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.listHoldings()
      .then((res) => setHoldings(res.holdings))
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-gray-400">Loading...</div>;
  }

  const totalValue = holdings.reduce((sum, h) => sum + h.quantity * h.avg_cost_basis, 0);
  const totalDividends = holdings.reduce((sum, h) => sum + h.total_dividends, 0);
  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <p className="text-sm text-gray-500">Portfolio Value (Cost Basis)</p>
          <p className="text-2xl font-bold text-gray-900 mt-1">{fmt(totalValue)}</p>
        </div>
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <p className="text-sm text-gray-500">Positions</p>
          <p className="text-2xl font-bold text-gray-900 mt-1">{holdings.length}</p>
        </div>
        <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
          <p className="text-sm text-gray-500">Total Dividends</p>
          <p className="text-2xl font-bold text-green-600 mt-1">{fmt(totalDividends)}</p>
        </div>
      </div>

      <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
        <h2 className="text-lg font-semibold text-gray-900 mb-4">Holdings</h2>
        <HoldingsTable holdings={holdings} />
      </div>
    </div>
  );
}
