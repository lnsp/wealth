import { PageHeader, SectionBoundary, TabBar, TabPanel, useTabParam } from '../components/ui';
import TaxOverview from './analysis/TaxOverview';
import TaxTools from './analysis/TaxTools';
import TaxFiling from './analysis/TaxFiling';

const TAB_IDS = ['overview', 'tools', 'filing'] as const;
type TaxTab = typeof TAB_IDS[number];

const TABS: { id: TaxTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'tools', label: 'Tools' },
  { id: 'filing', label: 'Filing' },
];

/**
 * Tax: its own page with its own tabs. Each tab loads only the requests it shows; nothing
 * from Analysis is mounted here. The active tab lives in the URL (?tab=…).
 */
export default function Tax() {
  const [tab, setTab] = useTabParam<TaxTab>(TAB_IDS, 'overview');
  return (
    <div>
      <PageHeader title="Tax" />
      <div className="md:mt-4">
        <TabBar tabs={TABS} activeTab={tab} onTabChange={setTab} ariaLabel="Tax views" />
      </div>
      <TabPanel id={tab}>
        <SectionBoundary key={tab}>
          {tab === 'overview' && <TaxOverview onOpenTools={() => setTab('tools')} />}
          {tab === 'tools' && <TaxTools />}
          {tab === 'filing' && <TaxFiling />}
        </SectionBoundary>
      </TabPanel>
    </div>
  );
}
