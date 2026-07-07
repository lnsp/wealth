interface LoadingStateProps {
  message?: string;
}

export function LoadingState({ message = 'Loading...' }: LoadingStateProps) {
  return (
    <div className="border-t border-divider pt-4 flex items-center justify-center py-20 text-[16px] text-ink-muted">
      {message}
    </div>
  );
}

interface ErrorStateProps {
  message: string;
  onRetry?: () => void;
}

export function ErrorState({ message, onRetry }: ErrorStateProps) {
  return (
    <div className="border-t border-divider pt-4 flex flex-col items-center justify-center py-20 gap-3">
      <svg className="w-10 h-10 text-claret" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
      </svg>
      <p className="text-[16px] text-claret">{message}</p>
      {onRetry && (
        <button onClick={onRetry} className="text-forest text-[15px] font-medium">
          Retry
        </button>
      )}
    </div>
  );
}

interface EmptyStateProps {
  message: string;
  action?: { label: string; href?: string; onClick?: () => void };
}

export function EmptyState({ message, action }: EmptyStateProps) {
  return (
    <div className="border-t border-divider pt-4 px-5 py-12 text-center text-[16px] text-ink-muted">
      <p>{message}</p>
      {action && action.href && (
        <a href={action.href} className="text-forest text-[15px] font-medium mt-2 inline-block">
          {action.label}
        </a>
      )}
      {action && action.onClick && (
        <button onClick={action.onClick} className="text-forest text-[15px] font-medium mt-2">
          {action.label}
        </button>
      )}
    </div>
  );
}
