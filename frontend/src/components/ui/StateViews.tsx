import { Component } from 'react';
import type { ReactNode } from 'react';

interface LoadingStateProps {
  message?: string;
  /** Number of skeleton lines. */
  lines?: number;
}

const WIDTHS = ['60%', '90%', '75%', '40%', '82%', '55%'];

/** Skeleton lines. Keeps the surrounding layout in place instead of a centred "Loading…". */
export function LoadingState({ message = 'Loading', lines = 4 }: LoadingStateProps) {
  return (
    <div className="py-6 flex flex-col gap-3" role="status" aria-busy="true">
      <span className="sr-only">{message}</span>
      {Array.from({ length: lines }, (_, i) => (
        <div key={i} className="h-3 rounded-[3px] bg-divider motion-safe:animate-pulse" style={{ width: WIDTHS[i % WIDTHS.length] }} />
      ))}
    </div>
  );
}

interface ErrorStateProps {
  /** Plain-language sentence. Defaults to the standard section message. */
  message?: string;
  onRetry?: () => void;
  /** Raw error text, tucked behind a "Technical details" disclosure. */
  details?: string;
}

/** Error band for a section: the rest of the page stays usable. */
export function ErrorState({ message = 'This section couldn’t be displayed. The rest of the page is unaffected.', onRetry, details }: ErrorStateProps) {
  return (
    <div role="alert" className="flex flex-col gap-2 rounded-apple border border-divider bg-inset px-5 py-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className="apple-chip border-claret text-claret">Error</span>
        <span className="flex-1 basis-64 text-[14px] font-medium text-ink">{message}</span>
        {onRetry && (
          <button type="button" onClick={onRetry} className="apple-btn-secondary apple-btn-sm">Try again</button>
        )}
      </div>
      {details && (
        <details className="text-[13px] text-ink-muted">
          <summary className="cursor-pointer min-h-[32px] flex items-center">Technical details</summary>
          <span className="font-mono text-[12px] break-words">{details}</span>
        </details>
      )}
    </div>
  );
}

interface EmptyStateProps {
  /** Short serif headline, e.g. "No dividends yet". */
  title?: string;
  message: ReactNode;
  action?: { label: string; href?: string; onClick?: () => void };
  /** Extra content under the message (e.g. a progress bar). */
  children?: ReactNode;
}

/** Empty state card: headline, one sentence, one primary action. */
export function EmptyState({ title, message, action, children }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-apple border border-divider px-6 py-9 text-center">
      {title && <p className="font-serif text-heading font-normal text-ink">{title}</p>}
      <p className="max-w-md text-[14px] text-ink-body">{message}</p>
      {children}
      {action && action.href && (
        <a href={action.href} className="apple-btn-primary mt-2">{action.label}</a>
      )}
      {action && !action.href && action.onClick && (
        <button type="button" onClick={action.onClick} className="apple-btn-primary mt-2">{action.label}</button>
      )}
    </div>
  );
}

interface SectionBoundaryProps {
  children: ReactNode;
  /** Shown instead of the default message. */
  message?: string;
}

/**
 * Error boundary for one section or tab panel. A render error shows the error band in place
 * of that section only; tabs, navigation and sibling sections keep working.
 */
export class SectionBoundary extends Component<SectionBoundaryProps, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: { componentStack?: string | null }) {
    console.error('SectionBoundary caught:', error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <ErrorState
          message={this.props.message}
          details={this.state.error.message}
          onRetry={() => this.setState({ error: null })}
        />
      );
    }
    return this.props.children;
  }
}
