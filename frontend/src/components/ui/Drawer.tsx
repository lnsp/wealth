interface DrawerProps {
  title: string;
  isOpen: boolean;
  onToggle: () => void;
  children: React.ReactNode;
}

export default function Drawer({ title, isOpen, onToggle, children }: DrawerProps) {
  return (
    <div className="border-t border-divider">
      <button
        onClick={onToggle}
        className="w-full flex items-center justify-between py-4 text-left group"
        aria-expanded={isOpen}
      >
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] group-hover:text-ink transition-colors">{title}</h2>
        <svg className={`w-4 h-4 text-ink-muted transition-transform duration-200 ${isOpen ? 'rotate-180' : ''}`} fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 8.25l-7.5 7.5-7.5-7.5" />
        </svg>
      </button>
      {isOpen && <div className="pb-4">{children}</div>}
    </div>
  );
}
