import { useState } from 'react';
import { usePlanning, type PensionSource } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import {
  Section, SubHeading, MetricCard, MetricGrid, NumberField, AddChip, RankedBars,
  EmptyState, ErrorState, useConfirm, type RankedBarItem,
} from '../ui';
import { fmtEUR, fmtPct, fmtAxis } from '../../utils/format';
import { ItemCard, RemoveButton, toNum } from './shared';
import { parsePct } from './planningMath';
import {
  DEFAULT_INFLATION_PCT, DEFAULT_RETURN_PCT, EARLIEST_STATUTORY_AGE, KV_RATE, KV_ZUSATZ_2026, PV_RATE_2026, REGELALTERSGRENZE,
  VERSORGUNGSBEZUG_FREIBETRAG_2026,
} from './constants';

type PensionResult = {
  sources: {
    type: string; label: string; monthly_gross: number; monthly_net: number; start_age: number;
    monthly_income_tax?: number; monthly_health?: number; taxable_share_pct?: number; starts_after_retirement?: boolean;
  }[];
  total_monthly_net: number; portfolio_net: number; projected_portfolio: number;
  monthly_contrib: number; gap: number; gap_annual: number; sparplan_to_close: number;
  gap_after_all_pensions?: number;
  sensitivity?: { age: number; pension_net: number; portfolio_net: number; total_net: number; gap: number }[];
  rentenwert: number;
  return_pct?: number; swr_pct?: number; inflation_pct?: number;
};

// English labels; the German legal term stays as a gloss where it is the name on the paperwork.
const PENSION_TEMPLATES = [
  { type: 'gesetzliche', label: 'Statutory pension', rp: 30 },
  { type: 'bav', label: 'Company pension (bAV)', rp: 0 },
  { type: 'riester', label: 'Riester pension', rp: 0 },
  { type: 'ruerup', label: 'Rürup pension', rp: 0 },
  { type: 'private', label: 'Private pension', rp: 0 },
  { type: 'other', label: 'Other', rp: 0 },
];
const TYPE_LABEL: Record<string, string> = Object.fromEntries(PENSION_TEMPLATES.map(t => [t.type, t.label]));
const sourceLabel = (s: { type: string; label: string }) => TYPE_LABEL[s.type] ?? s.label;
// Short names for the narrow label column of the income bars.
const SHORT_LABEL: Record<string, string> = { gesetzliche: 'Statutory', bav: 'Company', riester: 'Riester', ruerup: 'Rürup', private: 'Private', other: 'Other' };
const shortLabel = (s: { type: string; label: string }) => SHORT_LABEL[s.type] ?? s.label;

/** The response is only stored when it has the shape the result view maps over. */
function isPensionResult(body: unknown): body is PensionResult {
  if (!body || typeof body !== 'object') return false;
  const b = body as Record<string, unknown>;
  return Array.isArray(b.sources)
    && typeof b.gap === 'number'
    && typeof b.total_monthly_net === 'number'
    && typeof b.portfolio_net === 'number'
    && (b.sensitivity === undefined || b.sensitivity === null || Array.isArray(b.sensitivity));
}

