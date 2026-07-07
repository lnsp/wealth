import { useRef, useEffect } from 'react';

interface Tab<T extends string> {
  id: T;
  label: string;
}

interface TabBarProps<T extends string> {
  tabs: Tab<T>[];
  activeTab: T;
  onTabChange: (tab: T) => void;
}

export default function TabBar<T extends string>({ tabs, activeTab, onTabChange }: TabBarProps<T>) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    ref.current?.querySelector('[aria-selected="true"]')
      ?.scrollIntoView({ block: 'nearest', inline: 'center', behavior: 'smooth' });
  }, [activeTab]);

  return (
    <div ref={ref} className="flex gap-2 overflow-x-auto pb-1 scrollbar-hide" role="tablist">
      {tabs.map(tab => (
        <button
          key={tab.id}
          role="tab"
          aria-selected={activeTab === tab.id}
          aria-controls={`panel-${tab.id}`}
          onClick={() => onTabChange(tab.id)}
          className={`rounded-full px-3 md:px-4 py-2.5 text-[12px] font-medium whitespace-nowrap transition-colors min-h-[44px] ${
            activeTab === tab.id
              ? 'bg-forest text-white dark:text-parchment-deep'
              : 'border border-divider text-ink-muted hover:text-ink hover:border-ink-muted'
          }`}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}
