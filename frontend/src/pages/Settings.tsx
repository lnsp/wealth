import { PageHeader, TabBar, TabPanel, useTabParam, SectionBoundary } from '../components/ui';
import { fmtDate } from '../utils/format';
import { fetchJson, useLoad } from './settings/shared';
import DataTab from './settings/DataTab';
import AccountsTab from './settings/AccountsTab';
import SecurityTab from './settings/SecurityTab';
import NotificationsTab from './settings/NotificationsTab';

const TAB_IDS = ['data', 'accounts', 'security', 'notifications'] as const;
type SettingsTab = typeof TAB_IDS[number];
const SETTINGS_TABS: { id: SettingsTab; label: string }[] = [
  { id: 'data', label: 'Data' },
  { id: 'accounts', label: 'Accounts' },
  { id: 'security', label: 'Security' },
  { id: 'notifications', label: 'Notifications' },
];

interface BuildInfo {
  commit: string;
  built_at: string;
}

interface SettingsProps {
  /** Sign out lives in the sidebar and on the mobile More page; kept for the route's call signature. */
  onLogout?: () => void;
  authEnabled?: boolean;
}

function BuildFooter() {
  const { data } = useLoad(() => fetchJson<BuildInfo>('/api/settings/build-info'));
  if (!data?.commit || data.commit === 'unknown') return null;
  return (
    <p className="text-[12px] text-ink-muted text-center pt-10 pb-2">
      Version <span className="font-mono">{data.commit.substring(0, 7)}</span>
      {data.built_at && data.built_at !== 'unknown' && <> · built {fmtDate(data.built_at)}</>}
    </p>
  );
}

export default function Settings({ authEnabled = true }: SettingsProps = {}) {
  const [activeTab, setActiveTab] = useTabParam<SettingsTab>(TAB_IDS, 'data');

  return (
    <div>
      <PageHeader title="Settings" />
      <div className="md:mt-3">
        <TabBar tabs={SETTINGS_TABS} activeTab={activeTab} onTabChange={setActiveTab} ariaLabel="Settings sections" />
      </div>

      <TabPanel id={activeTab} className="pb-4">
        <SectionBoundary key={activeTab}>
          {activeTab === 'data' && <DataTab />}
          {activeTab === 'accounts' && <AccountsTab />}
          {activeTab === 'security' && <SecurityTab authEnabled={authEnabled} />}
          {activeTab === 'notifications' && <NotificationsTab />}
        </SectionBoundary>
      </TabPanel>

      <BuildFooter />
    </div>
  );
}