// "Pension gap" — Germany's standard retirement-planning lens. Compares
// projected income from gesetzliche Rente / bAV / Riester / Rürup against
// a monthly spending target and surfaces the gap that has to come from
// the private portfolio. Backend at POST /api/portfolio/pension-gap does
// the math (Rentenwert, Besteuerungsanteil, § 32a tariff on the combined
// income, retirees' health insurance); every result is in today's money.
// Return, withdrawal rate and inflation are the FIRE Calculator's.
export default function Rentenluecke() {
  const {
    pensionCurrentAge, pensionAge, pensionNeed, setPensionNeed, pensionContrib, setPensionContrib, pensionSources, setPensionSources,
    projReturn, projData, fireSWR, fireInflation, crChildren, churchRate, joint, contribSource,
  } = usePlanning();
  const tc = useThemeColors();
  const [confirm, confirmDialog] = useConfirm();
  const [result, setResult] = useState<PensionResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // tax_portion_pct 0 means "derive the taxable share from the type and start year".
  const addSource = (t: typeof PENSION_TEMPLATES[number]) => {
    setPensionSources([...pensionSources, {
      type: t.type, label: t.label, monthly_gross: t.type === 'gesetzliche' ? 0 : 500,
      rentenpunkte: t.rp, start_age: t.type === 'gesetzliche' ? Math.max(pensionAge, EARLIEST_STATUTORY_AGE) : pensionAge, tax_portion_pct: 0,
    }]);
  };
  const returnPct = parsePct(projReturn, projData?.return_pct ?? DEFAULT_RETURN_PCT);
  const swr = parsePct(fireSWR, 3.5);
  const inflation = parsePct(fireInflation, DEFAULT_INFLATION_PCT);
  const updateSource = (i: number, patch: Partial<PensionSource>) => {
    setPensionSources(pensionSources.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  };
  const removeSource = async (i: number) => {
    const name = sourceLabel(pensionSources[i]);
    if (!(await confirm({ title: `Remove “${name}”?`, body: 'The values you entered for this pension source will be lost.', confirmLabel: 'Remove source' }))) return;
    setPensionSources(pensionSources.filter((_, j) => j !== i));
  };

  const calculate = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch('/api/portfolio/pension-gap', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sources: pensionSources, retirement_age: pensionAge, monthly_need: pensionNeed, current_age: pensionCurrentAge,
          monthly_contrib: toNum(pensionContrib), source: contribSource, return_pct: returnPct, swr, inflation_pct: inflation,
          children: Math.max(0, Math.round(toNum(crChildren))), church_rate: churchRate, joint,
        }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const body: unknown = await res.json();
      if (!isPensionResult(body)) throw new Error('Unexpected response from the server');
      setResult(body);
    } catch (e) {
      setResult(null);
      setError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setLoading(false);
    }
  };

  const incomeBars: RankedBarItem[] = result
    ? [
      ...result.sources.map((s, i) => ({
        label: `${shortLabel(s)}${result.sources.filter(o => shortLabel(o) === shortLabel(s)).length > 1 ? ` ${i + 1}` : ''}`,
        value: s.monthly_net,
        display: fmtEUR(s.monthly_net),
        share: pensionNeed > 0 ? fmtPct((s.monthly_net / pensionNeed) * 100, { digits: 0 }) : undefined,
      })),
      {
        label: 'Portfolio',
        value: result.portfolio_net,
        display: fmtEUR(result.portfolio_net),
        share: pensionNeed > 0 ? fmtPct((result.portfolio_net / pensionNeed) * 100, { digits: 0 }) : undefined,
      },
    ]
    : [];
  const sensitivity = result?.sensitivity ?? [];

  return (
    <Section
      id="pension-gap"
      title="Pension Gap"
      subtitle="In German, Rentenlücke: the monthly shortfall between your pensions and the income you want in retirement."
    >
      <div className="grid grid-cols-1 gap-x-3 gap-y-4 md:grid-cols-2">
        <NumberField
          label="Desired retirement income"
          unit="€ / month"
          integer
          value={pensionNeed}
          onValueChange={v => setPensionNeed(Math.max(0, v))}
          hint="What you want to live on, after tax, in today’s money."
        />
        <NumberField
          label="Monthly investment until retirement"
          unit="€ / month"
          value={toNum(pensionContrib)}
          onValueChange={v => setPensionContrib(v > 0 ? String(v) : '')}
          hint="Leave at 0 to use your net contributions over the last 12 months."
        />
      </div>
      <p className="mt-3 text-[13px] text-ink-muted">
        Uses age {pensionCurrentAge} and retirement age {pensionAge} from Your Profile, and {fmtPct(returnPct)} return, a {fmtPct(swr)} withdrawal rate and {fmtPct(inflation)} inflation from the FIRE Calculator.
      </p>

      <SubHeading className="mt-6">Pension sources</SubHeading>
      <div className="mt-3 flex flex-wrap gap-2">
        {PENSION_TEMPLATES.map(t => (
          <AddChip key={t.type} onClick={() => addSource(t)}>{t.label}</AddChip>
        ))}
      </div>

      {pensionSources.length === 0 && !result && (
        <div className="mt-4">
          <EmptyState
            title="No pension sources yet"
            message="Add the pensions you expect, then calculate the gap. Your yearly pension statement (Renteninformation) lists your pension points."
            action={{ label: 'Add statutory pension', onClick: () => addSource(PENSION_TEMPLATES[0]) }}
          />
        </div>
      )}

      {pensionSources.length > 0 && (
        <div className="mt-4 flex flex-col gap-3">
          {pensionSources.map((src, i) => (
            <ItemCard key={i}>
              <div className="mb-3 flex items-center justify-between gap-3">
                <span className="min-w-0 text-[15px] font-medium text-ink">{sourceLabel(src)}</span>
                <RemoveButton name={sourceLabel(src)} onClick={() => removeSource(i)} />
              </div>
              <div className="grid grid-cols-1 gap-x-3 gap-y-4 sm:grid-cols-2">
                {src.type === 'gesetzliche' ? (
                  <NumberField
                    label="Pension points"
                    value={src.rentenpunkte}
                    onValueChange={v => updateSource(i, { rentenpunkte: Math.max(0, v) })}
                    hint="Rentenpunkte (Entgeltpunkte) from your Renteninformation."
                  />
                ) : (
                  <NumberField
                    label="Monthly pension, before tax"
                    unit="€ / month"
                    value={src.monthly_gross}
                    onValueChange={v => updateSource(i, { monthly_gross: Math.max(0, v) })}
                  />
                )}
                <NumberField
                  label="Starts at age"
                  unit="years"
                  integer
                  value={src.start_age}
                  onValueChange={v => updateSource(i, { start_age: Math.max(0, v) })}
                  hint={src.type === 'gesetzliche'
                    ? `Not before ${EARLIEST_STATUTORY_AGE}; 0,3 % less for each month before ${REGELALTERSGRENZE}, 0,5 % more after.`
                    : undefined}
                />
              </div>
            </ItemCard>
          ))}

          <button type="button" onClick={calculate} className="apple-btn-primary min-h-[44px] w-full md:w-auto md:self-start" disabled={loading}>
            {loading ? 'Calculating…' : 'Calculate pension gap'}
          </button>
        </div>
      )}

      {error && (
        <div className="mt-4">
          <ErrorState message="The pension gap couldn’t be calculated." details={error} onRetry={calculate} />
        </div>
      )}

      {result && (
        <div className="mt-6">
          <MetricGrid>
            <MetricCard
              label="Monthly gap, after tax"
              money={result.gap > 0}
              tone={result.gap > 0 ? 'negative' : 'positive'}
              value={result.gap > 0 ? fmtEUR(result.gap) : 'Covered'}
              sublabel={<>
                Against <span data-privacy="blur">{fmtEUR(pensionNeed)}</span> a month from age {pensionAge}, in today’s money
                {result.gap_after_all_pensions != null && Math.abs(result.gap_after_all_pensions - result.gap) >= 1 && (
                  <>; <span data-privacy="blur">{result.gap_after_all_pensions > 0 ? fmtEUR(result.gap_after_all_pensions) : 'covered'}</span> once every pension has started</>
                )}
              </>}
            />
            <MetricCard
              label="Extra savings needed"
              money={result.sparplan_to_close > 0}
              value={result.sparplan_to_close > 0 ? fmtEUR(result.sparplan_to_close) : 'None'}
              sublabel="A month until retirement, to close the gap"
            />
            <MetricCard
              label="Projected portfolio"
              value={fmtEUR(result.projected_portfolio)}
              sublabel={result.monthly_contrib > 0
                ? <>At retirement in today’s money, investing <span data-privacy="blur">{fmtEUR(result.monthly_contrib)}</span> a month</>
                : 'At retirement in today’s money, without further investing'}
            />
            <MetricCard
              label="Portfolio withdrawal"
              value={fmtEUR(result.portfolio_net)}
              sublabel="A month, after tax on the gain, in today’s money"
            />
          </MetricGrid>

          <SubHeading className="mt-6">Monthly income after tax, against your target</SubHeading>
          <p className="mt-1 mb-3 text-[13px] text-ink-muted">
            Share of the desired retirement income each source covers at age {pensionAge}.
            {result.sources.some(s => s.starts_after_retirement) && <> Sources that start later count as 0 here.</>}
          </p>
          <RankedBars items={incomeBars} scale="max" />

          {sensitivity.length > 0 && (
            <div className="mt-6">
              <SubHeading>If you retired at a different age</SubHeading>
              <p className="mt-1 text-[13px] text-ink-muted">
                Monthly income after tax in the first year of retirement, by retirement age; the dashed line is the remaining gap.
                The statutory pension starts at {EARLIEST_STATUTORY_AGE} at the earliest, with up to 14,4 % less; your pension points may also be fewer if you stop earlier.
              </p>
              <EChartWrapper
                option={{
                  tooltip: { trigger: 'axis' as const, valueFormatter: (v: unknown) => fmtEUR(Number(v)) },
                  legend: {
                    bottom: 0,
                    itemWidth: 16,
                    data: [
                      { name: 'Pensions', icon: 'rect' },
                      { name: 'Portfolio withdrawals', icon: 'rect' },
                      { name: 'Remaining gap', icon: 'path://M0 4h5v2h-5zM7 4h5v2h-5zM14 4h5v2h-5z' },
                    ],
                  },
                  grid: { top: 28, right: 12, bottom: 76, left: 48 },
                  xAxis: { type: 'category' as const, data: sensitivity.map(s => String(s.age)), axisLabel: { fontSize: 11 } },
                  yAxis: { type: 'value' as const, name: '€ / month', min: 0, axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
                  series: [
                    { name: 'Pensions', type: 'bar' as const, stack: 'income', data: sensitivity.map(s => s.pension_net), color: tc.forest, itemStyle: { color: tc.forest } },
                    { name: 'Portfolio withdrawals', type: 'bar' as const, stack: 'income', data: sensitivity.map(s => s.portfolio_net), color: tc.gold, itemStyle: { color: tc.gold } },
                    { name: 'Remaining gap', type: 'line' as const, data: sensitivity.map(s => Math.max(s.gap, 0)), color: tc.claret, itemStyle: { color: tc.claret }, lineStyle: { color: tc.claret, type: 'dashed' as const, width: 2 }, symbol: 'none' },
                  ],
                }}
                height="260px"
              />
            </div>
          )}

          <p className="mt-4 text-[13px] text-ink-muted">
            Estimate only, in today’s money. Pensions: one pension point is worth {fmtEUR(result.rentenwert, { cents: true })} a month; income tax uses the 2026 tariff on all pensions together, with the taxable share set by the start year (statutory, Rürup) or age (private annuity), and company and Riester pensions taxed in full.
            Retirees’ health and care insurance: about {fmtPct(((KV_RATE + KV_ZUSATZ_2026) / 2 + PV_RATE_2026) * 100, { digits: 2 })} on the statutory pension, the full rate on company pensions above {fmtEUR(VERSORGUNGSBEZUG_FREIBETRAG_2026, { cents: true })} a month, none on private, Riester and Rürup pensions.
            The portfolio withdrawal is taxed only on its gain. The result reflects the inputs at the time you pressed Calculate.
          </p>
        </div>
      )}
      {confirmDialog}
    </Section>
  );
}
