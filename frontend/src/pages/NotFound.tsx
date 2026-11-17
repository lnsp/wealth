import { Link } from 'react-router-dom';

export default function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center py-20 text-center">
      <p className="label-caps">Error 404</p>
      {/* On phones the app header carries the h1 ("Page not found"). */}
      <h1 className="hidden md:block font-serif text-title text-ink mt-2">Page not found</h1>
      <p className="text-[16px] text-ink-muted mt-2 max-w-xs">
        The page you’re looking for doesn’t exist or has been moved.
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
