import { useEffect, useState } from 'react';
import { Section, Chip, LoadingState, ErrorState, SectionBoundary } from '../../components/ui';
import { fmtDateTime, labelFor } from '../../utils/format';
import { fetchJson, useLoad, plural, RowList } from './shared';

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

interface SchedulerStatus {
  jobs?: JobStatus[];
  backfill?: BackfillJob[];
}

interface DataQuality {
  issues: { type: string; severity: string; title: string; detail: string; isin?: string }[];
}

type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'info';

const MANUAL_JOBS = [
  { endpoint: 'refresh-prices', label: 'Refresh prices' },
  { endpoint: 'refresh-etf-metadata', label: 'Refresh ETF data' },
  { endpoint: 'refresh-historical-prices', label: 'Refresh historical prices' },
  { endpoint: 'rebuild-networth', label: 'Rebuild net worth history' },
  { endpoint: 'check-alerts', label: 'Check price alerts' },
] as const;

const BACKFILL_LABELS: Record<string, string> = {
  historical_prices: 'Historical prices',
  historical_fx: 'FX rates (ECB)',
  historical_networth: 'Net worth snapshots',
};

const isBackup = (job: JobStatus) => /backup/i.test(job.name);

function jobTone(job: JobStatus): Tone {
  switch (job.status) {
    case 'ok': return 'success';
    case 'error': return 'danger';
    case 'running': return 'info';
    // A backup that has never run is the one thing on this page that must not be missed.
    case 'never': return isBackup(job) ? 'danger' : 'neutral';
    default: return 'neutral';
  }
}

/** One readable sentence per job instead of the scheduler's raw status string. */
function jobSentence(job: JobStatus): string {
  const schedule = `Runs ${job.schedule.charAt(0).toLowerCase()}${job.schedule.slice(1)}`;
  if (job.status === 'running') return `${schedule} · running now`;
  if (!job.last_run || job.status === 'never') {
    return isBackup(job)
      ? `${schedule} · no backup has been made since the server started`
      : `${schedule} · has not run since the server started`;
  }
  const when = `last run ${fmtDateTime(job.last_run)}`;
  if (job.status === 'error') return `${schedule} · ${when} · failed${job.message ? `: ${job.message}` : ''}`;
  // Success messages that are internal counters (e.g. "total=… (cash=… + invest=…)") stay out of the UI.
  const msg = job.message && !/[=]/.test(job.message) ? ` · ${job.message}` : '';
  return `${schedule} · ${when}${msg}`;
}

function SchedulerSection() {
  const { data, loading, error, reload } = useLoad(() => fetchJson<SchedulerStatus>('/api/settings/scheduler-status'));
  const [running, setRunning] = useState<Record<string, 'idle' | 'loading' | 'ok' | 'error'>>({});
  const jobs = data?.jobs ?? [];
  const backfill = data?.backfill ?? [];

  // Auto-refresh while any backfill job is still running
  const hasRunning = backfill.some(j => j.status === 'running' || j.status === 'pending');
  useEffect(() => {
    if (!hasRunning) return;
    const id = setInterval(reload, 10_000);
    return () => clearInterval(id);
  }, [hasRunning, reload]);

  const trigger = async (endpoint: string) => {
    setRunning(prev => ({ ...prev, [endpoint]: 'loading' }));
    try {
      const resp = await fetch(`/api/settings/${endpoint}`, { method: 'POST' });
      if (!resp.ok) throw new Error('Request failed');
      setRunning(prev => ({ ...prev, [endpoint]: 'ok' }));
      // Reload scheduler status after the background job has had time to complete
      setTimeout(reload, 3000);
    } catch {
      setRunning(prev => ({ ...prev, [endpoint]: 'error' }));
    }
    setTimeout(() => setRunning(prev => ({ ...prev, [endpoint]: 'idle' })), 5000);
  };

  return (
    <>
      <Section title="Scheduled Jobs" subtitle="Background jobs that keep prices, exchange rates, snapshots and backups current." first>
        {loading ? <LoadingState lines={5} /> : error && !data ? (
          <ErrorState message="The job status couldn’t be loaded." details={error} onRetry={reload} />
        ) : (
          <>
            {jobs.length === 0 ? (
              <p className="text-[14px] text-ink-body">The scheduler reported no jobs.</p>
            ) : (
              <RowList>
                {jobs.map(job => (
                  <li key={job.name} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 px-4 py-3">
                    <div className="min-w-0 flex-1 basis-56">
                      <p className="text-[15px] font-medium text-ink">{job.name}</p>
                      <p className="text-[13px] text-ink-muted mt-0.5 break-words">{jobSentence(job)}</p>
                    </div>
                    <Chip tone={jobTone(job)}>{job.status === 'running' ? 'Running' : labelFor(job.status)}</Chip>
                  </li>
                ))}
              </RowList>
            )}
            <div className="mt-4 flex flex-wrap gap-2" aria-live="polite">
              {MANUAL_JOBS.map(({ endpoint, label }) => {
                const state = running[endpoint] || 'idle';
                return (
                  <button
                    key={endpoint}
                    type="button"
                    onClick={() => trigger(endpoint)}
                    disabled={state === 'loading'}
                    className="apple-btn-secondary min-h-[44px] md:min-h-[40px]"
                  >
                    {state === 'loading' ? 'Starting…' :
                     state === 'ok' ? `${label} — started` :
                     state === 'error' ? `${label} — failed, try again` : label}
                  </button>
                );
              })}
            </div>
          </>
        )}
      </Section>

      {backfill.length > 0 && (
        <Section title="Initial Data Backfill" subtitle="One-off jobs that load history after the first import.">
          <RowList>
            {backfill.map(job => {
              const isRunning = job.status === 'running';
              const isDone = job.status === 'completed';
              const isFailed = job.status === 'failed';
              return (
                <li key={job.name} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 px-4 py-3">
                  <div className="min-w-0 flex-1 basis-56">
                    <p className="text-[15px] font-medium text-ink">{BACKFILL_LABELS[job.name] || labelFor(job.name)}</p>
                    <p className="text-[13px] text-ink-muted mt-0.5 break-words">
                      {isDone && (job.completed_at ? `Completed ${fmtDateTime(job.completed_at)}` : 'Completed')}
                      {isRunning && (job.started_at ? `Started ${fmtDateTime(job.started_at)}` : 'Running now')}
                      {isFailed && `Failed${job.attempts > 1 ? ` after ${job.attempts} attempts` : ''}${job.message ? ` · ${job.message}` : ''}`}
                      {job.status === 'pending' && 'Waiting to start'}
                    </p>
                  </div>
                  <Chip tone={isDone ? 'success' : isRunning ? 'info' : isFailed ? 'danger' : 'neutral'}>
                    {labelFor(job.status)}
                  </Chip>
                </li>
              );
            })}
          </RowList>
        </Section>
      )}
    </>
  );
}

