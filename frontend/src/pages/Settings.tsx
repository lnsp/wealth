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
    return <div className="flex items-center justify-center py-20 text-apple-callout text-apple-gray-2">Loading...</div>;
  }

  return (
    <div className="space-y-8">
      <h1 className="text-apple-title1 text-gray-900">Settings</h1>

      {/* Create Account — iOS grouped form style */}
      <div>
        <h2 className="text-apple-footnote text-apple-gray-1 uppercase tracking-wider px-4 mb-2">Add Account</h2>
        <form onSubmit={handleCreateAccount} className="apple-card divide-y divide-apple-gray-5">
          <div className="flex items-center px-4 py-0">
            <label className="w-28 shrink-0 text-apple-body text-gray-900">Name</label>
            <input
              type="text"
              placeholder="Account name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="flex-1 py-3 text-apple-body text-gray-900 placeholder-apple-gray-3 bg-transparent outline-none"
              required
            />
          </div>
          <div className="flex items-center px-4 py-0">
            <label className="w-28 shrink-0 text-apple-body text-gray-900">Institution</label>
            <select
              value={newInstitution}
              onChange={(e) => setNewInstitution(e.target.value)}
              className="flex-1 py-3 text-apple-body text-apple-gray-1 bg-transparent outline-none appearance-none"
            >
              <option value="sparkasse">Sparkasse</option>
              <option value="n26">N26</option>
              <option value="scalable_capital">Scalable Capital</option>
            </select>
          </div>
          <div className="flex items-center px-4 py-0">
            <label className="w-28 shrink-0 text-apple-body text-gray-900">Type</label>
            <select
              value={newType}
              onChange={(e) => setNewType(e.target.value)}
              className="flex-1 py-3 text-apple-body text-apple-gray-1 bg-transparent outline-none appearance-none"
            >
              <option value="checking">Checking</option>
              <option value="savings">Savings</option>
              <option value="brokerage">Brokerage</option>
            </select>
          </div>
          <div className="flex items-center px-4 py-0">
            <label className="w-28 shrink-0 text-apple-body text-gray-900">IBAN</label>
            <input
              type="text"
              placeholder="Optional"
              value={newIBAN}
              onChange={(e) => setNewIBAN(e.target.value)}
              className="flex-1 py-3 text-apple-body text-gray-900 placeholder-apple-gray-3 bg-transparent outline-none"
            />
          </div>
          <div className="px-4 py-3">
            <button
              type="submit"
              disabled={creating}
              className="apple-btn-primary w-full"
            >
              {creating ? 'Creating...' : 'Add Account'}
            </button>
          </div>
        </form>
      </div>

      {/* Accounts List */}
      <div>
        <h2 className="text-apple-footnote text-apple-gray-1 uppercase tracking-wider px-4 mb-2">Accounts</h2>
        {accounts.length === 0 ? (
          <div className="apple-card px-4 py-8 text-center text-apple-callout text-apple-gray-2">
            No accounts yet. Create one above.
          </div>
        ) : (
          <div className="apple-card divide-y divide-apple-gray-5">
            {accounts.map((acc) => (
              <div key={acc.id} className="flex items-center justify-between px-4 py-3">
                <div>
                  <p className="text-apple-body text-gray-900">{acc.name}</p>
                  <p className="text-apple-footnote text-apple-gray-1 mt-0.5">
                    {acc.institution} · {acc.type}
                    {acc.iban ? ` · ${acc.iban}` : ''}
                  </p>
                </div>
                <span className={`apple-badge ${
                  acc.is_active ? 'bg-apple-green/10 text-apple-green' : 'bg-apple-gray-6 text-apple-gray-1'
                }`}>
                  {acc.is_active ? 'Active' : 'Inactive'}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Securities */}
      <div>
        <div className="flex items-center justify-between px-4 mb-2">
          <h2 className="text-apple-footnote text-apple-gray-1 uppercase tracking-wider">Securities</h2>
          <a
            href="/api/settings/template"
            className="text-apple-footnote text-apple-blue hover:text-apple-blue/80 transition-colors"
          >
            Download Template
          </a>
        </div>
        {securities.length === 0 ? (
          <div className="apple-card px-4 py-8 text-center text-apple-callout text-apple-gray-2">
            No securities yet. Import transactions with ISINs to populate.
          </div>
        ) : (
          <div className="apple-card divide-y divide-apple-gray-5">
            {securities.map((sec) => (
              <div key={sec.isin} className="flex items-center justify-between px-4 py-3">
                <div className="min-w-0 flex-1">
                  <p className="text-apple-body text-gray-900">{sec.name}</p>
                  <p className="text-apple-footnote text-apple-gray-1 mt-0.5">
                    <span className="font-mono">{sec.isin}</span>
                    <span className="mx-1.5">·</span>
                    {sec.asset_class}
                    {sec.symbol && (
                      <>
                        <span className="mx-1.5">·</span>
                        <span className="font-mono">{sec.symbol}</span>
                      </>
                    )}
                  </p>
                </div>
                <button
                  onClick={() => handleUpdateSymbol(sec.isin)}
                  className="shrink-0 ml-3 text-apple-subhead text-apple-blue hover:text-apple-blue/80 transition-colors"
                >
                  {sec.symbol ? 'Edit' : 'Set Symbol'}
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
