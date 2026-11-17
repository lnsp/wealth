import { PageHeader, SectionBoundary, TabBar, TabPanel, useTabParam } from '../components/ui';
import RiskTab from './analysis/RiskTab';
import CostsTab from './analysis/CostsTab';
import ExposureTab from './analysis/ExposureTab';
import PerformanceTab from './analysis/PerformanceTab';
import SpendingTab from './analysis/SpendingTab';

const TAB_IDS = ['risk', 'costs', 'exposure', 'performance', 'spending'] as const;
type AnalysisTab = typeof TAB_IDS[number];

const TABS: { id: AnalysisTab; label: string }[] = [
  { id: 'risk', label: 'Risk' },
  { id: 'costs', label: 'Costs' },
  { id: 'exposure', label: 'Exposure' },
  { id: 'performance', label: 'Performance' },
  { id: 'spending', label: 'Spending' },
];

/**
 * Analysis: each tab is its own component and loads only what it shows. The active tab lives
 * in the URL (?tab=…), so a reload or "Try again" after an error stays on the same tab.
 */
export default function Analysis() {
  const [tab, setTab] = useTabParam<AnalysisTab>(TAB_IDS, 'risk');
  return (
    <div>
      <PageHeader title="Analysis" />
      <div className="md:mt-4">
        <TabBar tabs={TABS} activeTab={tab} onTabChange={setTab} ariaLabel="Analysis views" />
      </div>
      <TabPanel id={tab}>
        {/* Keyed by tab so an error in one tab never carries over to the next. */}
        <SectionBoundary key={tab}>
          {tab === 'risk' && <RiskTab />}
          {tab === 'costs' && <CostsTab />}
          {tab === 'exposure' && <ExposureTab />}
          {tab === 'performance' && <PerformanceTab />}
          {tab === 'spending' && <SpendingTab />}
        </SectionBoundary>
      </TabPanel>
    </div>
  );
}
