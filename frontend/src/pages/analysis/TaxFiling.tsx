import { useState } from 'react';
import { Chip, EmptyState, Field, Section, SectionBoundary } from '../../components/ui';
import { fmtDateLong } from '../../utils/format';
import { Callout, KeyValues, Loaded, Money, Statement, SubHeadingRow, TableRegion, arr, getJson, num, plural, useLoad } from './shared';
import type { LoadState } from './shared';

interface TaxCalendar {
  year: number;
  events?: { month: number; date: string; title: string; description: string; amount?: number; category: string; urgency: string }[] | null;
  upcoming: number;
}

interface AnlageKAP {
  year: number;
  brokers?: { broker_name: string; account_name: string; dividends: number; interest: number; realized_gains: number; realized_losses: number; teilfreistellung: number; withheld_tax: number }[] | null;
  anlage_kap?: { line: number; description: string; amount: number }[] | null;
  cross_broker_note?: string;
}

export default function TaxFiling() {
  const kap = useLoad(() => getJson<AnlageKAP>('/api/analysis/anlage-kap'));
  return (
    <>
      <SectionBoundary><Calendar /></SectionBoundary>
      <SectionBoundary><AnlageKap kap={kap} /></SectionBoundary>
      <SectionBoundary><Reconciliation kap={kap} /></SectionBoundary>
    </>
  );
}

/* ───────────── Tax calendar ───────────── */

