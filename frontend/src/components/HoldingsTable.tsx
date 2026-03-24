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
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-left text-gray-500">
            <th className="pb-3 pr-4 font-medium">Name</th>
            <th className="pb-3 pr-4 font-medium text-right">Quantity</th>
            <th className="pb-3 pr-4 font-medium text-right">Avg Cost</th>
            <th className="pb-3 pr-4 font-medium text-right">Dividends</th>
            <th className="pb-3 font-medium">ISIN</th>
          </tr>
        </thead>
        <tbody>
          {holdings.map((h) => (
            <tr key={`${h.account_id}-${h.security_isin}`} className="border-b border-gray-100">
              <td className="py-3 pr-4">
                <div className="font-medium text-gray-900">{h.name}</div>
                <div className="text-xs text-gray-500">{h.asset_class}{h.symbol ? ` \u00b7 ${h.symbol}` : ''}</div>
              </td>
              <td className="py-3 pr-4 text-right">{fmtQty(h.quantity)}</td>
              <td className="py-3 pr-4 text-right">{fmt(h.avg_cost_basis)}</td>
              <td className="py-3 pr-4 text-right">{fmt(h.total_dividends)}</td>
              <td className="py-3 text-gray-500">{h.security_isin}</td>
            </tr>
          ))}
          {holdings.length === 0 && (
            <tr>
              <td colSpan={5} className="py-8 text-center text-gray-400">
                No holdings yet. Import transactions to see your portfolio.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
