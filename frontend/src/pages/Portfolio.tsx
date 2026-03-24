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
    return <div className="flex items-center justify-center py-20 text-apple-callout text-apple-gray-2">Loading...</div>;
  }

  const totalValue = holdings.reduce((sum, h) => sum + h.quantity * h.avg_cost_basis, 0);
  const totalDividends = holdings.reduce((sum, h) => sum + h.total_dividends, 0);
  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  return (
    <div className="space-y-6">
      <h1 className="text-apple-title1 text-gray-900">Portfolio</h1>

      {/* KPI row */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="apple-card p-5">
          <p className="text-apple-footnote text-apple-gray-1">Portfolio Value (Cost Basis)</p>
          <p className="text-apple-title2 text-gray-900 mt-1 tabular-nums">{fmt(totalValue)}</p>
        </div>
        <div className="apple-card p-5">
          <p className="text-apple-footnote text-apple-gray-1">Positions</p>
          <p className="text-apple-title2 text-gray-900 mt-1">{holdings.length}</p>
        </div>
        <div className="apple-card p-5">
          <p className="text-apple-footnote text-apple-gray-1">Total Dividends</p>
          <p className="text-apple-title2 text-apple-green mt-1 tabular-nums">{fmt(totalDividends)}</p>
        </div>
      </div>

      {/* Holdings */}
      <div className="apple-card p-5">
        <h2 className="text-apple-headline text-gray-900 mb-4">Holdings</h2>
        <HoldingsTable holdings={holdings} />
      </div>
    </div>
  );
}
