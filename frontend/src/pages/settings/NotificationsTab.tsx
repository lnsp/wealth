import { useState } from 'react';
import type { ChangeEvent, FormEvent } from 'react';
import { api, type WealthReportDetail, type WealthReportSummary } from '../../api/client';
import {
  Section, SubHeading, Field, SelectField, Chip, AddChip, MetricCard, MetricGrid,
  LoadingState, ErrorState, EmptyState, SectionBoundary, useConfirm,
} from '../../components/ui';
import { fmtDate, fmtEUR, fmtMonth, fmtPct, labelFor } from '../../utils/format';
import { fetchJson, useLoad, errorText, InlineError, plural, RowList, ROW_BTN, ROW_BTN_DANGER, FORM_CARD } from './shared';

const BTN = 'min-h-[44px] md:min-h-[40px]';

/* ─────────────────────────── Wealth reports ─────────────────────────── */

/** "2026-10" → "October 2026"; anything else is shown as stored. */
function periodLabel(label: string): string {
  return /^\d{4}-\d{2}$/.test(label) ? fmtMonth(`${label}-01`) : label;
}

const changeTone = (v: number) => (v > 0 ? 'positive' : v < 0 ? 'negative' : 'neutral');

function ReportDetail({ report, onClose }: { report: WealthReportDetail; onClose: () => void }) {
  const d = report.data;
  const hasNetWorth = d.has_net_worth !== false;
  const withheld = d.dividends_withheld ?? 0;
  return (
    <div className={`${FORM_CARD} mt-4`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <SubHeading>Report for {periodLabel(report.period_label)}</SubHeading>
        <button type="button" onClick={onClose} className={ROW_BTN}>Close</button>
      </div>
      {hasNetWorth ? (
        <MetricGrid>
          <MetricCard
            label="Previous close"
            value={fmtEUR(d.net_worth_start)}
            sublabel={d.start_date ? `Net worth on ${fmtDate(d.start_date)}` : 'No earlier history'}
          />
          <MetricCard
            label="Close"
            value={fmtEUR(d.net_worth_end)}
            sublabel={d.end_date ? `Net worth on ${fmtDate(d.end_date)}` : undefined}
          />
          <MetricCard
            label="Change"
            value={fmtEUR(d.net_worth_change, { sign: true })}
            tone={changeTone(d.net_worth_change)}
            sublabel={d.net_worth_start > 0 ? fmtPct(d.net_worth_change_pct, { sign: true }) : undefined}
          />
          {d.net_deposits != null && (
            <MetricCard
              label="Net deposits"
              value={fmtEUR(d.net_deposits, { sign: true })}
              sublabel="Deposits and vested shares, less withdrawals"
            />
          )}
          {d.market_change != null && (
            <MetricCard
              label="Market and other"
              value={fmtEUR(d.market_change, { sign: true })}
              tone={changeTone(d.market_change)}
              sublabel="Prices, income, fees and revaluations"
            />
          )}
          <MetricCard
            label="Dividends, net"
            value={fmtEUR(d.total_dividends, { cents: true })}
            sublabel={withheld > 0
              ? `${fmtEUR(d.dividends_gross, { cents: true })} gross, ${fmtEUR(withheld, { cents: true })} withheld`
              : 'As credited'}
          />
        </MetricGrid>
      ) : (
        <p className="text-[14px] text-ink-body">There is no net worth history for this period yet.</p>
      )}
      <div className="text-[13px] text-ink-body flex flex-col gap-1">
        {d.top_gainer && <p>Best holding: {d.top_gainer.name} ({fmtPct(d.top_gainer.return_pct, { sign: true })})</p>}
        {d.top_loser && <p>Weakest holding: {d.top_loser.name} ({fmtPct(d.top_loser.return_pct, { sign: true })})</p>}
        <p>{plural(d.new_transactions ?? 0, 'transaction')} in the period</p>
        {(d.warnings ?? []).map((w, i) => <p key={i} className="text-ink-muted">{w}</p>)}
        <p className="text-ink-muted">
          Figures are recalculated from the current net worth history each time a report is opened.
          Foreign dividends use the exchange rate of the payment date.
        </p>
      </div>
    </div>
  );
}

/** Period of a stored report ("2026-10" or "2026") as the generate request expects it. */
function reportPeriod(rpt: WealthReportSummary): { report_type: string; year: number; month?: number } | null {
  const m = /^(\d{4})(?:-(\d{2}))?$/.exec(rpt.period_label);
  if (!m) return null;
  return m[2]
    ? { report_type: 'monthly', year: Number(m[1]), month: Number(m[2]) }
    : { report_type: 'annual', year: Number(m[1]) };
}

function ReportsSection() {
  const { data, loading, error, reload } = useLoad(() => api.listReports());
  const reports: WealthReportSummary[] = data?.reports ?? [];
  const [selected, setSelected] = useState<WealthReportDetail | null>(null);
  const [generating, setGenerating] = useState(false);
  const [regenerating, setRegenerating] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [actionError, setActionError] = useState('');

  const generate = async () => {
    const now = new Date();
    const prev = new Date(now.getFullYear(), now.getMonth() - 1, 1);
    setActionError('');
    setGenerating(true);
    try {
      await api.generateReport({ report_type: 'monthly', year: prev.getFullYear(), month: prev.getMonth() + 1 });
      await reload();
    } catch (err) {
      setActionError(errorText(err, 'The report couldn’t be generated.'));
    } finally {
      setGenerating(false);
    }
  };

  const regenerate = async (rpt: WealthReportSummary) => {
    const period = reportPeriod(rpt);
    if (!period) return;
    setActionError('');
    setRegenerating(rpt.id);
    try {
      await api.generateReport(period);
      await reload();
      if (selected?.id === rpt.id) setSelected(await api.getReport(rpt.id));
    } catch (err) {
      setActionError(errorText(err, `The report for ${periodLabel(rpt.period_label)} couldn’t be regenerated.`));
    } finally {
      setRegenerating(null);
    }
  };

  const view = async (id: string) => {
    setActionError('');
    try {
      setSelected(await api.getReport(id));
    } catch (err) {
      setActionError(errorText(err, 'The report couldn’t be opened.'));
    }
  };

  const generateBtn = (
    <button type="button" onClick={generate} disabled={generating} className={`apple-btn-secondary ${BTN}`}>
      {generating ? 'Generating…' : 'Generate last month'}
    </button>
  );

  return (
    <Section
      title="Wealth Reports"
      subtitle="A monthly and yearly summary of net worth, dividends and holdings, generated automatically."
      first
      actions={reports.length > 0 ? generateBtn : undefined}
    >
      <InlineError>{actionError}</InlineError>
      {loading ? <LoadingState lines={4} /> : error && !data ? (
        <ErrorState message="The reports couldn’t be loaded." details={error} onRetry={reload} />
      ) : reports.length === 0 ? (
        <EmptyState
          title="No reports yet"
          message="Reports are created on the first day of each month. You can also generate last month’s report now."
          action={{ label: generating ? 'Generating…' : 'Generate last month', onClick: generate }}
        />
      ) : (
        <>
          <RowList>
            {(showAll ? reports : reports.slice(0, 5)).map(rpt => (
              <li key={rpt.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
                <div className="min-w-0 flex-1 basis-48">
                  <p className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-ink">
                    {periodLabel(rpt.period_label)}
                    <Chip>{labelFor(rpt.report_type)}</Chip>
                  </p>
                  <p className="text-[13px] text-ink-muted mt-0.5">Generated {fmtDate(rpt.generated_at)}</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <button type="button" onClick={() => view(rpt.id)} aria-label={`View report for ${periodLabel(rpt.period_label)}`} className={ROW_BTN}>View</button>
                  <a href={`/api/settings/reports/${rpt.id}/pdf`} download aria-label={`PDF of report for ${periodLabel(rpt.period_label)}`} className={ROW_BTN}>PDF</a>
                  {reportPeriod(rpt) && (
                    <button
                      type="button"
                      onClick={() => regenerate(rpt)}
                      disabled={regenerating === rpt.id}
                      aria-label={`Regenerate report for ${periodLabel(rpt.period_label)}`}
                      className={ROW_BTN}
                    >
                      {regenerating === rpt.id ? 'Regenerating…' : 'Regenerate'}
                    </button>
                  )}
                </div>
              </li>
            ))}
          </RowList>
          {reports.length > 5 && (
            <button type="button" onClick={() => setShowAll(prev => !prev)} aria-expanded={showAll} className={`${ROW_BTN} mt-3`}>
              {showAll ? 'Show fewer' : `Show all ${reports.length} reports`}
            </button>
          )}
        </>
      )}
      {selected && <ReportDetail report={selected} onClose={() => setSelected(null)} />}
    </Section>
  );
}

/* ─────────────────────── Notification channels ─────────────────────── */

type ChannelType = 'email' | 'ntfy' | 'pushover' | 'webhook';

interface Channel {
  id: string;
  type: string;
  name: string;
  config: Record<string, string>;
  enabled: boolean;
  channel_for: string;
  digest_frequency: string;
}

const CHANNEL_TYPES: [ChannelType, string][] = [
  ['webhook', 'Webhook'],
  ['ntfy', 'ntfy'],
  ['pushover', 'Pushover'],
  ['email', 'Email (SMTP)'],
];

const SEND_FOR: [string, string][] = [
  ['all', 'Price alerts and digest'],
  ['alerts', 'Price alerts only'],
  ['digest', 'Digest only'],
];

const DIGEST: [string, string][] = [
  ['weekly', 'Weekly'],
  ['monthly', 'Monthly'],
  ['quarterly', 'Quarterly'],
  ['never', 'Never'],
];

/** Config keys the backend expects per channel type (internal/handler/settings.go: validateChannelConfig). */
const CONFIG_KEYS: Record<ChannelType, string[]> = {
  webhook: ['url'],
  ntfy: ['url', 'topic'],
  pushover: ['user_key', 'api_token'],
  email: ['smtp_host', 'smtp_port', 'from', 'to', 'username', 'password'],
};

const CONFIG_DEFAULTS: Record<ChannelType, Record<string, string>> = {
  webhook: { url: '' },
  ntfy: { url: 'https://ntfy.sh', topic: '' },
  pushover: { user_key: '', api_token: '' },
  email: { smtp_host: '', smtp_port: '587', from: '', to: '', username: '', password: '' },
};

const SECRET_KEYS = new Set(['password', 'api_token']);

function urlError(raw: string): string | undefined {
  const v = raw.trim();
  if (!v) return 'Enter a full address starting with https://';
  try {
    const u = new URL(v);
    if ((u.protocol !== 'https:' && u.protocol !== 'http:') || !u.hostname) return 'Enter a full address starting with https://';
    const host = u.hostname.toLowerCase();
    if (host === 'localhost' || /\.(localhost|local|internal)$/.test(host)) return 'The address must be reachable from the internet, not a local one';
  } catch {
    return 'Enter a full address starting with https://';
  }
  return undefined;
}

const emailError = (raw: string): string | undefined =>
  /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(raw.trim()) ? undefined : 'Enter an email address like name@example.com';

function validateChannel(type: ChannelType, name: string, cfg: Record<string, string>): Record<string, string> {
  const errs: Record<string, string> = {};
  const set = (key: string, msg: string | undefined) => { if (msg) errs[key] = msg; };
  if (!name.trim()) errs.name = 'Enter a name for the channel';
  const v = (k: string) => (cfg[k] ?? '').trim();
  switch (type) {
    case 'webhook':
      set('url', urlError(v('url')));
      break;
    case 'ntfy':
      set('url', urlError(v('url')));
      if (!v('topic')) errs.topic = 'Enter the topic to publish to';
      else if (/[/\\\s]/.test(v('topic')) || v('topic').includes('..')) errs.topic = 'A topic can’t contain slashes, spaces or “..”';
      break;
    case 'pushover':
      if (!v('user_key')) errs.user_key = 'Enter your Pushover user key';
      if (!v('api_token')) errs.api_token = 'Enter the Pushover application token';
      break;
    case 'email':
      if (!v('smtp_host')) errs.smtp_host = 'Enter the SMTP server, e.g. smtp.example.com';
      if (!/^\d{1,5}$/.test(v('smtp_port')) || Number(v('smtp_port')) < 1 || Number(v('smtp_port')) > 65535) errs.smtp_port = 'Enter a port number, usually 587';
      set('from', emailError(v('from')));
      set('to', emailError(v('to')));
      break;
  }
  return errs;
}

function ChannelForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [type, setType] = useState<ChannelType>('webhook');
  const [name, setName] = useState('');
  const [sendFor, setSendFor] = useState('all');
  const [digest, setDigest] = useState('monthly');
  // One draft per type, so switching the type and back keeps what was typed.
  const [configs, setConfigs] = useState<Record<ChannelType, Record<string, string>>>(CONFIG_DEFAULTS);
  const [submitted, setSubmitted] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const cfg = configs[type];
  const errs = submitted ? validateChannel(type, name, cfg) : {};
  const setCfg = (key: string, value: string) => setConfigs(prev => ({ ...prev, [type]: { ...prev[type], [key]: value } }));
  const cfgField = (key: string) => ({
    value: cfg[key] ?? '',
    onChange: (e: ChangeEvent<HTMLInputElement>) => setCfg(key, e.target.value),
    error: errs[key],
    autoComplete: 'off',
    spellCheck: false,
  });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    setError('');
    if (Object.keys(validateChannel(type, name, cfg)).length > 0) return;
    const config: Record<string, string> = {};
    for (const key of CONFIG_KEYS[type]) {
      const raw = cfg[key] ?? '';
      config[key] = SECRET_KEYS.has(key) ? raw : raw.trim();
    }
    setSaving(true);
    try {
      await fetchJson('/api/settings/channels', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type,
          name: name.trim(),
          config,
          enabled: true,
          channel_for: sendFor,
          digest_frequency: digest,
        }),
      });
      onDone();
    } catch (err) {
      setError(errorText(err, 'The channel couldn’t be saved.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className={`${FORM_CARD} mb-4`}>
      <SubHeading>Add Notification Channel</SubHeading>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Name" value={name} onChange={e => setName(e.target.value)} error={errs.name} autoFocus />
        <SelectField label="Channel type" value={type} onChange={e => setType(e.target.value as ChannelType)}>
          {CHANNEL_TYPES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </SelectField>

        {type === 'webhook' && (
          <Field label="Webhook URL" type="url" inputMode="url" hint="Each notification is sent to this address as a JSON POST request." className="md:col-span-2" {...cfgField('url')} />
        )}

        {type === 'ntfy' && (
          <>
            <Field label="Server URL" type="url" inputMode="url" hint="Use https://ntfy.sh unless you host your own server." {...cfgField('url')} />
            <Field label="Topic" hint="The topic your phone is subscribed to." {...cfgField('topic')} />
          </>
        )}

        {type === 'pushover' && (
          <>
            <Field label="User key" {...cfgField('user_key')} />
            <Field label="Application token" type="password" {...cfgField('api_token')} autoComplete="new-password" />
          </>
        )}

        {type === 'email' && (
          <>
            <Field label="SMTP server" {...cfgField('smtp_host')} />
            <Field label="SMTP port" inputMode="numeric" {...cfgField('smtp_port')} />
            <Field label="From address" type="email" inputMode="email" {...cfgField('from')} />
            <Field label="To address" type="email" inputMode="email" {...cfgField('to')} />
            <Field label="SMTP username" hint="Optional." {...cfgField('username')} />
            <Field label="SMTP password" type="password" hint="Optional." {...cfgField('password')} autoComplete="new-password" />
          </>
        )}

        <SelectField label="Send for" value={sendFor} onChange={e => setSendFor(e.target.value)}>
          {SEND_FOR.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </SelectField>
        {sendFor !== 'alerts' && (
          <SelectField label="Digest frequency" value={digest} onChange={e => setDigest(e.target.value)}>
            {DIGEST.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </SelectField>
        )}
      </div>
      <InlineError>{error}</InlineError>
      <div className="flex flex-wrap justify-end gap-2.5">
        <button type="button" onClick={onCancel} className={`apple-btn-secondary ${BTN}`}>Cancel</button>
        <button type="submit" disabled={saving} className={`apple-btn-primary ${BTN}`}>{saving ? 'Saving…' : 'Save channel'}</button>
      </div>
    </form>
  );
}

function channelSummary(ch: Channel): string {
  const type = CHANNEL_TYPES.find(([v]) => v === ch.type)?.[1] ?? labelFor(ch.type);
  const sendFor = SEND_FOR.find(([v]) => v === ch.channel_for)?.[1] ?? labelFor(ch.channel_for);
  const freq = ch.digest_frequency || 'monthly';
  const digest = ch.channel_for === 'alerts' || freq === 'never' ? '' : ` · ${labelFor(freq).toLowerCase()} digest`;
  return `${type} · ${sendFor}${digest}`;
}

function ChannelsSection() {
  const { data, loading, error, reload } = useLoad(() => fetchJson<{ channels?: Channel[] }>('/api/settings/channels'));
  const channels = data?.channels ?? [];
  const [confirm, confirmDialog] = useConfirm();
  const [adding, setAdding] = useState(false);
  const [actionError, setActionError] = useState('');

  const remove = async (ch: Channel) => {
    const ok = await confirm({
      title: `Remove channel “${ch.name}”?`,
      body: 'Alerts and digests are no longer sent to this channel, and its saved settings are deleted. This can’t be undone.',
      confirmLabel: 'Remove channel',
    });
    if (!ok) return;
    setActionError('');
    try {
      await fetchJson(`/api/settings/channels/${ch.id}`, { method: 'DELETE' });
      await reload();
    } catch (err) {
      setActionError(errorText(err, `“${ch.name}” couldn’t be removed.`));
    }
  };

  return (
    <Section
      title="Notification Channels"
      subtitle="Where price alerts and the regular digest are sent outside the app."
      actions={!adding && channels.length > 0 ? <AddChip onClick={() => setAdding(true)}>Add channel</AddChip> : undefined}
    >
      {adding && <ChannelForm onCancel={() => setAdding(false)} onDone={() => { setAdding(false); reload(); }} />}
      <InlineError>{actionError}</InlineError>
      {loading ? <LoadingState lines={3} /> : error && !data ? (
        <ErrorState message="The notification channels couldn’t be loaded." details={error} onRetry={reload} />
      ) : channels.length === 0 ? (
        !adding && (
          <EmptyState
            title="No channels yet"
            message="Add a channel to receive price alerts and digests by webhook, push notification or email."
            action={{ label: 'Add channel', onClick: () => setAdding(true) }}
          />
        )
      ) : (
        <RowList>
          {channels.map(ch => (
            <li key={ch.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
              <div className="min-w-0 flex-1 basis-48">
                <p className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-ink">
                  <span className="break-words">{ch.name}</span>
                  <Chip tone={ch.enabled ? 'success' : 'neutral'}>{ch.enabled ? 'Enabled' : 'Disabled'}</Chip>
                </p>
                <p className="text-[13px] text-ink-muted mt-0.5">{channelSummary(ch)}</p>
              </div>
              <button type="button" onClick={() => remove(ch)} aria-label={`Remove ${ch.name}…`} className={ROW_BTN_DANGER}>Remove…</button>
            </li>
          ))}
        </RowList>
      )}
      {confirmDialog}
    </Section>
  );
}

export default function NotificationsTab() {
  return (
    <>
      <SectionBoundary><ReportsSection /></SectionBoundary>
      <SectionBoundary><ChannelsSection /></SectionBoundary>
    </>
  );
}
