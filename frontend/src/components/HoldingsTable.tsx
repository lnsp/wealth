import type { HoldingRow } from '../api/client';

interface Props {
  holdings: HoldingRow[];
}

export default function HoldingsTable({ holdings }: Props) {
  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);
  const fmtQty = (n: number) =>
    new Intl.NumberFormat('de-DE', { minimumFractionDigits: 3, maximumFractionDigits: 3 }).format(n);

  return (
    <div className="overflow-x-auto -mx-5">
      <table className="w-full">
        <thead>
          <tr className="text-left text-apple-caption1 text-apple-gray-1 uppercase tracking-wider">
            <th className="px-5 pb-2 font-medium">Name</th>
            <th className="px-5 pb-2 font-medium text-right">Quantity</th>
            <th className="px-5 pb-2 font-medium text-right">Avg Cost</th>
            <th className="px-5 pb-2 font-medium text-right">Dividends</th>
            <th className="px-5 pb-2 font-medium">ISIN</th>
          </tr>
        </thead>
        <tbody>
          {holdings.map((h, i) => (
            <tr
              key={`${h.account_id}-${h.security_isin}`}
              className={`transition-colors hover:bg-apple-gray-6/60 ${
                i < holdings.length - 1 ? 'border-b border-apple-gray-5' : ''
              }`}
            >
              <td className="px-5 py-3">
                <div className="text-apple-subhead font-medium text-gray-900">{h.name}</div>
                <div className="text-apple-caption1 text-apple-gray-1 mt-0.5">
                  {h.asset_class}{h.symbol ? ` · ${h.symbol}` : ''}
                </div>
              </td>
              <td className="px-5 py-3 text-right text-apple-subhead tabular-nums">{fmtQty(h.quantity)}</td>
              <td className="px-5 py-3 text-right text-apple-subhead tabular-nums">{fmt(h.avg_cost_basis)}</td>
              <td className="px-5 py-3 text-right text-apple-subhead tabular-nums text-apple-green">{fmt(h.total_dividends)}</td>
              <td className="px-5 py-3 text-apple-caption1 text-apple-gray-1 font-mono">{h.security_isin}</td>
            </tr>
          ))}
          {holdings.length === 0 && (
            <tr>
              <td colSpan={5} className="px-5 py-12 text-center text-apple-callout text-apple-gray-2">
                No holdings yet. Import transactions to see your portfolio.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
