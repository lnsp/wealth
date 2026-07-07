import { useState, useEffect, useCallback } from 'react';
import { api, type Account, type Security, type WealthReportSummary, type WealthReportDetail, type PasskeyEntry } from '../api/client';
import { QRCodeSVG } from 'qrcode.react';
import { registerPasskey } from '../utils/webauthn';
import { TabBar } from '../components/ui';

type SettingsTab = 'data' | 'accounts' | 'security' | 'notifications';
const SETTINGS_TABS: { id: SettingsTab; label: string }[] = [
  { id: 'data', label: 'Data' },
  { id: 'accounts', label: 'Accounts' },
  { id: 'security', label: 'Security' },
  { id: 'notifications', label: 'Notifications' },
];

interface BuildInfo {
  commit: string;
  built_at: string;
}

interface SettingsProps {
  onLogout?: () => void;
  authEnabled?: boolean;
}

interface JobStatus {
  name: string;
  schedule: string;
  last_run: string | null;
  status: string;
  message: string;
}

interface BackfillJob {
  name: string;
  status: string;
  started_at: string | null;
  completed_at: string | null;
  message: string;
  attempts: number;
}

export default function Settings({ onLogout, authEnabled }: SettingsProps = {}) {
  const [activeTab, setActiveTab] = useState<SettingsTab>(() => (localStorage.getItem('settings_tab') as SettingsTab) || 'data');
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [securities, setSecurities] = useState<Security[]>([]);
  const [jobs, setJobs] = useState<JobStatus[]>([]);
  const [backfillJobs, setBackfillJobs] = useState<BackfillJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [dataQuality, setDataQuality] = useState<{ issues: { type: string; severity: string; title: string; detail: string; isin?: string }[]; count: number; errors: number; warnings: number; info: number } | null>(null);
  const [channels, setChannels] = useState<{ id: string; type: string; name: string; config: Record<string, string>; enabled: boolean; channel_for: string; digest_frequency: string }[]>([]);

  // Create account form
  const [newName, setNewName] = useState('');
  const [newInstitution, setNewInstitution] = useState('sparkasse');
  const [newType, setNewType] = useState('checking');
  const [newIBAN, setNewIBAN] = useState('');
  const [newTaxTreatment, setNewTaxTreatment] = useState('taxable');
  const [newEmployerMatch, setNewEmployerMatch] = useState('');
  const [newSecurityISIN, setNewSecurityISIN] = useState('');
  const [creating, setCreating] = useState(false);

  // Refresh button states
  const [refreshing, setRefreshing] = useState<Record<string, 'idle' | 'loading' | 'ok' | 'error'>>({});


  // Users
  const [users, setUsers] = useState<{ id: string; username: string; role: string; is_active: boolean; totp_enabled?: boolean }[]>([]);
  const [totpSetup, setTotpSetup] = useState<{ userId: string; url: string; secret: string } | null>(null);
  const [totpCode, setTotpCode] = useState('');
  const [totpError, setTotpError] = useState('');
  const [passkeys, setPasskeys] = useState<PasskeyEntry[]>([]);
  const [passkeyName, setPasskeyName] = useState('');
  const [passkeyRegistering, setPasskeyRegistering] = useState(false);
  const [passkeyError, setPasskeyError] = useState('');
  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newRole, setNewRole] = useState('member');


  // Build info
  const [buildInfo, setBuildInfo] = useState<BuildInfo | null>(null);

  // Reports
  const [reports, setReports] = useState<WealthReportSummary[]>([]);
  const [selectedReport, setSelectedReport] = useState<WealthReportDetail | null>(null);
  const [generating, setGenerating] = useState(false);
  const [showAllReports, setShowAllReports] = useState(false);

  const loadData = useCallback(async () => {
    try {
      const [accRes, secRes, schedRes] = await Promise.all([
        api.listAllAccounts(),
        api.listSecurities(),
        fetch('/api/settings/scheduler-status').then(r => r.json()).catch(() => ({ jobs: [] })),
      ]);
      setAccounts(accRes.accounts || []);
      setSecurities(secRes.securities || []);
      setJobs(schedRes.jobs || []);
      setBackfillJobs(schedRes.backfill || []);
      api.listReports().then(r => setReports(r.reports || [])).catch(() => {});
      api.listUsers().then(r => setUsers(r.users || [])).catch(() => {});
      api.listPasskeys().then(r => setPasskeys(r.passkeys || [])).catch(() => {});
      fetch('/api/analysis/data-quality').then(r => r.json()).then(setDataQuality).catch(() => {});
      fetch('/api/settings/channels').then(r => r.json()).then(d => setChannels(d.channels || [])).catch(() => {});
      fetch('/api/settings/build-info').then(r => r.json()).then(setBuildInfo).catch(() => {});
    } catch (e) {
      console.error('Failed to load settings:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  // Auto-refresh while any backfill job is still running
  useEffect(() => {
    const hasRunning = backfillJobs.some(j => j.status === 'running' || j.status === 'pending');
    if (!hasRunning) return;
    const id = setInterval(() => {
      fetch('/api/settings/scheduler-status').then(r => r.json()).then(res => {
        setJobs(res.jobs || []);
        setBackfillJobs(res.backfill || []);
      }).catch(() => {});
    }, 10_000);
    return () => clearInterval(id);
  }, [backfillJobs]);

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
        tax_treatment: newTaxTreatment,
        employer_match_pct: newEmployerMatch ? parseFloat(newEmployerMatch) : undefined,
        import_security_isin: newSecurityISIN || undefined,
      });
      setNewName('');
      setNewIBAN('');
      setNewTaxTreatment('taxable');
      setNewEmployerMatch('');
      setNewSecurityISIN('');
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

  const [editingAccountId, setEditingAccountId] = useState<string | null>(null);
  const [editTax, setEditTax] = useState('taxable');
  const [editMatch, setEditMatch] = useState('');
  const [editCurrency, setEditCurrency] = useState('EUR');

  const handleEditAccount = (acc: Account) => {
    if (editingAccountId === acc.id) {
      setEditingAccountId(null);
      return;
    }
    setEditingAccountId(acc.id);
    setEditTax(acc.tax_treatment || 'taxable');
    setEditMatch(acc.employer_match_pct != null ? String(acc.employer_match_pct) : '');
    setEditCurrency(acc.currency || 'EUR');
  };

  const handleSaveAccountTax = async (acc: Account) => {
    try {
      await api.updateAccount(acc.id, {
        tax_treatment: editTax,
        employer_match_pct: editMatch ? parseFloat(editMatch) : undefined,
        currency: editCurrency !== acc.currency ? editCurrency : undefined,
      });
      setEditingAccountId(null);
      loadData();
    } catch (err) {
      console.error('Update account tax failed:', err);
    }
  };

  const handleRenameAccount = async (acc: Account) => {
    const newName = prompt('Rename account:', acc.name);
    if (newName === null || newName === acc.name) return;
    if (!newName.trim()) return;
    try {
      await api.updateAccount(acc.id, { name: newName.trim() });
      loadData();
    } catch (err) {
      console.error('Rename account failed:', err);
    }
  };

  const handleDeleteAccount = async (acc: Account) => {
    if (!confirm(`Delete "${acc.name}" and all its transactions? This cannot be undone.`)) return;
    try {
      await api.deleteAccount(acc.id);
      loadData();
    } catch (err) {
      console.error('Delete account failed:', err);
    }
  };

  const handleToggleActive = async (acc: Account) => {
    try {
      await api.updateAccount(acc.id, { is_active: !acc.is_active });
      loadData();
    } catch (err) {
      console.error('Toggle account failed:', err);
    }
  };

  const handleRefresh = async (endpoint: string) => {
    setRefreshing(prev => ({ ...prev, [endpoint]: 'loading' }));
    try {
      const resp = await fetch(`/api/settings/${endpoint}`, { method: 'POST' });
      if (!resp.ok) throw new Error('Request failed');
      setRefreshing(prev => ({ ...prev, [endpoint]: 'ok' }));
      // Reload scheduler status after background job has time to complete
      setTimeout(loadData, 3000);
      // Reset status indicator after a few seconds
      setTimeout(() => setRefreshing(prev => ({ ...prev, [endpoint]: 'idle' })), 5000);
    } catch {
      setRefreshing(prev => ({ ...prev, [endpoint]: 'error' }));
      setTimeout(() => setRefreshing(prev => ({ ...prev, [endpoint]: 'idle' })), 5000);
    }
  };

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-[16px] text-ink-muted">Loading...</div>;
  }

  const statusColors: Record<string, string> = {
    ok: 'bg-parchment-deep border border-sage text-sage',
    error: 'bg-parchment-deep border border-claret text-claret',
    running: 'bg-parchment-deep border border-forest text-forest',
    never: 'bg-parchment-deep text-ink-muted',
  };

  const fmtTime = (d: string | null) => {
    if (!d) return 'Never';
    return new Date(d).toLocaleString('de-DE', {
      day: '2-digit', month: '2-digit', year: 'numeric',
      hour: '2-digit', minute: '2-digit',
    });
  };

  return (
    <div>
      {/* Pill-nav replaces the prior 4-drawer accordion. Previous layout
          forced the user to scroll ~6 screens to reach later sections;
          tabs give each section its own viewport. */}
      <h1 className="font-serif text-title text-ink mb-3">Settings</h1>
      <TabBar
        tabs={SETTINGS_TABS}
        activeTab={activeTab}
        onTabChange={(t) => { setActiveTab(t); localStorage.setItem('settings_tab', t); }}
      />

      {activeTab === 'data' && (
        <div className="space-y-6 pb-4 pt-2">
      {/* Scheduler Status */}
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Scheduler</h2>
        <div className="border-t border-divider pt-6 py-3 md:py-5 divide-y divide-divider">
          {jobs.map((job) => (
            <div key={job.name} className="flex items-center justify-between py-3">
              <div className="min-w-0 flex-1">
                <p className="text-[16px] text-ink">{job.name}</p>
                <p className="text-[13px] text-ink-muted mt-0.5">
                  {job.schedule}
                  {job.last_run && <> · Last: {fmtTime(job.last_run)}</>}
                  {job.message && <> · <span data-privacy="blur">{job.message}</span></>}
                </p>
              </div>
              <span className={`apple-badge ${statusColors[job.status] || statusColors.never}`}>
                {job.status}
              </span>
            </div>
          ))}
          <div className="py-3 flex flex-wrap gap-2">
            {([
              { endpoint: 'refresh-prices', label: 'Refresh Prices' },
              { endpoint: 'refresh-etf-metadata', label: 'Refresh ETF Data' },
              { endpoint: 'refresh-historical-prices', label: 'Refresh Historical Prices' },
              { endpoint: 'rebuild-networth', label: 'Rebuild Net Worth History' },
              { endpoint: 'check-alerts', label: 'Check Price Alerts' },
            ] as const).map(({ endpoint, label }) => {
              const state = refreshing[endpoint] || 'idle';
              return (
                <button
                  key={endpoint}
                  onClick={() => handleRefresh(endpoint)}
                  disabled={state === 'loading'}
                  className={`apple-btn-secondary text-[12px] transition-all duration-200 ${
                    state === 'loading' ? 'opacity-60' :
                    state === 'ok' ? 'border-sage text-sage' :
                    state === 'error' ? 'border-claret text-claret' : ''
                  }`}
                >
                  {state === 'loading' ? 'Running...' :
                   state === 'ok' ? 'Started' :
                   state === 'error' ? 'Failed' : label}
                </button>
              );
            })}
          </div>
        </div>
      </div>

      {/* Backfill Progress */}
      {backfillJobs.length > 0 && (
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Initial Data Backfill</h2>
        <div className="border-t border-divider pt-6 py-3 md:py-5 divide-y divide-divider">
          {backfillJobs.map((job) => {
            const label: Record<string, string> = {
              historical_prices: 'Historical Prices',
              historical_fx: 'FX Rates (ECB)',
              historical_networth: 'Net Worth Snapshots',
            };
            const isRunning = job.status === 'running';
            const isDone = job.status === 'completed';
            const isFailed = job.status === 'failed';
            return (
              <div key={job.name} className="flex items-center justify-between py-3">
                <div className="min-w-0 flex-1">
                  <p className="text-[16px] text-ink">{label[job.name] || job.name}</p>
                  <p className="text-[13px] text-ink-muted mt-0.5">
                    {isDone && job.completed_at && <>Completed {fmtTime(job.completed_at)}</>}
                    {isRunning && job.started_at && <>Started {fmtTime(job.started_at)}</>}
                    {isFailed && <>Failed{job.attempts > 1 ? ` (${job.attempts} attempts)` : ''}{job.message ? ` · ${job.message}` : ''}</>}
                    {job.status === 'pending' && 'Waiting to start'}
                  </p>
                </div>
                <span className={`apple-badge ${
                  isDone ? 'bg-parchment-deep border border-sage text-sage' :
                  isRunning ? 'bg-parchment-deep border border-forest text-forest animate-pulse' :
                  isFailed ? 'bg-parchment-deep border border-claret text-claret' :
                  'bg-parchment-deep text-ink-muted'
                }`}>
                  {job.status}
                </span>
              </div>
            );
          })}
        </div>
      </div>
      )}

      {/* Data Export */}
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Data Export</h2>
        <div className="border-t border-divider pt-6 py-3 md:py-5 flex items-center justify-between">
          <div>
            <p className="text-[16px] text-ink">Export Transactions</p>
            <p className="text-[13px] text-ink-muted mt-0.5">Download all transactions as CSV</p>
          </div>
          <a
            href="/api/settings/export-transactions"
            className="text-[15px] text-forest hover:text-forest-light transition-colors"
          >
            Download
          </a>
        </div>
      </div>

        </div>
      )}

      {activeTab === 'accounts' && (
        <div className="space-y-6 pb-4 pt-2">
      {/* Create Account */}
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Add Account</h2>
        <form onSubmit={handleCreateAccount} className="border-t border-divider pt-6 py-3 md:py-5 divide-y divide-divider">
          <div className="flex items-center py-0">
            <label className="w-28 shrink-0 text-[16px] text-ink">Name</label>
            <input
              type="text"
              placeholder="Account name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="flex-1 py-3 text-[16px] text-ink placeholder-ink-muted/40 bg-transparent outline-none"
              required
            />
          </div>
          <div className="flex items-center py-0">
            <label className="w-28 shrink-0 text-[16px] text-ink">Institution</label>
            <select
              value={newInstitution}
              onChange={(e) => setNewInstitution(e.target.value)}
              className="flex-1 py-3 text-[16px] text-ink-muted bg-transparent outline-none appearance-none"
            >
              <option value="sparkasse">Sparkasse</option>
              <option value="n26">N26</option>
              <option value="revolut">Revolut</option>
              <option value="scalable_capital">Scalable Capital</option>
              <option value="trade_republic">Trade Republic</option>
              <option value="ing">ING</option>
              <option value="dkb">DKB</option>
              <option value="comdirect">comdirect</option>
              <option value="morgan_stanley">Morgan Stanley</option>
              <option value="delta">Delta</option>
              <option value="manual">Manual Entry</option>
              <option value="other">Other</option>
            </select>
          </div>
          <div className="flex items-center py-0">
            <label className="w-28 shrink-0 text-[16px] text-ink">Type</label>
            <select
              value={newType}
              onChange={(e) => setNewType(e.target.value)}
              className="flex-1 py-3 text-[16px] text-ink-muted bg-transparent outline-none appearance-none"
            >
              <option value="checking">Checking</option>
              <option value="savings">Savings</option>
              <option value="brokerage">Brokerage</option>
              <option value="credit">Credit Card</option>
              <option value="real_estate">Real Estate</option>
              <option value="pension">Pension</option>
              <option value="precious_metals">Precious Metals</option>
              <option value="liability">Liability</option>
            </select>
          </div>
          <div className="flex items-center py-0">
            <label className="w-28 shrink-0 text-[16px] text-ink">IBAN</label>
            <input
              type="text"
              placeholder="Optional"
              value={newIBAN}
              onChange={(e) => setNewIBAN(e.target.value)}
              className="flex-1 py-3 text-[16px] text-ink placeholder-ink-muted/40 bg-transparent outline-none"
            />
          </div>
          <div className="flex items-center py-0">
            <label className="w-28 shrink-0 text-[16px] text-ink">Tax Treatment</label>
            <select
              value={newTaxTreatment}
              onChange={(e) => setNewTaxTreatment(e.target.value)}
              className="flex-1 py-3 text-[16px] text-ink-muted bg-transparent outline-none appearance-none"
            >
              <option value="taxable">Taxable (Abgeltungssteuer)</option>
              <option value="bav">bAV (Betriebliche Altersvorsorge)</option>
              <option value="riester">Riester</option>
              <option value="rurup">Rürup (Basisrente)</option>
              <option value="savings">Tax-Free Savings</option>
            </select>
          </div>
          {(newTaxTreatment === 'bav') && (
            <div className="flex items-center py-0">
              <label className="w-28 shrink-0 text-[16px] text-ink">Employer Match</label>
              <input
                type="number"
                step="0.1"
                min="0"
                max="100"
                placeholder="e.g. 50%"
                value={newEmployerMatch}
                onChange={(e) => setNewEmployerMatch(e.target.value)}
                className="flex-1 py-3 text-[16px] text-ink placeholder-ink-muted/40 bg-transparent outline-none"
              />
              <span className="text-[14px] text-ink-muted ml-1">%</span>
            </div>
          )}
          {newInstitution === 'morgan_stanley' && (
            <div className="flex items-center py-0">
              <label className="w-28 shrink-0 text-[16px] text-ink">Stock ISIN</label>
              <input
                type="text"
                placeholder="e.g. US5949181045"
                value={newSecurityISIN}
                onChange={(e) => setNewSecurityISIN(e.target.value)}
                className="flex-1 py-3 text-[16px] text-ink placeholder-ink-muted/40 bg-transparent outline-none"
              />
            </div>
          )}
          <div className="py-3">
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

      {/* Data Quality */}
      {dataQuality && dataQuality.count > 0 && (
        <div>
          <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Data Quality</h2>
          <div className="border-t border-divider pt-6 py-3 md:py-5 divide-y divide-divider">
            <div className="py-3 flex items-center gap-3">
              {dataQuality.errors > 0 && <span className="apple-badge bg-parchment-deep border border-claret text-claret">{dataQuality.errors} errors</span>}
              {dataQuality.warnings > 0 && <span className="apple-badge bg-parchment-deep border border-amber text-amber">{dataQuality.warnings} warnings</span>}
              {dataQuality.info > 0 && <span className="apple-badge bg-parchment-deep border border-forest text-forest">{dataQuality.info} info</span>}
            </div>
            {dataQuality.issues.map((iss, i) => (
              <div key={i} className="py-3">
                <div className="flex items-start gap-2">
                  <span className={`mt-0.5 w-2 h-2 rounded-full shrink-0 ${iss.severity === 'error' ? 'bg-claret' : iss.severity === 'warning' ? 'bg-amber' : 'bg-forest'}`} />
                  <div className="min-w-0 flex-1">
                    <p className="text-[16px] text-ink">{iss.title}</p>
                    <p className="text-[13px] text-ink-muted mt-0.5">{iss.detail}</p>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Accounts List */}
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Accounts</h2>
        {accounts.length === 0 ? (
          <div className="border-t border-divider pt-6 py-3 md:py-5 py-8 text-center text-[16px] text-ink-muted">
            No accounts yet. Create one above.
          </div>
        ) : (
          <div className="border-t border-divider pt-6 py-3 md:py-5 divide-y divide-divider">
            {accounts.map((acc) => (
              <div key={acc.id}>
                <div className="flex items-center justify-between py-3">
                  <div className="min-w-0 flex-1">
                    <p className="text-[16px] text-ink">{acc.name}</p>
                    <p className="text-[13px] text-ink-muted mt-0.5">
                      {acc.institution} · {acc.type}
                      {acc.iban ? ` · ${acc.iban}` : ''}
                      {acc.tax_treatment && acc.tax_treatment !== 'taxable' && (
                        <span className="ml-1 text-gold">{
                          ({ bav: 'bAV', riester: 'Riester', rurup: 'Rürup', savings: 'Tax-Free' } as Record<string,string>)[acc.tax_treatment] || acc.tax_treatment
                        }</span>
                      )}
                      {acc.employer_match_pct != null && acc.employer_match_pct > 0 && (
                        <span className="ml-1 text-sage">{acc.employer_match_pct}% match</span>
                      )}
                    </p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <button onClick={() => handleRenameAccount(acc)} className="text-[15px] text-forest hover:text-forest-light transition-colors">Rename</button>
                    <button onClick={() => handleEditAccount(acc)} className="text-[15px] text-forest hover:text-forest-light transition-colors">{editingAccountId === acc.id ? 'Close' : 'Edit'}</button>
                    <button onClick={() => handleDeleteAccount(acc)} className="text-[15px] text-claret hover:underline transition-colors">Delete</button>
                    <button
                      onClick={() => handleToggleActive(acc)}
                      className={`apple-badge cursor-pointer transition-colors ${
                        acc.is_active ? 'bg-parchment-deep border border-sage text-sage hover:bg-inset' : 'bg-parchment-deep text-ink-muted hover:bg-divider'
                      }`}
                      title={acc.is_active ? 'Click to deactivate' : 'Click to reactivate'}
                      aria-label={`${acc.is_active ? 'Deactivate' : 'Activate'} ${acc.name}`}
                    >
                      {acc.is_active ? 'Active' : 'Inactive'}
                    </button>
                  </div>
                </div>
                {editingAccountId === acc.id && (
                  <div className="pb-3 space-y-2">
                    <div className="flex items-center gap-3">
                      <label className="w-28 shrink-0 text-[13px] text-ink-muted">Tax Treatment</label>
                      <select value={editTax} onChange={e => setEditTax(e.target.value)} className="flex-1 py-1.5 px-2 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink">
                        <option value="taxable">Taxable (Abgeltungssteuer)</option>
                        <option value="bav">bAV</option>
                        <option value="riester">Riester</option>
                        <option value="rurup">Rürup</option>
                        <option value="savings">Tax-Free Savings</option>
                      </select>
                    </div>
                    {editTax === 'bav' && (
                      <div className="flex items-center gap-3">
                        <label className="w-28 shrink-0 text-[13px] text-ink-muted">Employer Match</label>
                        <input type="number" step="0.1" min="0" max="100" value={editMatch} onChange={e => setEditMatch(e.target.value)} placeholder="e.g. 50" className="flex-1 py-1.5 px-2 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink" />
                        <span className="text-[13px] text-ink-muted">%</span>
                      </div>
                    )}
                    <div className="flex items-center gap-3">
                      <label className="w-28 shrink-0 text-[13px] text-ink-muted">Currency</label>
                      <select value={editCurrency} onChange={e => setEditCurrency(e.target.value)} className="flex-1 py-1.5 px-2 text-[15px] rounded-lg border border-divider bg-parchment-deep text-ink">
                        <option value="EUR">EUR — Euro</option>
                        <option value="USD">USD — US Dollar</option>
                        <option value="GBP">GBP — Pound Sterling</option>
                        <option value="CHF">CHF — Swiss Franc</option>
                      </select>
                    </div>
                    <button onClick={() => handleSaveAccountTax(acc)} className="apple-btn-primary text-[13px] px-4 py-1.5">Save</button>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Securities */}
      <div>
        <div className="flex items-center justify-between mb-2">
          <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em]">Securities</h2>
          <a
            href="/api/settings/template"
            className="text-[13px] text-forest hover:text-forest-light transition-colors"
          >
            Download Template
          </a>
        </div>
        {securities.length === 0 ? (
          <div className="border-t border-divider pt-6 py-3 md:py-5 py-8 text-center text-[16px] text-ink-muted">
            No securities yet. Import transactions with ISINs to populate.
          </div>
        ) : (
          <div className="border-t border-divider pt-6 py-3 md:py-5 divide-y divide-divider">
            {securities.map((sec) => (
              <div key={sec.isin} className="flex items-center justify-between py-3">
                <div className="min-w-0 flex-1">
                  <p className="text-[16px] text-ink">{sec.name}</p>
                  <p className="text-[13px] text-ink-muted mt-0.5">
                    <span className="font-mono">{sec.isin}</span>
                    {sec.wkn && (
                      <>
                        <span className="mx-1.5">·</span>
                        <span className="font-mono">{sec.wkn}</span>
                      </>
                    )}
                    <span className="mx-1.5">·</span>
                    {sec.asset_class}
                    {sec.symbol && (
                      <>
                        <span className="mx-1.5">·</span>
                        <span className="font-mono">{sec.symbol}</span>
                      </>
                    )}
                    {sec.ter != null && sec.ter > 0 && (
                      <>
                        <span className="mx-1.5">·</span>
                        <span>TER {sec.ter.toFixed(2)}%</span>
                      </>
                    )}
                  </p>
                </div>
                <button
                  onClick={() => handleUpdateSymbol(sec.isin)}
                  className="shrink-0 ml-3 text-[15px] text-forest hover:text-forest-light transition-colors"
                >
                  {sec.symbol ? 'Edit' : 'Set Symbol'}
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

        </div>
      )}

      {activeTab === 'security' && (
        <div className="space-y-6 pb-4 pt-2">
      {/* User Management */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Users</h2>
        {users.length > 0 && (
          <div className="space-y-2 mb-4">
            {users.map(u => (
              <div key={u.id} className={`flex items-center justify-between rounded-xl px-3 py-2.5 ${u.is_active ? 'bg-parchment-deep' : 'bg-parchment-deep opacity-60'}`}>
                <div>
                  <p className="text-[15px] font-medium text-ink">{u.username}</p>
                  <p className="text-[12px] text-ink-muted">
                    {u.role} {!u.is_active && '(disabled)'}
                    {u.totp_enabled && <span className="ml-1 text-sage">2FA</span>}
                  </p>
                </div>
                <div className="flex gap-2 shrink-0 ml-2">
                  <button onClick={() => api.toggleUser(u.id).then(() => api.listUsers().then(r => setUsers(r.users || []))).catch(console.error)}
                    className={`text-[12px] font-medium ${u.is_active ? 'text-amber' : 'text-sage'}`}>
                    {u.is_active ? 'Disable' : 'Enable'}
                  </button>
                  <button onClick={() => api.deleteUser(u.id).then(() => setUsers(prev => prev.filter(x => x.id !== u.id))).catch(console.error)}
                    className="text-claret text-[12px] font-medium">Delete</button>
                </div>
              </div>
            ))}
          </div>
        )}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-2 px-1 md:px-0">
          <input aria-label="Username" type="text" placeholder="Username" value={newUsername} onChange={e => setNewUsername(e.target.value)}
            className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px]" />
          <input aria-label="Password" type="password" placeholder="Password (min 8)" value={newPassword} onChange={e => setNewPassword(e.target.value)}
            className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px]" />
          <select aria-label="User role" value={newRole} onChange={e => setNewRole(e.target.value)}
            className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px]">
            <option value="member">Member</option>
            <option value="admin">Admin</option>
          </select>
          <button onClick={() => {
            if (!newUsername || !newPassword) return;
            api.createUser({ username: newUsername, password: newPassword, role: newRole })
              .then(() => { setNewUsername(''); setNewPassword(''); api.listUsers().then(r => setUsers(r.users || [])); })
              .catch(console.error);
          }} disabled={!newUsername || newPassword.length < 8}
            className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-4 py-2 text-[16px] font-medium disabled:opacity-40">
            Add User
          </button>
        </div>
      </div>

      {/* Two-Factor Authentication */}
      {users.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Two-Factor Authentication</h2>
          <div className="space-y-3 px-1 md:px-0">
            {users.map(u => (
              <div key={u.id} className="flex items-center justify-between">
                <div>
                  <span className="text-[15px] font-medium text-ink">{u.username}</span>
                  <span className={`ml-2 text-[12px] font-medium ${u.totp_enabled ? 'text-sage' : 'text-ink-muted'}`}>
                    {u.totp_enabled ? '2FA Enabled' : '2FA Off'}
                  </span>
                </div>
                {u.totp_enabled ? (
                  <button
                    onClick={() => api.disableTOTP(u.id).then(() => api.listUsers().then(r => setUsers(r.users || []))).catch(console.error)}
                    className="text-claret text-[12px] font-medium"
                  >Disable 2FA</button>
                ) : (
                  <button
                    onClick={() => {
                      setTotpError('');
                      api.setupTOTP(u.id).then(r => setTotpSetup({ userId: u.id, url: r.url, secret: r.secret })).catch(console.error);
                    }}
                    className="text-forest text-[12px] font-medium"
                  >Enable 2FA</button>
                )}
              </div>
            ))}
          </div>

          {/* TOTP Setup Flow */}
          {totpSetup && (
            <div className="mt-4 p-4 rounded-xl bg-parchment-deep">
              <p className="text-[15px] font-medium text-ink mb-2">Scan QR Code</p>
              <p className="text-[12px] text-ink-muted mb-3">
                Scan with your authenticator app (Google Authenticator, Authy, etc.)
              </p>
              <div className="flex flex-col items-center gap-3 mb-3">
                <QRCodeSVG value={totpSetup.url} size={192} className="rounded-lg" />
                <p className="text-[11px] text-ink-muted font-mono break-all text-center max-w-xs">
                  Secret: {totpSetup.secret}
                </p>
              </div>
              <div className="flex gap-2">
                <input
                  type="text"
                  placeholder="Enter 6-digit code"
                  value={totpCode}
                  onChange={e => setTotpCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px] text-center tracking-widest tabular-nums"
                />
                <button
                  onClick={() => {
                    setTotpError('');
                    api.verifyTOTP(totpSetup.userId, totpCode)
                      .then(() => {
                        setTotpSetup(null);
                        setTotpCode('');
                        api.listUsers().then(r => setUsers(r.users || []));
                      })
                      .catch(e => setTotpError(e.message || 'Invalid code'));
                  }}
                  disabled={totpCode.length !== 6}
                  className="rounded-[8px] bg-sage text-white dark:text-parchment-deep px-4 py-2 text-[16px] font-medium disabled:opacity-40"
                >Verify</button>
                <button
                  onClick={() => { setTotpSetup(null); setTotpCode(''); setTotpError(''); }}
                  className="rounded-[8px] bg-divider text-ink-body px-4 py-2 text-[16px] font-medium"
                >Cancel</button>
              </div>
              {totpError && <p className="text-claret text-[12px] mt-2">{totpError}</p>}
            </div>
          )}

          {/* Passkeys */}
          <div className="mt-6 pt-4 border-t border-divider">
            <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Passkeys</h2>

            {!window.PublicKeyCredential ? (
              <p className="text-[13px] text-ink-muted px-1 md:px-0">
                Passkeys require HTTPS. Access the app via a secure connection to register passkeys.
              </p>
            ) : (
              <>
                {passkeys.length > 0 && (
                  <div className="space-y-2 mb-3 px-1 md:px-0">
                    {passkeys.map(pk => (
                      <div key={pk.id} className="flex items-center justify-between">
                        <div>
                          <span className="text-[15px] font-medium text-ink">{pk.name}</span>
                          <span className="ml-2 text-[12px] text-ink-muted">
                            {new Date(pk.created_at).toLocaleDateString('de-DE')}
                          </span>
                        </div>
                        <button
                          onClick={() => api.deletePasskey(pk.id).then(() => api.listPasskeys().then(r => setPasskeys(r.passkeys || []))).catch(console.error)}
                          className="text-claret text-[12px] font-medium"
                        >Remove</button>
                      </div>
                    ))}
                  </div>
                )}

                <div className="flex flex-wrap gap-2 px-1 md:px-0">
                  <input
                    type="text"
                    placeholder="Passkey name (optional)"
                    value={passkeyName}
                    onChange={e => setPasskeyName(e.target.value)}
                    className="flex-1 min-w-0 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[15px]"
                  />
                  <button
                    onClick={async () => {
                      setPasskeyError('');
                      setPasskeyRegistering(true);
                      try {
                        await registerPasskey(passkeyName || undefined);
                        setPasskeyName('');
                        const r = await api.listPasskeys();
                        setPasskeys(r.passkeys || []);
                      } catch (e) {
                        setPasskeyError(e instanceof Error ? e.message : 'Registration failed');
                      } finally {
                        setPasskeyRegistering(false);
                      }
                    }}
                    disabled={passkeyRegistering}
                    className="rounded-[8px] bg-sage text-white dark:text-parchment-deep px-4 py-2 text-[15px] font-medium disabled:opacity-40"
                  >{passkeyRegistering ? 'Registering...' : 'Add Passkey'}</button>
                </div>
                {passkeyError && <p className="text-claret text-[12px] mt-2 px-1 md:px-0">{passkeyError}</p>}
              </>
            )}
          </div>
        </div>
      )}

        </div>
      )}

      {activeTab === 'notifications' && (
        <div className="space-y-6 pb-4 pt-2">
      {/* Wealth Reports */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <div className="flex items-center justify-between mb-3 md:mb-4 px-1 md:px-0">
          <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Wealth Reports</h2>
          <button
            onClick={() => {
              const now = new Date();
              const prevMonth = new Date(now.getFullYear(), now.getMonth() - 1, 1);
              setGenerating(true);
              api.generateReport({
                report_type: 'monthly',
                year: prevMonth.getFullYear(),
                month: prevMonth.getMonth() + 1,
              }).then(() => api.listReports().then(r => setReports(r.reports || [])))
                .catch(console.error)
                .finally(() => setGenerating(false));
            }}
            disabled={generating}
            className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-3 py-1.5 text-[13px] font-medium disabled:opacity-40"
          >
            {generating ? 'Generating...' : 'Generate Last Month'}
          </button>
        </div>

        {reports.length > 0 ? (
          <div className="space-y-2 px-1 md:px-0">
            {(showAllReports ? reports : reports.slice(0, 5)).map(rpt => (
              <div key={rpt.id} className="flex items-center justify-between rounded-xl bg-parchment-deep px-3 py-2.5">
                <div>
                  <p className="text-[15px] font-medium text-ink">
                    {rpt.period_label} <span className="text-[12px] text-ink-muted ml-1">{rpt.report_type}</span>
                  </p>
                  <p className="text-[11px] text-ink-muted">
                    Generated {new Date(rpt.generated_at).toLocaleDateString('de-DE')}
                  </p>
                </div>
                <div className="flex gap-2">
                  <button onClick={() => api.getReport(rpt.id).then(setSelectedReport).catch(console.error)}
                    className="text-forest text-[12px] font-medium">View</button>
                  <a href={`/api/settings/reports/${rpt.id}/pdf`} download
                    className="text-sage text-[12px] font-medium">PDF</a>
                </div>
              </div>
            ))}
            {reports.length > 5 && (
              <button
                onClick={() => setShowAllReports(prev => !prev)}
                className="w-full mt-1 py-2 text-[13px] text-forest font-medium"
              >
                {showAllReports ? 'Show less' : `Show all ${reports.length} reports`}
              </button>
            )}
          </div>
        ) : (
          <p className="text-[13px] text-ink-muted px-1 md:px-0">
            No reports generated yet. Click "Generate Last Month" to create your first wealth report.
          </p>
        )}

        {/* Report detail modal */}
        {selectedReport && (
          <div className="mt-4 pt-4 border-t border-divider px-1 md:px-0">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-[15px] font-semibold text-ink">
                Report: {selectedReport.period_label}
              </h3>
              <button onClick={() => setSelectedReport(null)} className="text-ink-muted text-[12px]">Close</button>
            </div>

            <div className="grid grid-cols-2 md:grid-cols-4 gap-2 mb-3">
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">Start</p>
                <p className="text-[13px] font-semibold tabular-nums">
                  {new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(selectedReport.data.net_worth_start)}
                </p>
              </div>
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">End</p>
                <p className="text-[13px] font-semibold tabular-nums">
                  {new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(selectedReport.data.net_worth_end)}
                </p>
              </div>
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">Change</p>
                <p className={`text-[13px] font-semibold tabular-nums ${selectedReport.data.net_worth_change >= 0 ? 'text-sage' : 'text-claret'}`}>
                  {selectedReport.data.net_worth_change >= 0 ? '+' : ''}{new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(selectedReport.data.net_worth_change)}
                  {' '}({selectedReport.data.net_worth_change_pct >= 0 ? '+' : ''}{selectedReport.data.net_worth_change_pct}%)
                </p>
              </div>
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">Dividends</p>
                <p className="text-[13px] font-semibold tabular-nums text-sage">
                  {new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(selectedReport.data.total_dividends)}
                </p>
              </div>
            </div>

            {selectedReport.data.top_gainer && (
              <p className="text-[12px] text-ink-muted px-1">
                Top gainer: <span className="text-sage font-medium">{selectedReport.data.top_gainer.name} ({selectedReport.data.top_gainer.return_pct > 0 ? '+' : ''}{selectedReport.data.top_gainer.return_pct}%)</span>
              </p>
            )}
            {selectedReport.data.top_loser && (
              <p className="text-[12px] text-ink-muted px-1">
                Top loser: <span className="text-claret font-medium">{selectedReport.data.top_loser.name} ({selectedReport.data.top_loser.return_pct}%)</span>
              </p>
            )}
            <p className="text-[12px] text-ink-muted mt-1 px-1">
              {selectedReport.data.new_transactions} transactions in period
            </p>
          </div>
        )}
      </div>

      {/* Notification Channels */}
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Notification Channels</h2>
        <div className="border-t border-divider pt-6 py-3 md:py-5 divide-y divide-divider">
          {channels.map(ch => (
            <div key={ch.id} className="flex items-center justify-between py-3">
              <div>
                <p className="text-[16px] text-ink">{ch.name}</p>
                <p className="text-[13px] text-ink-muted">{ch.type} · {ch.channel_for} · {ch.digest_frequency || 'monthly'} digest · {ch.enabled ? 'Enabled' : 'Disabled'}</p>
              </div>
              <button onClick={() => { fetch(`/api/settings/channels/${ch.id}`, { method: 'DELETE' }).then(() => loadData()); }} className="text-[15px] text-claret">Remove</button>
            </div>
          ))}
          {channels.length === 0 && (
            <div className="py-3 text-[13px] text-ink-muted">No channels configured. Add one to receive alerts externally.</div>
          )}
        </div>
        <div className="py-3 flex flex-wrap gap-1.5">
          {['email', 'ntfy', 'pushover', 'webhook'].map(type => (
            <button key={type} onClick={() => {
              const configs: Record<string, Record<string, string>> = {
                email: { smtp_host: '', smtp_port: '587', from: '', to: '', username: '', password: '' },
                ntfy: { url: 'https://ntfy.sh', topic: '' },
                pushover: { user_key: '', api_token: '' },
                webhook: { url: '' },
              };
              const name = prompt(`Name for ${type} channel:`, type);
              if (!name) return;
              const channelFor = prompt('Channel for (all / alerts / digest):', 'all') || 'all';
              const digestFreq = prompt('Digest frequency (weekly / monthly / quarterly / never):', 'monthly') || 'monthly';
              fetch('/api/settings/channels', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ type, name, config: configs[type], enabled: true, channel_for: channelFor, digest_frequency: digestFreq }),
              }).then(() => loadData());
            }} className="apple-btn-secondary text-[12px] px-3 py-2">+ {type}</button>
          ))}
        </div>
      </div>

        </div>
      )}

      {authEnabled && onLogout && (
        <div>
          <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Session</h2>
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <button
              onClick={onLogout}
              className="w-full flex items-center justify-center gap-2 rounded-[8px] py-2.5 text-[16px] text-claret hover:bg-parchment-deep transition-colors"
            >
              <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
              </svg>
              Sign Out
            </button>
          </div>
        </div>
      )}

      {buildInfo?.commit && buildInfo.commit !== 'unknown' && (
        <p className="text-[11px] text-ink-muted text-center pt-6 pb-2">
          {buildInfo.commit.substring(0, 7)}
          {buildInfo.built_at && buildInfo.built_at !== 'unknown' && (
            <> &middot; built {new Date(buildInfo.built_at).toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' })}</>
          )}
        </p>
      )}
    </div>
  );
}
