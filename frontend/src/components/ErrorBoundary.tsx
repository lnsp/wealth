import { Component, type ReactNode } from 'react';
import { ErrorState } from './ui/StateViews';

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export default class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: { componentStack?: string | null }) {
    console.error('ErrorBoundary caught:', error, info.componentStack);
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) return this.props.fallback;
      // Page-level fallback. Sections and tab panels have their own SectionBoundary, so this
      // only shows when a whole page fails. Navigation stays usable around it, and "Try again"
      // re-renders the same URL (tab included, since tabs live in the query string).
      return (
        <div className="py-6">
          <ErrorState
            message="This page couldn\u2019t be displayed. Navigation still works."
            details={this.state.error?.message}
            onRetry={() => this.setState({ hasError: false, error: null })}
          />
        </div>
      );
    }
    return this.props.children;
  }
}
