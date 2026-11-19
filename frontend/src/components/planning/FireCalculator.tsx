import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import { Section, SubHeading, MetricCard, MetricGrid, NumberField, ProgressBar, EmptyState, ErrorState, LoadingState } from '../ui';
import { fmtEUR, fmtPct, fmtNum, fmtAxis } from '../../utils/format';
import { Assumptions, ScrollTable, monthLabel, toNum } from './shared';
import { parsePct } from './planningMath';
import UnlinkedAssetPaymentsNote from './UnlinkedAssetPaymentsNote';
import { DEFAULT_INFLATION_PCT, DEFAULT_RETURN_PCT } from './constants';

// "Financial Independence" calculator. The target, the independence date and
// the retirement phase all come from one backend model (GET
// /portfolio/projection with expenses): accumulate investable wealth with the
// trailing 12-month net contribution, stop when the portfolio covers the
// spending gap grossed up for flat tax on the gain share (after the 30 %
// Teilfreistellung and the saver's allowance) at the withdrawal rate — plus a
// reserve for the years before the pension starts — then withdraw an
// inflation-indexed amount for up to 40 years. Everything is in today's money.
export default function FireCalculator() {
  const {
    investableNetWorth, projData, projFailed, reloadProjection, projDefaultContrib,
    projContrib, setProjContrib, projReturn, setProjReturn,
    fireExpenses, setFireExpenses, fireSWR, setFireSWR,
    pensionIncome, setPensionIncome, otherIncome, setOtherIncome,
    fireInflation, setFireInflation,
    pensionCurrentAge, pensionAge, income, contribSource, setContribSource,
  } = usePlanning();
  const hasIncome = income?.has_income ?? false;
  const tc = useThemeColors();
  const [editContrib, setEditContrib] = useState(false);

  const title = 'FIRE Calculator';
  const subtitle = 'Financial independence: the point where your investments and pension cover your expenses.';

  const monthlyContrib = projData ? parsePct(projContrib, projData.contribution) : 0;
  if (investableNetWorth <= 0 && monthlyContrib <= 0) {
    return (
      <Section id="fire" title={title} subtitle={subtitle}>
        <EmptyState
          title="No cash or investments to plan from yet"
          message="The calculator starts from your cash and securities. Import transactions or add an account first."
          action={{ label: 'Go to Settings', href: '/settings' }}
        />
      </Section>
    );
  }

  const expenses = toNum(fireExpenses);
  const swr = parsePct(fireSWR, 3.5);
  const inflation = parsePct(fireInflation, DEFAULT_INFLATION_PCT);
  const pension = toNum(pensionIncome);
  const other = toNum(otherIncome);
  const portfolioSpend = Math.max(expenses - other, 0);
  const annualReturn = parsePct(projReturn, projData?.return_pct ?? DEFAULT_RETURN_PCT);

  const drawdown = projData?.drawdown;
  const target = drawdown?.fire_number ?? 0;
  const monthsToFire = drawdown?.months_to_fire ?? (drawdown?.fire_date ? drawdown.years_to_fire * 12 : -1);
  const reached = drawdown != null && monthsToFire >= 0;
  const achieved = reached && monthsToFire === 0;
  const progress = target > 0 ? Math.min((investableNetWorth / target) * 100, 100) : (achieved ? 100 : 0);
  const yearsToFire = Math.round(monthsToFire / 12);
  const ageAtFire = pensionCurrentAge + Math.floor(monthsToFire / 12);
  const bridgeReserve = drawdown?.bridge_reserve ?? 0;
  const usesAverage = projDefaultContrib != null && Math.abs(monthlyContrib - projDefaultContrib) < 0.5;
  const taxRows = drawdown?.tax_breakdown
    ? drawdown.tax_breakdown.filter((_, i, all) => i % 5 === 0 || i === all.length - 1)
    : [];
  const longevity = drawdown?.longevity_years ?? 0;

  return (
    <Section id="fire" title={title} subtitle={subtitle}>
      <div className="grid grid-cols-2 gap-x-3 gap-y-4 md:grid-cols-4">
        <NumberField
          className="col-span-2"
          label="Annual expenses"
          unit="€ / year"
          value={expenses}
          onValueChange={v => setFireExpenses(String(Math.max(0, v)))}
          hint={hasIncome
            ? <>In today’s money, after tax. Your running costs on the Income tab: <span data-privacy="blur">{fmtEUR(income?.summary.fire_expenses_today ?? 0)}</span>{Math.round(income?.summary.fire_expenses_today ?? 0) !== Math.round(expenses) && (
              <> · <button type="button" className="underline text-forest min-h-[32px]" onClick={() => setFireExpenses(String(Math.round(income?.summary.fire_expenses_today ?? 0)))}>use them</button></>
            )}</>
            : 'In today’s money, after tax.'}
        />
        <NumberField
          label="Withdrawal rate"
          unit="%"
          value={swr}
          onValueChange={v => setFireSWR(String(v))}
          hint="Share of the portfolio you draw each year."
        />
        <NumberField
          label="Expected return"
          unit="% p.a."
          value={annualReturn}
          onValueChange={v => setProjReturn(String(v))}
          hint="Before inflation."
        />
        <NumberField
          className="col-span-2"
          label="Pension income in retirement"
          unit="€ / year"
          value={pension}
          onValueChange={v => setPensionIncome(String(Math.max(0, v)))}
          hint={`After tax, in today’s money, from age ${pensionAge} (retirement age in Your Profile).`}
        />
        <NumberField
          label="Other income in retirement"
          unit="€ / year"
          value={other}
          onValueChange={v => setOtherIncome(String(Math.max(0, v)))}
          hint="Rent, part-time work; from the independence date."
        />
        <NumberField
          label="Inflation"
          unit="% p.a."
          value={inflation}
          onValueChange={v => setFireInflation(String(v))}
        />
      </div>

      {expenses === 0 && (
        <p className="mt-4 text-[14px] text-ink-body">
          Enter your annual expenses to see your target portfolio and when you would reach it.
        </p>
      )}

      {expenses > 0 && portfolioSpend === 0 && (
        <p className="mt-4 text-[14px] text-ink-body">
          Your other income covers your expenses, so no portfolio is needed for them.
        </p>
      )}

      {portfolioSpend > 0 && !drawdown && (
        projFailed
          ? <div className="mt-4"><ErrorState message="The target couldn’t be calculated." onRetry={reloadProjection} /></div>
          : <div className="mt-4"><LoadingState message="Calculating your target" lines={2} /></div>
      )}

      {portfolioSpend > 0 && drawdown && (
        <div className="mt-5">
          <MetricGrid>
            <MetricCard
              label="Target portfolio"
              value={fmtEUR(target)}
              sublabel={bridgeReserve > 0
                ? <>In today’s money, incl. <span data-privacy="blur">{fmtEUR(bridgeReserve)}</span> for the {fmtNum(drawdown.bridge_years ?? 0, 1)} years before your pension</>
                : 'In today’s money, enough to pay the tax on withdrawals'}
            />
            <MetricCard
              label="Reached in"
              money={false}
              tone={achieved ? 'positive' : 'neutral'}
              value={achieved ? 'Reached' : reached ? `about ${fmtNum(yearsToFire)} ${yearsToFire === 1 ? 'year' : 'years'}` : 'over 50 years'}
              sublabel={achieved
                ? 'Your cash and investments cover the target'
                : reached ? `${monthLabel(drawdown.fire_date)}, at age ${ageAtFire}` : 'Not within 50 years at these assumptions'}
            />
            <MetricCard
              label={achieved ? 'Surplus' : 'Still to go'}
              value={fmtEUR(Math.abs(target - investableNetWorth))}
              sublabel={achieved ? 'Above the target portfolio' : 'Target minus cash and investments today'}
            />
            {(pension > 0 || other > 0) && (
              <MetricCard
                label="Spending the portfolio covers"
                value={fmtEUR(drawdown.spending_gap ?? portfolioSpend)}
                sublabel={pension > 0
                  ? <>A year once the pension starts; <span data-privacy="blur">{fmtEUR(portfolioSpend)}</span> before</>
                  : 'A year: expenses minus other income'}
              />
            )}
          </MetricGrid>

          <div className="mt-4">
            <ProgressBar value={progress} label="Progress to target portfolio" />
            <p className="mt-1.5 text-[13px] text-ink-body">
              <span data-privacy="blur">{fmtEUR(investableNetWorth)}</span> in cash and investments today · {fmtPct(progress)} of target.
              Your home, car and loans are left out.
            </p>
          </div>

          {(projData?.post_fire_health ?? 0) > 0 && (
            <p className="mt-3 text-[13px] text-ink-body">
              Includes <span data-privacy="blur">{fmtEUR(projData?.post_fire_health ?? 0)}</span> a year for health and care insurance after FIRE, until your pension starts:
              {income?.profile.health_kind === 'pkv'
                ? ' your private premium without the employer subsidy.'
                : ' voluntary statutory insurance on the gains in your withdrawals, never less than the minimum.'}
            </p>
          )}

          {hasIncome && (
            <div className="mt-4 flex flex-col gap-2">
              <span className="text-[13px] text-ink-body">Contributions</span>
              <div role="group" aria-label="Contributions" className="inline-flex flex-wrap gap-1.5">
                {([['income', 'Expected income'], ['trailing', 'Last 12 months'], ['custom', 'Custom']] as const).map(([id, label]) => (
                  <button key={id} type="button" aria-pressed={contribSource === id}
                    onClick={() => { setContribSource(id); setEditContrib(id === 'custom'); }}
                    className={`min-h-[44px] px-3.5 rounded-[4px] border text-[14px] transition-colors ${contribSource === id
                      ? 'bg-forest border-forest text-parchment font-semibold'
                      : 'border-field text-ink-body font-medium hover:border-forest hover:text-ink'}`}>
                    {label}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div className="mt-2 flex flex-wrap items-center justify-between gap-x-4">
            <p className="min-w-0 flex-1 basis-56 text-[13px] text-ink-body">
              {contribSource === 'income' && projData?.contribution_source === 'income'
                ? <>Contributions follow your monthly schedule from the Income tab, <span data-privacy="blur">{fmtEUR(projData.contribution)}</span> a month on average in the first year, and stop at the independence date.{' '}</>
                : null}
              {contribSource === 'income' && projData?.contribution_source === 'income' ? null : usesAverage
                ? <>Assumes you keep adding <span data-privacy="blur">{fmtEUR(monthlyContrib)}</span> a month, your net contributions over the last 12 months.</>
                : <>Assumes contributions of <span data-privacy="blur">{fmtEUR(monthlyContrib)}</span> a month{projDefaultContrib != null && <> (your last 12 months: <span data-privacy="blur">{fmtEUR(projDefaultContrib)}</span> a month)</>}.</>}
              {' '}Growth of {fmtPct(annualReturn)} a year before inflation; amounts in today’s money at {fmtPct(inflation)} inflation.
              {usesAverage && <UnlinkedAssetPaymentsNote payments={projData?.contribution_basis?.unlinked_asset_payments} />}
            </p>
            <button
              type="button"
              aria-expanded={editContrib}
              aria-controls="fire-contrib"
              onClick={() => setEditContrib(v => !v)}
              className="min-h-[44px] px-2 -mr-2 text-[14px] font-semibold text-forest underline underline-offset-2"
            >
              {editContrib ? 'Done' : 'Change'}
            </button>
          </div>
          {editContrib && (
            <div id="fire-contrib" className="mt-2 grid grid-cols-1 gap-3 md:grid-cols-2">
              <NumberField
                label="Monthly contributions"
                unit="€ / month"
                value={monthlyContrib}
                onValueChange={v => { setProjContrib(String(Math.max(0, v))); if (hasIncome) setContribSource('custom'); }}
                hint={projDefaultContrib != null && !usesAverage
                  ? (
                    <button type="button" className="underline text-forest min-h-[32px]" onClick={() => setProjContrib(String(projDefaultContrib))}>
                      Reset to your last 12 months
                    </button>
                  )
                  : 'Deposits minus withdrawals, plus share vests, averaged over the last 12 months.'}
              />
            </div>
          )}
        </div>
      )}

      {portfolioSpend > 0 && drawdown && reached && (
        <div className="mt-8">
          <SubHeading>Retirement phase</SubHeading>
          <MetricGrid className="mt-3">
            <MetricCard label="Independence date" money={false} value={monthLabel(drawdown.fire_date)} />
            <MetricCard
              label="Portfolio lasts"
              money={false}
              value={longevity >= 40 ? 'over 40 years' : `${fmtNum(longevity)} years`}
              sublabel="After the independence date, withdrawals rising with inflation"
            />
            <MetricCard
              label="Monthly withdrawal"
              value={fmtEUR((drawdown.first_year_withdrawal ?? 0) / 12)}
              sublabel={<>Before tax, first year; about <span data-privacy="blur">{fmtEUR((drawdown.first_year_tax ?? 0) / 12)}</span> of it is tax</>}
            />
            <MetricCard
              label="Years funded"
              money={false}
              tone={drawdown.success_rate >= 100 ? 'positive' : drawdown.success_rate >= 80 ? 'neutral' : 'negative'}
              value={longevity >= 40 ? 'All 40' : `${fmtNum(longevity)} of 40`}
              sublabel="Steady returns with no market swings; not a probability of success"
            />
          </MetricGrid>

          {drawdown.series.length > 2 && (
            <div className="mt-5">
              <SubHeading>Portfolio value after the independence date, in today’s money</SubHeading>
              <EChartWrapper
                option={{
                  color: [tc.sage],
                  grid: { left: 52, right: 16, top: 28, bottom: 30 },
                  xAxis: {
                    type: 'category' as const,
                    data: drawdown.series.map(s => monthLabel(s.date, true)),
                    axisLabel: { fontSize: 11, interval: Math.max(Math.floor(drawdown.series.length / 6) - 1, 0) },
                  },
                  yAxis: { type: 'value' as const, name: '€', axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
                  series: [{
                    name: 'Portfolio value',
                    type: 'line' as const,
                    data: drawdown.series.map(s => s.value),
                    smooth: true,
                    symbol: 'none',
                    color: tc.sage,
                    lineStyle: { color: tc.sage, width: 2 },
                    itemStyle: { color: tc.sage },
                    areaStyle: { color: tc.sage, opacity: 0.12 },
                  }],
                  tooltip: { trigger: 'axis' as const, valueFormatter: (v: unknown) => fmtEUR(Number(v)) },
                }}
                height="220px"
              />
            </div>
          )}

          {taxRows.length > 0 && (
            <div className="mt-5">
              <SubHeading>Tax during withdrawal</SubHeading>
              <p className="mt-1 mb-3 text-[13px] text-ink-muted">
                Every fifth year, in today’s money. Total tax over the whole period: <span className="text-ink font-medium" data-privacy="blur">{fmtEUR(drawdown.cumulative_tax)}</span>.
              </p>
              <ScrollTable label="Tax during withdrawal, by year">
                <table className="apple-table min-w-[480px]">
                  <thead>
                    <tr>
                      <th scope="col">Year</th>
                      <th scope="col" className="num">Withdrawn</th>
                      <th scope="col" className="num">Tax</th>
                      <th scope="col" className="num">Received</th>
                      <th scope="col" className="num">Tax rate</th>
                    </tr>
                  </thead>
                  <tbody>
                    {taxRows.map(t => (
                      <tr key={t.year}>
                        <td>{t.year}</td>
                        <td className="num" data-privacy="blur">{fmtEUR(t.gross_withdrawal)}</td>
                        <td className="num" data-privacy="blur">{fmtEUR(t.estimated_tax)}</td>
                        <td className="num" data-privacy="blur">{fmtEUR(t.net_received)}</td>
                        <td className="num">{fmtPct(t.effective_rate_pct)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </ScrollTable>
            </div>
          )}

          <Assumptions>
            <p>
              Only the gain in each sale is taxed: {fmtPct(drawdown.tax_rate_pct ?? 26.375, { digits: 3 })} flat tax
              {(drawdown.teilfreistellung_pct ?? 0) > 0 && <> after the {fmtPct(drawdown.teilfreistellung_pct ?? 0, { digits: 0 })} partial exemption for funds (your current mix)</>}
              {' '}and the <span data-privacy="blur">{fmtEUR(drawdown.allowance ?? 1000)}</span> saver’s allowance. Gains are {fmtPct(drawdown.gain_share_pct ?? 0, { digits: 0 })} of the portfolio at the independence date, counting today’s value and every contribution as paid in.
            </p>
            <p>
              {projData?.contribution_source === 'income'
                ? 'Contributions follow the monthly schedule from the Income tab until the independence date; withdrawals rise with inflation.'
                : 'Contributions stay at the same amount in euros; withdrawals rise with inflation.'}
              {(drawdown.pension_from_age ?? 0) > 0 && <> Until the pension starts at {drawdown.pension_from_age}, the portfolio pays all your spending.</>}
            </p>
          </Assumptions>
        </div>
      )}
    </Section>
  );
}