function DataQualitySection() {
  const { data, loading, error, reload } = useLoad(() => fetchJson<DataQuality>('/api/analysis/data-quality'));
  // Concentration drift is a portfolio observation, not a data problem — Net Worth shows it.
  const issues = (data?.issues ?? []).filter(i => i.type !== 'concentration_drift');
  const count = (sev: string) => issues.filter(i => i.severity === sev).length;
  const errors = count('error');
  const warnings = count('warning');
  const info = issues.length - errors - warnings;

  return (
    <Section title="Data Quality" subtitle="Checks on imported data: stale prices, missing dividends, unusual fees and possible duplicates.">
      {loading ? <LoadingState lines={3} /> : error && !data ? (
        <ErrorState message="The data checks couldn’t be loaded." details={error} onRetry={reload} />
      ) : issues.length === 0 ? (
        <p className="flex items-center gap-2 text-[14px] text-ink-body"><Chip tone="success">No issues</Chip> All data checks passed.</p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 mb-3">
            {errors > 0 && <Chip tone="danger">{plural(errors, 'error')}</Chip>}
            {warnings > 0 && <Chip tone="warning">{plural(warnings, 'warning')}</Chip>}
            {info > 0 && <Chip tone="info">{plural(info, 'note')}</Chip>}
          </div>
          <RowList>
            {issues.map((iss, i) => (
              <li key={i} className="flex flex-wrap items-start gap-x-3 gap-y-1 px-4 py-3">
                <Chip tone={iss.severity === 'error' ? 'danger' : iss.severity === 'warning' ? 'warning' : 'info'} className="mt-0.5">
                  {iss.severity === 'error' ? 'Error' : iss.severity === 'warning' ? 'Warning' : 'Note'}
                </Chip>
                <div className="min-w-0 flex-1 basis-56">
                  <p className="text-[15px] text-ink">{iss.title}</p>
                  <p className="text-[13px] text-ink-muted mt-0.5">{iss.detail}</p>
                </div>
              </li>
            ))}
          </RowList>
        </>
      )}
    </Section>
  );
}

export default function DataTab() {
  return (
    <>
      <SectionBoundary><SchedulerSection /></SectionBoundary>
      <SectionBoundary><DataQualitySection /></SectionBoundary>
      <Section title="Data Export">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p className="text-[15px] font-medium text-ink">Export transactions</p>
            <p className="text-[13px] text-ink-muted mt-0.5">Download all transactions as a CSV file.</p>
          </div>
          <a href="/api/settings/export-transactions" className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">Download CSV</a>
        </div>
      </Section>
    </>
  );
}
