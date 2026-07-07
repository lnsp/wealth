import { Link } from 'react-router-dom';

export default function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center py-20 text-center">
      <p className="text-[64px] font-bold text-ink-muted/30 leading-none">404</p>
      <h1 className="font-serif text-[22px] font-medium text-ink mt-3">Page not found</h1>
      <p className="text-[16px] text-ink-muted mt-2 max-w-xs">
        The page you're looking for doesn't exist or has been moved.
      </p>
      <Link
        to="/"
        className="mt-6 apple-btn-primary px-6"
      >
        Go to Net Worth
      </Link>
    </div>
  );
}
