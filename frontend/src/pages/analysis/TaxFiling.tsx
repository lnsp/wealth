import { useState } from 'react';
import { Chip, EmptyState, Field, Section, SectionBoundary } from '../../components/ui';
import { fmtDateLong, fmtPct } from '../../utils/format';
import { Callout, KeyValues, Loaded, Money, Statement, SubHeadingRow, TableRegion, arr, getJson, num, plural, useLoad } from './shared';
import type { LoadState } from './shared';

interface TaxCalendar {
  year: number;
  events?: { month: number; date: string; title: string; description: string; amount?: number; category: string; urgency: string }[] | null;
  upcoming: number;
}

type KapSection = 'domestic' | 'foreign' | 'taxes' | 'inv';

interface KapLine {
  line: number;
  /** Official German wording, so a figure stays recognisable if a form renumbers lines. */
  description: string;
  hint?: string;
  amount: number;
  section: KapSection;
  /** Belongs to a German bank's Steuerbescheinigung; the amount is this app's estimate. */
  estimate?: boolean;
  /** The app cannot know this figure; leave it empty unless a certificate shows one. */
  blank?: boolean;
}

interface KapBroker {
  broker_name: string;
  account_name: string;
  domestic: boolean;
  domicile_assumed: boolean;
  dividends: number;
  interest: number;
  realized_gains: number;
  realized_losses: number;
  vorabpauschale: number;
  teilfreistellung: number;
  withheld_tax: number;
  foreign_tax: number;
}

interface AnlageKAP {
  year: number;
  joint?: boolean;
  church_tax_rate?: number;
  allowance?: number;
  brokers?: KapBroker[] | null;
  anlage_kap?: KapLine[] | null;
  anlage_kap_inv?: KapLine[] | null;
  layout_note?: string;
  net_taxable?: number;
  /** Losses carried in from earlier years (≤ 0), both pots. */
  loss_carry_in?: number;
  estimated_tax?: number;
  total_withheld?: number;
  balance_due?: number;
  cross_broker_note?: string;
}

/**
 * Filing preferences set elsewhere on the Tax page: joint filing (Overview,
 * Freistellungsauftrag section) and the church-tax rate (Tools, sell simulator).
 */
