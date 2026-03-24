import { useState, useEffect, useCallback } from 'react';
import { api, type TransactionRow } from '../api/client';

export default function Transactions() {
  const [transactions, setTransactions] = useState<TransactionRow[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(true);
  const limit = 50;

  const loadTransactions = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.listTransactions(limit, offset);
      setTransactions(res.transactions);
      setTotal(res.total);
    } catch (e) {
      console.error('Failed to load transactions:', e);
    } finally {
      setLoading(false);
    }
  }, [offset]);

  useEffect(() => { loadTransactions(); }, [loadTransactions]);

  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);
  const fmtDate = (d: string) =>
    new Date(d).toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' });

  const typeColors: Record<string, string> = {
    buy: 'bg-blue-100 text-blue-700',
    sell: 'bg-orange-100 text-orange-700',
    dividend: 'bg-green-100 text-green-700',
    deposit: 'bg-emerald-100 text-emerald-700',
    withdrawal: 'bg-red-100 text-red-700',
    interest: 'bg-yellow-100 text-yellow-700',
    fee: 'bg-gray-100 text-gray-700',
    transfer: 'bg-purple-100 text-purple-700',
    savings_plan: 'bg-indigo-100 text-indigo-700',
  };

  const pages = Math.ceil(total / limit);
  const currentPage = Math.floor(offset / limit) + 1;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-gray-900">
          Transactions <span className="text-sm font-normal text-gray-500">({total} total)</span>
        </h1>
      </div>

      <div className="rounded-xl bg-white shadow-sm border border-gray-200 overflow-hidden">
        {loading ? (
          <div className="flex items-center justify-center py-20 text-gray-400">Loading...</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-200 bg-gray-50 text-left text-gray-500">
                  <th className="px-4 py-3 font-medium">Date</th>
                  <th className="px-4 py-3 font-medium">Type</th>
                  <th className="px-4 py-3 font-medium">Account</th>
                  <th className="px-4 py-3 font-medium">Counterparty</th>
                  <th className="px-4 py-3 font-medium">Reference</th>
                  <th className="px-4 py-3 font-medium text-right">Amount</th>
                </tr>
              </thead>
              <tbody>
                {transactions.map((t) => (
                  <tr key={t.id} className="border-b border-gray-100 hover:bg-gray-50">
                    <td className="px-4 py-3 whitespace-nowrap">{fmtDate(t.date)}</td>
                    <td className="px-4 py-3">
                      <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${typeColors[t.type] || 'bg-gray-100 text-gray-600'}`}>
                        {t.type}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-gray-600">{t.account_name}</td>
                    <td className="px-4 py-3 text-gray-600 max-w-[200px] truncate">{t.counterparty || '-'}</td>
                    <td className="px-4 py-3 text-gray-500 max-w-[200px] truncate">{t.reference || '-'}</td>
                    <td className={`px-4 py-3 text-right font-medium whitespace-nowrap ${
                      t.amount >= 0 ? 'text-gray-900' : 'text-red-600'
                    }`}>
                      {fmt(t.amount)}
                    </td>
                  </tr>
                ))}
                {transactions.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-12 text-center text-gray-400">
                      No transactions yet. Import a CSV to get started.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {pages > 1 && (
        <div className="flex items-center justify-between">
          <button
            onClick={() => setOffset(Math.max(0, offset - limit))}
            disabled={offset === 0}
            className="rounded-lg border border-gray-300 px-4 py-2 text-sm disabled:opacity-50 hover:bg-gray-50"
          >
            Previous
          </button>
          <span className="text-sm text-gray-500">Page {currentPage} of {pages}</span>
          <button
            onClick={() => setOffset(offset + limit)}
            disabled={offset + limit >= total}
            className="rounded-lg border border-gray-300 px-4 py-2 text-sm disabled:opacity-50 hover:bg-gray-50"
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}
