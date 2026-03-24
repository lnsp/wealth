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

  const typeStyles: Record<string, string> = {
    buy: 'bg-apple-blue/10 text-apple-blue',
    sell: 'bg-apple-orange/10 text-apple-orange',
    dividend: 'bg-apple-green/10 text-apple-green',
    deposit: 'bg-emerald-50 text-emerald-600',
    withdrawal: 'bg-apple-red/10 text-apple-red',
    interest: 'bg-apple-yellow/10 text-yellow-700',
    fee: 'bg-apple-gray-6 text-apple-gray-1',
    transfer: 'bg-apple-purple/10 text-apple-purple',
    savings_plan: 'bg-apple-indigo/10 text-apple-indigo',
  };

  const pages = Math.ceil(total / limit);
  const currentPage = Math.floor(offset / limit) + 1;

  return (
    <div className="space-y-5">
      <div className="flex items-baseline justify-between">
        <h1 className="text-apple-title1 text-gray-900">Transactions</h1>
        <span className="text-apple-footnote text-apple-gray-1">{total} total</span>
      </div>

      <div className="apple-card overflow-hidden">
        {loading ? (
          <div className="flex items-center justify-center py-20 text-apple-callout text-apple-gray-2">Loading...</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="text-left text-apple-caption1 text-apple-gray-1 uppercase tracking-wider bg-apple-gray-6/50">
                  <th className="px-5 py-2.5 font-medium">Date</th>
                  <th className="px-5 py-2.5 font-medium">Type</th>
                  <th className="px-5 py-2.5 font-medium">Account</th>
                  <th className="px-5 py-2.5 font-medium">Counterparty</th>
                  <th className="px-5 py-2.5 font-medium">Reference</th>
                  <th className="px-5 py-2.5 font-medium text-right">Amount</th>
                </tr>
              </thead>
              <tbody>
                {transactions.map((t, i) => (
                  <tr
                    key={t.id}
                    className={`transition-colors hover:bg-apple-gray-6/40 ${
                      i < transactions.length - 1 ? 'border-b border-apple-gray-5' : ''
                    }`}
                  >
                    <td className="px-5 py-3 whitespace-nowrap text-apple-subhead tabular-nums">{fmtDate(t.date)}</td>
                    <td className="px-5 py-3">
                      <span className={`apple-badge ${typeStyles[t.type] || 'bg-apple-gray-6 text-apple-gray-1'}`}>
                        {t.type}
                      </span>
                    </td>
                    <td className="px-5 py-3 text-apple-subhead text-apple-gray-1">{t.account_name}</td>
                    <td className="px-5 py-3 text-apple-subhead text-apple-gray-1 max-w-[200px] truncate">{t.counterparty || '—'}</td>
                    <td className="px-5 py-3 text-apple-subhead text-apple-gray-1 max-w-[200px] truncate">{t.reference || '—'}</td>
                    <td className={`px-5 py-3 text-right text-apple-subhead font-medium whitespace-nowrap tabular-nums ${
                      t.amount >= 0 ? 'text-gray-900' : 'text-apple-red'
                    }`}>
                      {fmt(t.amount)}
                    </td>
                  </tr>
                ))}
                {transactions.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-5 py-12 text-center text-apple-callout text-apple-gray-2">
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
            className="apple-btn-secondary"
          >
            Previous
          </button>
          <span className="text-apple-footnote text-apple-gray-1">Page {currentPage} of {pages}</span>
          <button
            onClick={() => setOffset(offset + limit)}
            disabled={offset + limit >= total}
            className="apple-btn-secondary"
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}