function Calendar() {
  const calendar = useLoad(() => getJson<TaxCalendar>('/api/analysis/tax-calendar'));
  const year = calendar.data?.year;
  return (
    <Section first title={year ? `Tax Calendar ${year}` : 'Tax Calendar'} subtitle="German tax dates that apply to your portfolio, with amounts where they can be estimated.">
      <Loaded state={calendar} lines={5}>
        {data => {
          const today = new Date();
          today.setHours(0, 0, 0, 0);
          const events = arr(data?.events)
            .map(ev => {
              const time = new Date(ev.date).getTime();
              return { ...ev, days: Number.isFinite(time) ? Math.round((time - today.getTime()) / 86_400_000) : 0 };
            })
            .sort((a, b) => a.date.localeCompare(b.date));
          if (events.length === 0) return <Statement>No tax dates for this year.</Statement>;
          const upcoming = events.filter(e => e.days >= 0);
          const past = events.filter(e => e.days < 0).reverse();

          const row = (ev: typeof events[number]) => {
            const isPast = ev.days < 0;
            const when = isPast ? `${plural(-ev.days, 'day', 'days')} ago`
              : ev.days === 0 ? 'Today'
              : `In ${plural(ev.days, 'day', 'days')}`;
            const tone = isPast ? 'neutral' : ev.days < 14 ? 'danger' : ev.days < 60 ? 'warning' : 'neutral';
            return (
              <li key={`${ev.date}-${ev.title}`} className="py-3">
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
                  <span className="text-[14px] font-medium text-ink">{ev.title}</span>
                  <span className="flex flex-wrap items-center gap-2 text-[13px] text-ink-body">
                    {fmtDateLong(ev.date)}
                    <Chip tone={tone}>{isPast ? `Past · ${when}` : when}</Chip>
                  </span>
                </div>
                <p className="mt-1 text-[13px] text-ink-body">{ev.description}</p>
                {ev.amount != null && ev.amount > 0 && <p className="mt-1 text-[13px] text-ink">Amount: <Money value={ev.amount} className="font-medium" /></p>}
              </li>
            );
          };

          return (
            <>
              <SubHeadingRow>Upcoming</SubHeadingRow>
              {upcoming.length > 0
                ? <ul className="divide-y divide-divider border-y border-divider">{upcoming.map(row)}</ul>
                : <Statement>Nothing left this year. Next year’s dates appear in January.</Statement>}
              {past.length > 0 && (
                <>
                  <SubHeadingRow>Earlier This Year</SubHeadingRow>
                  <ul className="divide-y divide-divider border-y border-divider">{past.map(row)}</ul>
                </>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Anlage KAP ───────────── */

const NoKap = () => (
  <EmptyState
    title="Nothing to file yet"
    message="Anlage KAP figures are built from sales, dividends and interest. They appear once such transactions exist for the tax year."
    action={{ label: 'Import transactions', href: '/transactions' }}
  />
);

function AnlageKap({ kap }: { kap: LoadState<AnlageKAP> }) {
  const year = kap.data?.year;
  return (
    <Section title={year ? `Anlage KAP ${year}` : 'Anlage KAP'}
      subtitle="Anlage KAP is the capital income form of the German tax return. These are the figures to copy across, line by line."
      actions={year ? (
        <>
          <a href={`/api/analysis/export-tax?year=${year}`} download className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]">Export CSV</a>
          <a href={`/api/analysis/export-datev?year=${year}`} download className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]" title="DATEV is the format German tax advisers import">Export DATEV</a>
        </>
      ) : undefined}>
      <Loaded state={kap} lines={6}>
        {data => {
          const lines = arr(data?.anlage_kap);
          const brokers = arr(data?.brokers);
          if (lines.length === 0) return <NoKap />;
          return (
            <>
              {data.cross_broker_note && (
                <div className="mb-5"><Callout tone="info" label="Note">{data.cross_broker_note}</Callout></div>
              )}

              <ul className="md:hidden divide-y divide-divider border-y border-divider">
                {lines.map(line => (
                  <li key={line.line} className="flex items-baseline justify-between gap-4 py-2 text-[13px]">
                    <span className="min-w-0 text-ink-body"><span className="font-medium text-ink">Line {line.line}</span> · {line.description}</span>
                    <Money value={line.amount} cents tone={line.amount < 0 ? 'negative' : undefined} className="shrink-0 whitespace-nowrap text-ink" />
                  </li>
                ))}
              </ul>
              <TableRegion label="Anlage KAP lines" className="hidden md:block">
                <table className="apple-table">
                  <thead>
                    <tr>
                      <th scope="col">Line (Zeile)</th>
                      <th scope="col">Entry</th>
                      <th scope="col" className="num">Amount</th>
                    </tr>
                  </thead>
                  <tbody>
                    {lines.map(line => (
                      <tr key={line.line}>
                        <td className="whitespace-nowrap text-ink">{line.line}</td>
                        <td className="min-w-[180px]">{line.description}</td>
                        <td className="num whitespace-nowrap text-ink"><Money value={line.amount} cents tone={line.amount < 0 ? 'negative' : undefined} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </TableRegion>

              {brokers.length > 0 && (
                <>
                  <SubHeadingRow>By Broker</SubHeadingRow>
                  <div className="grid gap-3 md:grid-cols-2">
                    {brokers.map((b, i) => {
                      const rows = [
                        { label: 'Dividends', v: b.dividends },
                        { label: 'Interest', v: b.interest },
                        { label: 'Realised gains', v: b.realized_gains },
                        { label: 'Realised losses', v: b.realized_losses },
                        { label: 'Partial exemption', v: b.teilfreistellung },
                        { label: 'Tax withheld', v: b.withheld_tax },
                      ].filter(r => num(r.v) !== 0);
                      return (
                        <div key={`${b.broker_name}-${i}`} className="rounded-apple border border-divider px-4 py-3">
                          <p className="mb-2 text-[14px] font-medium text-ink">{b.broker_name}</p>
                          {rows.length > 0
                            ? <KeyValues rows={rows.map(r => ({ label: r.label, value: <Money value={r.v} cents tone={r.v < 0 ? 'negative' : undefined} /> }))} />
                            : <p className="text-[13px] text-ink-muted">No taxable income this year.</p>}
                        </div>
                      );
                    })}
                  </div>
                </>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Steuerbescheid reconciliation ───────────── */

function readAmounts(): Record<string, string> {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem('recon_amounts') || '{}');
    return parsed && typeof parsed === 'object' ? parsed as Record<string, string> : {};
  } catch { return {}; }
}

/** Accepts "1.234,56" as well as "1234.56". */
function parseAmount(text: string | undefined): number | null {
  const t = (text ?? '').trim().replace(/\s|€/g, '').replace('−', '-');
  if (t === '') return null;
  const n = Number(t.includes(',') ? t.replace(/\./g, '').replace(',', '.') : t);
  return Number.isFinite(n) ? n : null;
}

function Reconciliation({ kap }: { kap: LoadState<AnlageKAP> }) {
  const [amounts, setAmounts] = useState<Record<string, string>>(readAmounts);
  const update = (key: string, value: string) => {
    const next = { ...amounts, [key]: value };
    setAmounts(next);
    try { localStorage.setItem('recon_amounts', JSON.stringify(next)); } catch { /* kept for this visit only */ }
  };
  return (
    <Section title="Steuerbescheid Reconciliation"
      subtitle="When your Steuerbescheid (tax assessment notice) arrives, enter the tax office’s figure for each line to compare it with this app’s.">
      <Loaded state={kap} lines={5}>
        {data => {
          const lines = arr(data?.anlage_kap);
          if (lines.length === 0) return <Statement>Nothing to reconcile until Anlage KAP has figures for the year.</Statement>;
          const checked = lines.map(line => {
            const key = `${data.year}-Z${line.line}`;
            const entered = parseAmount(amounts[key]);
            const invalid = (amounts[key] ?? '').trim() !== '' && entered === null;
            const diff = entered !== null && entered !== 0 ? entered - line.amount : 0;
            return { line, key, entered, invalid, diff, differs: entered !== null && entered !== 0 && Math.abs(diff) > 1 };
          });
          const anyEntered = checked.some(c => c.entered !== null && c.entered !== 0);
          const differing = checked.filter(c => c.differs).length;
          return (
            <>
              <ul className="divide-y divide-divider border-y border-divider">
                {checked.map(({ line, key, invalid, diff, differs, entered }) => (
                  <li key={line.line} className="grid gap-x-6 gap-y-2 py-3 md:grid-cols-[1fr_220px] md:items-start">
                    <div className="min-w-0 text-[13px]">
                      <p className="font-medium text-ink">Line {line.line}: {line.description.split('(')[0].trim()}</p>
                      <p className="mt-0.5 text-ink-body">This app: <Money value={line.amount} cents className="text-ink" /></p>
                      {differs && (
                        <p className="mt-1 flex flex-wrap items-center gap-2 text-ink-body">
                          <Chip tone="danger">Differs</Chip>
                          Tax office is <Money value={Math.abs(diff)} cents className="font-medium text-ink" /> {diff > 0 ? 'higher' : 'lower'}
                        </p>
                      )}
                      {!differs && entered !== null && entered !== 0 && (
                        <p className="mt-1 flex items-center gap-2 text-ink-body"><Chip tone="success">Matches</Chip></p>
                      )}
                    </div>
                    <Field label={`Tax office figure, line ${line.line}`} unit="€" inputMode="decimal" autoComplete="off"
                      value={amounts[key] ?? ''} onChange={e => update(key, e.target.value)}
                      error={invalid ? 'Enter an amount such as 1.234,56' : undefined} />
                  </li>
                ))}
              </ul>
              {anyEntered && (
                <div className="mt-4" role="status">
                  {differing > 0
                    ? <Callout tone="danger" label={plural(differing, 'difference', 'differences')} title="Review these lines with your Steuerberater (tax adviser)." />
                    : <Callout tone="success" label="All match" title="Every figure you entered agrees with this app to within 1 €." />}
                </div>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}
