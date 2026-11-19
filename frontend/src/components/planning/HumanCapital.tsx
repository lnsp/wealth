import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import { Section, SubHeading, MetricCard, MetricGrid, NumberField, EmptyState } from '../ui';
import { fmtEUR, fmtAxis } from '../../utils/format';
import { toNum } from './shared';
import { humanCapitalCrossover, humanCapitalPV, scheduleByYear } from './planningMath';

// Human Capital = present value of future earnings: this year's gross income,
// growing at a salary growth rate (default 2 %) from next year on, discounted
// (default 3 %). The crossover chart shows when financial capital (cash and
// investments plus the monthly budget, default 7 % growth) overtakes the
// remaining human capital. The same function and inputs drive the Playbook's
// milestone. Income, ages and the monthly budget come from Your Profile.
export default function HumanCapital() {
  const {
    investableNetWorth, crGrossIncome, pensionCurrentAge, pensionAge, crBudget,
    hcSalaryGrowth: salaryGrowthPct, setHcSalaryGrowth: setSalaryGrowthPct,
    hcDiscount: discountPct, setHcDiscount: setDiscountPct,
    hcPortfolioGrowth: portfolioGrowthPct, setHcPortfolioGrowth: setPortfolioGrowthPct, income,
  } = usePlanning();
  const schedule = scheduleByYear(income);
  const tc = useThemeColors();

  const grossIncome = toNum(crGrossIncome);
  const currentAge = pensionCurrentAge;
  const retireAge = pensionAge;
  const yearsToRetire = Math.max(0, retireAge - currentAge);
  const discountRate = discountPct / 100;
  const growthRate = salaryGrowthPct / 100;
  const monthlyContrib = toNum(crBudget);

  const ready = grossIncome > 0 && yearsToRetire > 0;
  const humanCapital = ready ? Math.round(humanCapitalPV(grossIncome, growthRate, discountRate, yearsToRetire, 0, schedule.incomes)) : 0;
  const financialCapital = investableNetWorth;
  const crossover = ready
    ? humanCapitalCrossover({
      income: grossIncome, growth: growthRate, discount: discountRate, years: yearsToRetire,
      startWealth: financialCapital, monthlyContrib, portfolioGrowth: portfolioGrowthPct / 100, ...schedule,
    })
    : { series: [], crossoverIndex: null };
  const crossoverData = crossover.series.map(d => ({ age: currentAge + d.yearIndex, human: Math.round(d.human), financial: Math.round(d.financial) }));
  const crossoverAge = crossover.crossoverIndex != null ? currentAge + crossover.crossoverIndex : undefined;
  return (
    <Section
      id="human-capital"
      title="Human Capital"
      subtitle="What your future earnings are worth today, compared with the money you have already built up."
    >
      {!ready ? (
        <EmptyState
          title={grossIncome <= 0 ? 'Gross income needed' : 'Retirement age needed'}
          message={grossIncome <= 0
            ? 'Enter your gross income in Your Profile at the top of this page to value your future earnings.'
            : 'Your retirement age in Your Profile must be higher than your current age.'}
          action={{ label: 'Go to Your Profile', href: '#profile' }}
        />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-x-3 gap-y-4 md:grid-cols-3">
            {!schedule.incomes && <NumberField label="Salary growth" unit="% p.a." value={salaryGrowthPct} onValueChange={setSalaryGrowthPct} />}
            <NumberField label="Discount rate" unit="% p.a." value={discountPct} onValueChange={setDiscountPct} hint="How much less a euro earned next year is worth today." />
            <NumberField label="Portfolio growth" unit="% p.a." value={portfolioGrowthPct} onValueChange={setPortfolioGrowthPct} />
          </div>

          <MetricGrid className="mt-5">
            <MetricCard
              label="Human capital"
              value={fmtEUR(humanCapital)}
              sublabel={`Gross earnings over ${yearsToRetire} ${yearsToRetire === 1 ? 'year' : 'years'} to retirement, in today’s value`}
            />
            <MetricCard label="Financial capital" value={fmtEUR(financialCapital)} sublabel="Your cash and investments today" />
            <MetricCard
              label="Crossover age"
              money={false}
              value={crossoverAge ?? 'Not before retirement'}
              sublabel="When financial capital overtakes remaining human capital"
            />
          </MetricGrid>

          <SubHeading className="mt-6">Human and financial capital by age</SubHeading>
          <p className="mt-1 text-[13px] text-ink-muted">
            {schedule.incomes
              ? <>Earnings and savings come from your income streams on the Income tab.</>
              : <>Financial capital assumes you invest the <span data-privacy="blur">{fmtEUR(monthlyContrib)}</span> monthly budget from Your Profile.</>}
          </p>
          <EChartWrapper
            option={{
              tooltip: { trigger: 'axis' as const, valueFormatter: (v: unknown) => fmtEUR(Number(v)) },
              legend: {
                bottom: 0,
                itemWidth: 16,
                data: [
                  { name: 'Human capital', icon: 'path://M0 4h19v2h-19z' },
                  { name: 'Financial capital', icon: 'path://M0 4h5v2h-5zM7 4h5v2h-5zM14 4h5v2h-5z' },
                ],
              },
              xAxis: { type: 'category' as const, name: 'Age', nameLocation: 'middle' as const, nameGap: 26, data: crossoverData.map(d => `${d.age}`), axisLabel: { fontSize: 11 } },
              yAxis: { type: 'value' as const, name: '€', axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
              series: [
                {
                  name: 'Human capital', type: 'line' as const, data: crossoverData.map(d => d.human), smooth: 0.3, showSymbol: false,
                  color: tc.forest, itemStyle: { color: tc.forest }, lineStyle: { color: tc.forest, width: 2 }, areaStyle: { opacity: 0.08, color: tc.forest },
                },
                {
                  name: 'Financial capital', type: 'line' as const, data: crossoverData.map(d => d.financial), smooth: 0.3, showSymbol: false,
                  color: tc.gold, itemStyle: { color: tc.gold }, lineStyle: { color: tc.gold, width: 2, type: 'dashed' as const }, areaStyle: { opacity: 0.08, color: tc.gold },
                },
              ],
              grid: { left: 52, right: 16, top: 28, bottom: 64 },
            }}
            height="280px"
          />
        </>
      )}
    </Section>
  );
}