function filingQuery(): string {
  const params = new URLSearchParams();
  try {
    if (localStorage.getItem('fsa_joint') === '1') params.set('joint', '1');
    const church = parseFloat(localStorage.getItem('church_tax_rate') || '0');
    if (church > 0) params.set('church_tax', String(church));
  } catch { /* preferences only */ }
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

export default function TaxFiling() {
  const [query] = useState(filingQuery);
  const kap = useLoad(() => getJson<AnlageKAP>(`/api/analysis/anlage-kap${query}`));
  return (
    <>
      <SectionBoundary><Calendar query={query} /></SectionBoundary>
      <SectionBoundary><AnlageKap kap={kap} query={query} /></SectionBoundary>
      <SectionBoundary><Reconciliation kap={kap} /></SectionBoundary>
    </>
  );
}

/* ───────────── Tax calendar ───────────── */

function Calendar({ query }: { query: string }) {
  const calendar = useLoad(() => getJson<TaxCalendar>(`/api/analysis/tax-calendar${query}`));
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

const SECTION_COPY: Record<KapSection, { title: string; note: string }> = {
  domestic: {
    title: 'Income with German Tax Withheld (Lines 7–17)',
    note: 'Copy these lines from each German bank’s Steuerbescheinigung (tax certificate). The amounts marked “Estimate” are this app’s figures to check the certificates against.',
  },
  foreign: {
    title: 'Income Without German Tax Withheld (Lines 18–26)',
    note: 'Foreign brokers issue no German certificate, so you declare this income yourself.',
  },
  taxes: {
    title: 'Tax Withheld and Creditable (Lines 37–42)',
    note: 'Lines 37 to 39 come from your Steuerbescheinigung; line 41 from your foreign broker’s statements.',
  },
  inv: {
    title: 'Anlage KAP-INV: Funds at Foreign Brokers',
    note: 'A separate form for investment funds without German withholding. Enter amounts before the Teilfreistellung (partial exemption); the tax office applies it.',
  },
};

function LineAmount({ line }: { line: KapLine }) {
  if (line.blank) return <span className="text-ink-muted">Leave empty</span>;
  return <Money value={line.amount} cents tone={line.amount < 0 ? 'negative' : undefined} className="text-ink" />;
}

function LineDescription({ line }: { line: KapLine }) {
  return (
    <>
      <span className="text-ink-body" lang="de">{line.description}</span>
      {line.estimate && <Chip className="ml-2 align-middle" title="Copy the figure from your Steuerbescheinigung; this is the app’s estimate">Estimate</Chip>}
      {line.hint && <span className="mt-0.5 block text-[12px] text-ink-muted">{line.hint}</span>}
    </>
  );
}

function KapLines({ section, lines, form }: { section: KapSection; lines: KapLine[]; form: string }) {
  if (lines.length === 0) return null;
  const copy = SECTION_COPY[section];
  const onlyBlank = lines.every(l => l.blank);
  return (
    <>
      <SubHeadingRow>{copy.title}</SubHeadingRow>
      <p className="mb-3 text-[13px] text-ink-body">
        {onlyBlank ? 'No German bank paid you capital income this year. Leave this block empty unless a bank sent you a Steuerbescheinigung.' : copy.note}
      </p>
      <ul className="md:hidden divide-y divide-divider border-y border-divider">
        {lines.map(line => (
          <li key={line.line} className="py-2 text-[13px]">
            <div className="flex items-baseline justify-between gap-4">
              <span className="font-medium text-ink">{form} line {line.line}</span>
              <span className="shrink-0 whitespace-nowrap"><LineAmount line={line} /></span>
            </div>
            <p className="mt-0.5"><LineDescription line={line} /></p>
          </li>
        ))}
      </ul>
      <TableRegion label={`${copy.title} lines`} className="hidden md:block">
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
                <td className="whitespace-nowrap align-top text-ink">{line.line}</td>
                <td className="min-w-[260px]"><LineDescription line={line} /></td>
                <td className="num whitespace-nowrap align-top"><LineAmount line={line} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>
    </>
  );
}

function AnlageKap({ kap, query }: { kap: LoadState<AnlageKAP>; query: string }) {
  const year = kap.data?.year;
  return (
    <Section title={year ? `Anlage KAP ${year}` : 'Anlage KAP'}
      subtitle="Anlage KAP is the capital income form of the German tax return. These are the figures to enter, line by line, with the form’s own wording next to each line number."
      actions={year ? (
        <>
          <a href={`/api/analysis/export-tax?year=${year}${query.replace('?', '&')}`} download className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]">Export CSV</a>
          <a href={`/api/analysis/export-datev?year=${year}`} download className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]" title="DATEV is the format German tax advisers import">Export DATEV</a>
        </>
      ) : undefined}>
      <Loaded state={kap} lines={6}>
        {data => {
          const lines = arr(data?.anlage_kap);
          const invLines = arr(data?.anlage_kap_inv);
          const brokers = arr(data?.brokers);
          if (brokers.length === 0) return <NoKap />;
          const bySection = (s: KapSection) => lines.filter(l => l.section === s);
          const balance = num(data.balance_due);
          const church = num(data.church_tax_rate);
          return (
            <>
              {data.cross_broker_note && (
                <div className="mb-5"><Callout tone="info" label="Note">{data.cross_broker_note}</Callout></div>
              )}
              {data.layout_note && (
                <div className="mb-5"><Callout tone="warning" label="Check the form">{data.layout_note}</Callout></div>
              )}

              <KapLines section="domestic" lines={bySection('domestic')} form="KAP" />
              <KapLines section="foreign" lines={bySection('foreign')} form="KAP" />
              <KapLines section="taxes" lines={bySection('taxes')} form="KAP" />
              <KapLines section="inv" lines={invLines} form="KAP-INV" />

              <SubHeadingRow>What the Tax Office Will Likely Compute</SubHeadingRow>
              <KeyValues rows={[
                ...(num(data.loss_carry_in) < 0 ? [{ label: 'Losses carried in from earlier years', value: <Money value={data.loss_carry_in} cents tone="negative" /> }] : []),
                { label: 'Capital income after loss offsetting', value: <Money value={data.net_taxable} cents /> },
                { label: `Sparer-Pauschbetrag (saver’s allowance${data.joint ? ', joint filing' : ''})`, value: <Money value={data.allowance} /> },
                { label: `Tax on the rest (${church > 0 ? `with ${fmtPct(church * 100, { digits: 0 })} church tax` : 'no church tax'})`, value: <Money value={data.estimated_tax} cents /> },
                { label: 'Already withheld or creditable', value: <Money value={data.total_withheld} cents /> },
                {
                  label: balance >= 0 ? 'Estimated additional tax' : 'Estimated refund',
                  value: <Money value={Math.abs(balance)} cents className="font-medium" />,
                },
              ]} />
              <p className="mt-2 text-[12px] text-ink-muted">
                Same calculation as the Overview: assumes every bank’s losses are declared and includes losses carried in from earlier years. Joint filing and church tax follow the settings on the Overview and Tools tabs.
              </p>

              <SubHeadingRow>By Broker</SubHeadingRow>
              <div className="grid gap-3 md:grid-cols-2">
                {brokers.map((b, i) => {
                  const rows = [
                    { label: 'Dividends and distributions', v: b.dividends },
                    { label: 'Interest', v: b.interest },
                    { label: 'Realised gains', v: b.realized_gains },
                    { label: 'Realised losses', v: b.realized_losses },
                    { label: 'Vorabpauschale', v: b.vorabpauschale },
                    { label: 'Partial exemption', v: b.teilfreistellung },
                    { label: 'German tax withheld', v: b.withheld_tax },
                    { label: 'Foreign tax withheld', v: b.foreign_tax },
                  ].filter(r => num(r.v) !== 0);
                  const domicile = b.domestic ? 'German bank' : b.domicile_assumed ? 'Assumed foreign' : 'Foreign broker';
                  return (
                    <div key={`${b.broker_name}-${i}`} className="rounded-apple border border-divider px-4 py-3">
                      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                        <p className="text-[14px] font-medium text-ink">{b.broker_name}</p>
                        <Chip tone={b.domicile_assumed ? 'warning' : 'neutral'}>{domicile}</Chip>
                      </div>
                      {b.account_name && b.account_name !== b.broker_name && <p className="-mt-1 mb-2 text-[12px] text-ink-muted">{b.account_name}</p>}
                      {rows.length > 0
                        ? <KeyValues rows={rows.map(r => ({ label: r.label, value: <Money value={r.v} cents tone={r.v < 0 ? 'negative' : undefined} /> }))} />
                        : <p className="text-[13px] text-ink-muted">No taxable income this year.</p>}
                      <p className="mt-2 text-[12px] text-ink-muted">
                        {b.domestic
                          ? 'Withholds German tax and issues a Steuerbescheinigung: copy lines 7 to 17 and 37 to 39 from it.'
                          : b.domicile_assumed
                            ? 'The app does not know whether this institution withholds German tax, so it is treated as foreign. If it issued a Steuerbescheinigung, use that instead.'
                            : 'Withholds no German tax: its income goes on lines 19 to 23, funds on Anlage KAP-INV.'}
                      </p>
                    </div>
                  );
                })}
              </div>
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

/**
 * Parses an amount typed in German or English notation.
 * "6.177" → 6177, "6.177,50" → 6177.5, "1,5" → 1.5, "1234.56" → 1234.56, "−692,94" → −692.94.
 * A dot followed by groups of exactly three digits is a thousands separator; with a
 * comma present the comma is the decimal mark. Returns null for empty or unreadable text.
 */
function parseGermanAmount(text: string | undefined): number | null {
  const t = (text ?? '').trim().replace(/[\s  €]/g, '').replace(/[−–]/g, '-');
  if (t === '') return null;
  let normalised: string;
  if (t.includes(',')) {
    if (!/^-?(\d{1,3}(\.\d{3})+|\d*)(,\d+)?$/.test(t)) return null;
    normalised = t.replace(/\./g, '').replace(',', '.');
  } else if (/^-?\d{1,3}(\.\d{3})+$/.test(t)) {
    normalised = t.replace(/\./g, '');
  } else if (/^-?\d*(\.\d+)?$/.test(t)) {
    normalised = t;
  } else {
    return null;
  }
  if (!/\d/.test(normalised)) return null;
  const n = Number(normalised);
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
          // Lines the app cannot estimate (line 16) have nothing to compare against.
          const lines = [
            ...arr(data?.anlage_kap).filter(l => !l.blank).map(line => ({ line, form: 'KAP', key: `${data.year}-Z${line.line}` })),
            ...arr(data?.anlage_kap_inv).filter(l => !l.blank).map(line => ({ line, form: 'KAP-INV', key: `${data.year}-INV-Z${line.line}` })),
          ];
          if (lines.length === 0) return <Statement>Nothing to reconcile until Anlage KAP has figures for the year.</Statement>;
          const checked = lines.map(({ line, form, key }) => {
            const entered = parseGermanAmount(amounts[key]);
            const invalid = (amounts[key] ?? '').trim() !== '' && entered === null;
            // An entered 0 is a figure too; only an empty field is "not entered".
            const diff = entered !== null ? entered - line.amount : 0;
            return { line, form, key, entered, invalid, diff, differs: entered !== null && Math.abs(diff) > 1 };
          });
          const anyEntered = checked.some(c => c.entered !== null);
          const differing = checked.filter(c => c.differs).length;
          return (
            <>
              <ul className="divide-y divide-divider border-y border-divider">
                {checked.map(({ line, form, key, invalid, diff, differs, entered }) => (
                  <li key={key} className="grid gap-x-6 gap-y-2 py-3 md:grid-cols-[1fr_220px] md:items-start">
                    <div className="min-w-0 text-[13px]">
                      <p className="font-medium text-ink">{form} line {line.line}: <span lang="de">{line.description.split('(')[0].trim()}</span></p>
                      <p className="mt-0.5 text-ink-body">This app: <Money value={line.amount} cents className="text-ink" /></p>
                      {differs && (
                        <p className="mt-1 flex flex-wrap items-center gap-2 text-ink-body">
                          <Chip tone="danger">Differs</Chip>
                          Tax office is <Money value={Math.abs(diff)} cents className="font-medium text-ink" /> {diff > 0 ? 'higher' : 'lower'}
                        </p>
                      )}
                      {!differs && entered !== null && (
                        <p className="mt-1 flex items-center gap-2 text-ink-body"><Chip tone="success">Matches</Chip></p>
                      )}
                    </div>
                    <Field label={`Tax office figure, ${form} line ${line.line}`} unit="€" inputMode="decimal" autoComplete="off"
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
