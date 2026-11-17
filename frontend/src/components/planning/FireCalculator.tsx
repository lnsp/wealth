import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import { Section, SubHeading, MetricCard, MetricGrid, NumberField, ProgressBar, EmptyState, ErrorState } from '../ui';
import { fmtEUR, fmtPct, fmtNum, fmtAxis } from '../../utils/format';
import { ScrollTable, monthLabel, toNum } from './shared';

// "Financial Independence" calculator — solves for the FIRE number
// (annual expenses / withdrawal rate), shows progress toward it, and projects
// years remaining given the monthly contribution / return assumptions from projData.
// When projData includes a drawdown schedule (returned when expenses>0),
// renders the retirement-phase chart + tax breakdown below the headline.
export default function FireCalculator() {
  const {
    totalNetWorth, projData, projFailed, reloadProjection, projDefaultContrib,
    projContrib, setProjContrib, projReturn, setProjReturn,
    fireExpenses, setFireExpenses, fireSWR, setFireSWR,
    pensionIncome, setPensionIncome, otherIncome, setOtherIncome,
    pensionCurrentAge,
  } = usePlanning();
  const tc = useThemeColors();
  const [editContrib, setEditContrib] = useState(false);

  const title = 'FIRE Calculator';
  const subtitle = 'Financial independence: the point where investment income covers your expenses.';

  if (totalNetWorth <= 0) {
    return (
      <Section id="fire" title={title} subtitle={subtitle}>
        <EmptyState
          title="No net worth to plan from yet"
          message="The calculator starts from your current net worth. Import transactions or add an account first."
          action={{ label: 'Go to Settings', href: '/settings' }}
        />
      </Section>
    );
  }

  const expenses = parseFloat(fireExpenses) || 0;
  const swr = parseFloat(fireSWR) || 3.5;
  const fireNumber = expenses > 0 ? expenses / (swr / 100) : 0;
  const totalOtherIncome = (parseFloat(pensionIncome) || 0) + (parseFloat(otherIncome) || 0);
  const withdrawalGap = Math.max(expenses - totalOtherIncome, 0);
  const adjustedFireNumber = withdrawalGap > 0 ? withdrawalGap / (swr / 100) : 0;
  const targetFireNumber = totalOtherIncome > 0 ? adjustedFireNumber : fireNumber;

  const progress = expenses > 0
    ? (targetFireNumber <= 0 ? 100 : Math.min((totalNetWorth / targetFireNumber) * 100, 100))
    : 0;
  const monthlyContrib = projData ? (parseFloat(projContrib) || projData.contribution) : 0;
  const parsedReturn = parseFloat(projReturn);
  const annualReturn = !Number.isNaN(parsedReturn) ? parsedReturn : (projData ? projData.return_pct : 7);
  const monthlyReturn = Math.pow(Math.max(0, 1 + annualReturn / 100), 1 / 12) - 1;

  // Years-to-FIRE: solve for n where FV*(1+r)^n + PMT*((1+r)^n - 1)/r ≥ targetFireNumber.
  // Iterate month-by-month so r === 0 and PMT === 0 both work cleanly; 600 = 50yr cap.
  let yearsToFire = 0;
  let yearsCapped = false;
  if (targetFireNumber > totalNetWorth && (monthlyReturn > 0 || monthlyContrib > 0)) {
    let fv = totalNetWorth;
    let reached = false;
    for (let m = 1; m <= 600; m++) {
      fv = fv * (1 + monthlyReturn) + monthlyContrib;
      if (fv >= targetFireNumber) {
        yearsToFire = Math.ceil(m / 12);
        reached = true;
        break;
      }
    }
    if (!reached) {
      yearsToFire = 50;
      yearsCapped = true;
    }
  }

  const achieved = progress >= 100;
  const within50 = !achieved && !yearsCapped && yearsToFire > 0;
  const usesAverage = projDefaultContrib != null && Math.abs(monthlyContrib - projDefaultContrib) < 0.5;
  const drawdown = projData?.drawdown;
  const taxRows = drawdown?.tax_breakdown
    ? drawdown.tax_breakdown.filter((_, i, all) => i % 5 === 0 || i === all.length - 1)
    : [];

  return (
    <Section id="fire" title={title} subtitle={subtitle}>
      <div className="grid grid-cols-2 gap-x-3 gap-y-4 md:grid-cols-4">
        <NumberField
          className="col-span-2"
          label="Annual expenses"
          unit="€ / year"
          value={expenses}
          onValueChange={v => setFireExpenses(String(Math.max(0, v)))}
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
          value={toNum(pensionIncome)}
          onValueChange={v => setPensionIncome(String(Math.max(0, v)))}
          hint="Lowers the amount your portfolio has to cover."
        />
        <NumberField
          className="col-span-2"
          label="Other income in retirement"
          unit="€ / year"
          value={toNum(otherIncome)}
          onValueChange={v => setOtherIncome(String(Math.max(0, v)))}
          hint="Rent, part-time work and similar."
        />
      </div>

      {expenses === 0 && (
        <p className="mt-4 text-[14px] text-ink-body">
          Enter your annual expenses to see your target portfolio and when you would reach it.
        </p>
      )}

      {fireNumber > 0 && (
        <div className="mt-5">
          <MetricGrid>
            <MetricCard
              label="Target portfolio"
              value={fmtEUR(targetFireNumber)}
              sublabel={totalOtherIncome > 0
                ? <span data-privacy="blur">({fmtEUR(expenses)} − {fmtEUR(totalOtherIncome)} income) ÷ {fmtPct(swr)}</span>
                : <span data-privacy="blur">{fmtEUR(expenses)} ÷ {fmtPct(swr)}</span>}
            />
            <MetricCard
              label="Reached in"
              money={false}
              tone={achieved ? 'positive' : 'neutral'}
              value={achieved ? 'Reached' : within50 ? `about ${fmtNum(yearsToFire)} years` : 'over 50 years'}
              sublabel={within50 ? `At age ${pensionCurrentAge + yearsToFire}` : achieved ? 'Your net worth covers the target' : 'Not within 50 years at these assumptions'}
            />
            <MetricCard
              label={achieved ? 'Surplus' : 'Still to go'}
              value={fmtEUR(Math.abs(targetFireNumber - totalNetWorth))}
              sublabel={achieved ? 'Above the target portfolio' : 'Target minus net worth today'}
            />
            {totalOtherIncome > 0 && (
              <MetricCard
                label="Yearly withdrawal needed"
                value={fmtEUR(withdrawalGap)}
                sublabel="Expenses minus pension and other income"
              />
            )}
          </MetricGrid>

          <div className="mt-4">
            <ProgressBar value={progress} label="Progress to target portfolio" />
            <p className="mt-1.5 text-[13px] text-ink-body">
              <span data-privacy="blur">{fmtEUR(totalNetWorth)}</span> today · {fmtPct(progress)} of target
            </p>
          </div>

          {projFailed && !projData ? (
            <div className="mt-3">
              <ErrorState message="Your average contribution couldn’t be loaded, so the time to target assumes no contributions." onRetry={reloadProjection} />
            </div>
          ) : (
            <div className="mt-2 flex flex-wrap items-center justify-between gap-x-4">
              <p className="min-w-0 flex-1 basis-56 text-[13px] text-ink-body">
                {usesAverage
                  ? <>Assumes contributions stay at <span data-privacy="blur">{fmtEUR(monthlyContrib)}</span> a month, your 12-month average.</>
                  : <>Assumes contributions of <span data-privacy="blur">{fmtEUR(monthlyContrib)}</span> a month{projDefaultContrib != null && <> (your 12-month average is <span data-privacy="blur">{fmtEUR(projDefaultContrib)}</span>)</>}.</>}
                {' '}Growth of {fmtPct(annualReturn)} a year, no inflation adjustment.
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
          )}
          {editContrib && (
            <div id="fire-contrib" className="mt-2 grid grid-cols-1 gap-3 md:grid-cols-2">
              <NumberField
                label="Monthly contributions"
                unit="€ / month"
                value={monthlyContrib}
                onValueChange={v => setProjContrib(String(Math.max(0, v)))}
                hint={projDefaultContrib != null && !usesAverage
                  ? (
                    <button type="button" className="underline text-forest min-h-[32px]" onClick={() => setProjContrib(String(projDefaultContrib))}>
                      Reset to your 12-month average
                    </button>
                  )
                  : 'Deposits into your accounts, averaged over the last 12 months.'}
              />
            </div>
          )}
        </div>
      )}

      {drawdown && (
        <div className="mt-8">
          <SubHeading>Retirement phase</SubHeading>
          <MetricGrid className="mt-3">
            <MetricCard label="Independence date" money={false} value={monthLabel(drawdown.fire_date)} />
            <MetricCard
              label="Portfolio lasts"
              money={false}
              value={drawdown.longevity_years >= 40 ? 'over 40 years' : `${fmtNum(drawdown.longevity_years)} years`}
              sublabel="After the independence date"
            />
            <MetricCard label="Monthly withdrawal" value={fmtEUR(withdrawalGap / 12)} sublabel="Before tax" />
            <MetricCard
              label="Coverage"
              money={false}
              tone={drawdown.success_rate >= 100 ? 'positive' : drawdown.success_rate >= 80 ? 'neutral' : 'negative'}
              value={fmtPct(drawdown.success_rate, { digits: 0 })}
              sublabel={drawdown.success_rate >= 100 ? 'Covers a full 40-year retirement' : 'Share of a 40-year retirement the portfolio covers'}
            />
          </MetricGrid>

          {drawdown.series.length > 2 && (
            <div className="mt-5">
              <SubHeading>Portfolio value after the independence date</SubHeading>
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
                Every fifth year. Total tax over the whole period: <span className="text-ink font-medium" data-privacy="blur">{fmtEUR(drawdown.cumulative_tax)}</span>.
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
        </div>
      )}
    </Section>
  );
}
