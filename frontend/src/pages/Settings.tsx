import { useState, useEffect, useCallback } from 'react';
import { api, type Account, type Security } from '../api/client';

export default function Settings() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [securities, setSecurities] = useState<Security[]>([]);
  const [loading, setLoading] = useState(true);

  // Create account form
  const [newName, setNewName] = useState('');
  const [newInstitution, setNewInstitution] = useState('sparkasse');
  const [newType, setNewType] = useState('checking');
  const [newIBAN, setNewIBAN] = useState('');
  const [creating, setCreating] = useState(false);

  const loadData = useCallback(async () => {
    try {
      const [accRes, secRes] = await Promise.all([api.listAccounts(), api.listSecurities()]);
      setAccounts(accRes.accounts);
      setSecurities(secRes.securities);
    } catch (e) {
      console.error('Failed to load settings:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  const handleCreateAccount = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName) return;
    setCreating(true);
    try {
      await api.createAccount({
        name: newName,
        institution: newInstitution,
        type: newType,
        iban: newIBAN || undefined,
      });
      setNewName('');
      setNewIBAN('');
      loadData();
    } catch (err) {
      console.error('Create account failed:', err);
    } finally {
      setCreating(false);
    }
  };

  const handleUpdateSymbol = async (isin: string) => {
    const symbol = prompt(`Enter Yahoo Finance ticker for ${isin}:`);
    if (symbol === null) return;
    try {
      await api.updateSecuritySymbol(isin, symbol);
      loadData();
    } catch (err) {
      console.error('Update symbol failed:', err);
    }
  };

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-gray-400">Loading...</div>;
  }

  return (
    <div className="space-y-8">
      {/* Create Account */}
      <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
        <h2 className="text-lg font-semibold text-gray-900 mb-4">Add Account</h2>
        <form onSubmit={handleCreateAccount} className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
          <input
            type="text"
            placeholder="Account name"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
            required
          />
          <select
            value={newInstitution}
            onChange={(e) => setNewInstitution(e.target.value)}
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          >
            <option value="sparkasse">Sparkasse</option>
            <option value="n26">N26</option>
            <option value="scalable_capital">Scalable Capital</option>
          </select>
          <select
            value={newType}
            onChange={(e) => setNewType(e.target.value)}
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          >
            <option value="checking">Checking</option>
            <option value="savings">Savings</option>
            <option value="brokerage">Brokerage</option>
          </select>
          <input
            type="text"
            placeholder="IBAN (optional)"
            value={newIBAN}
            onChange={(e) => setNewIBAN(e.target.value)}
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
          />
          <button
            type="submit"
            disabled={creating}
            className="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {creating ? 'Creating...' : 'Add Account'}
          </button>
        </form>
      </div>

      {/* Accounts List */}
      <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
        <h2 className="text-lg font-semibold text-gray-900 mb-4">Accounts</h2>
        {accounts.length === 0 ? (
          <p className="text-gray-400">No accounts yet. Create one above.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-200 text-left text-gray-500">
                  <th className="pb-3 pr-4 font-medium">Name</th>
                  <th className="pb-3 pr-4 font-medium">Institution</th>
                  <th className="pb-3 pr-4 font-medium">Type</th>
                  <th className="pb-3 pr-4 font-medium">IBAN</th>
                  <th className="pb-3 font-medium">Status</th>
                </tr>
              </thead>
              <tbody>
                {accounts.map((acc) => (
                  <tr key={acc.id} className="border-b border-gray-100">
                    <td className="py-3 pr-4 font-medium text-gray-900">{acc.name}</td>
                    <td className="py-3 pr-4 text-gray-600">{acc.institution}</td>
                    <td className="py-3 pr-4 text-gray-600">{acc.type}</td>
                    <td className="py-3 pr-4 text-gray-500">{acc.iban || '-'}</td>
                    <td className="py-3">
                      <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                        acc.is_active ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-500'
                      }`}>
                        {acc.is_active ? 'Active' : 'Inactive'}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Securities */}
      <div className="rounded-xl bg-white p-6 shadow-sm border border-gray-200">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold text-gray-900">Securities</h2>
          <a
            href="/api/settings/template"
            className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-50"
          >
            Download Holdings Template
          </a>
        </div>
        {securities.length === 0 ? (
          <p className="text-gray-400">No securities yet. Import transactions with ISINs to populate.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-200 text-left text-gray-500">
                  <th className="pb-3 pr-4 font-medium">ISIN</th>
                  <th className="pb-3 pr-4 font-medium">Name</th>
                  <th className="pb-3 pr-4 font-medium">Class</th>
                  <th className="pb-3 pr-4 font-medium">Symbol</th>
                  <th className="pb-3 font-medium">Action</th>
                </tr>
              </thead>
              <tbody>
                {securities.map((sec) => (
                  <tr key={sec.isin} className="border-b border-gray-100">
                    <td className="py-3 pr-4 font-mono text-gray-600">{sec.isin}</td>
                    <td className="py-3 pr-4 text-gray-900">{sec.name}</td>
                    <td className="py-3 pr-4 text-gray-600">{sec.asset_class}</td>
                    <td className="py-3 pr-4">
                      {sec.symbol ? (
                        <span className="font-mono text-gray-700">{sec.symbol}</span>
                      ) : (
                        <span className="text-amber-600 text-xs">Not set</span>
                      )}
                    </td>
                    <td className="py-3">
                      <button
                        onClick={() => handleUpdateSymbol(sec.isin)}
                        className="text-blue-600 hover:text-blue-800 text-xs"
                      >
                        Edit Symbol
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
