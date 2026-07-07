import { useState, useEffect, useCallback, useRef } from 'react';
import { api, type NotificationEntry } from './api/client';
import { BrowserRouter, Routes, Route, NavLink, Navigate, useLocation } from 'react-router-dom';
import NetWorth from './pages/NetWorth';
import Portfolio from './pages/Portfolio';
import Analysis from './pages/Analysis';
import Transactions from './pages/Transactions';
import Planning from './pages/Planning';
import Cashflow from './pages/Cashflow';
import Tax from './pages/Tax';
import Settings from './pages/Settings';
import Login from './pages/Login';
import NotFound from './pages/NotFound';
import ErrorBoundary from './components/ErrorBoundary';

const navItems = [
  {
    path: '/', label: 'Net Worth', icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z" />
      </svg>
    )
  },
  {
    path: '/portfolio', label: 'Portfolio', icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125" />
      </svg>
    )
  },
  {
    path: '/analysis', label: 'Analysis', icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 6a7.5 7.5 0 107.5 7.5h-7.5V6z" />
        <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 10.5H21A7.5 7.5 0 0013.5 3v7.5z" />
      </svg>
    )
  },
  {
    path: '/planning', label: 'Planning', icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
    )
  },
  {
    path: '/tax', label: 'Tax', icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
      </svg>
    )
  },
  {
    path: '/transactions', label: 'Transactions', icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
      </svg>
    )
  },
  {
    path: '/cashflow', label: 'Cashflow', icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 18 9 11.25l4.306 4.306a11.95 11.95 0 0 1 5.814-5.518l2.74-1.22m0 0-5.94-2.281m5.94 2.28-2.28 5.941" />
      </svg>
    )
  },
  {
    path: '/settings', label: 'Settings', desktopOnly: true, icon: (
      <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" />
        <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
      </svg>
    )
  },
];

export default function App() {
  const location = useLocation();
  const [navBarPos, setNavBarPos] = useState({ top: 0, height: 0 });
  const navRef = useRef<HTMLElement>(null);

  useEffect(() => {
    requestAnimationFrame(() => {
      const activeEl = navRef.current?.querySelector('a[aria-current="page"]') as HTMLElement | null;
      if (activeEl) setNavBarPos({ top: activeEl.offsetTop, height: activeEl.offsetHeight });
    });
  }, [location.pathname]);

  const [authRequired, setAuthRequired] = useState<boolean | null>(null);
  const [authenticated, setAuthenticated] = useState(false);
  const [unreadCount, setUnreadCount] = useState(0);
  const [notifications, setNotifications] = useState<NotificationEntry[]>([]);
  const [showNotifications, setShowNotifications] = useState(false);
  const [darkMode, setDarkMode] = useState(() => {
    if (typeof window !== 'undefined') {
      const stored = localStorage.getItem('theme');
      if (stored) return stored === 'dark';
      return window.matchMedia('(prefers-color-scheme: dark)').matches;
    }
    return false;
  });
  const [privacyMode, setPrivacyMode] = useState(() => localStorage.getItem('privacy') === 'on');

  useEffect(() => {
    document.documentElement.classList.toggle('dark', darkMode);
    localStorage.setItem('theme', darkMode ? 'dark' : 'light');
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute('content', darkMode ? '#1A1816' : '#F0EDE8');
  }, [darkMode]);

  useEffect(() => {
    document.documentElement.classList.toggle('privacy', privacyMode);
    localStorage.setItem('privacy', privacyMode ? 'on' : 'off');
  }, [privacyMode]);

  // Keyboard shortcut: Ctrl+Shift+P to toggle privacy mode
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.shiftKey && e.key === 'P') {
        e.preventDefault();
        setPrivacyMode(p => !p);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, []);

  const checkAuth = useCallback(async () => {
    try {
      const resp = await fetch('/api/auth/status');
      const data = await resp.json();
      setAuthenticated(data.authenticated);
      // required field is false when auth is disabled, absent (= true) when enabled
      setAuthRequired(data.required !== false);
    } catch {
      setAuthRequired(false);
    }
  }, []);

  useEffect(() => { checkAuth(); }, [checkAuth]);
  useEffect(() => {
    const poll = () => api.listNotifications().then(r => {
      setUnreadCount(r.unread_count);
      setNotifications(r.notifications || []);
    }).catch(() => { });
    poll();
    const interval = setInterval(poll, 60000);
    return () => clearInterval(interval);
  }, []);

  const notifRef = useRef<HTMLDivElement>(null);
  // Close dropdown when clicking outside
  useEffect(() => {
    if (!showNotifications) return;
    const handleClick = (e: MouseEvent) => {
      if (notifRef.current && !notifRef.current.contains(e.target as Node)) {
        setShowNotifications(false);
      }
    };
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setShowNotifications(false);
        // NotificationBell is defined inside the parent, so each
        // setShowNotifications re-render produces a new component type and
        // remounts the button DOM node. We have to defer focus past React's
        // commit (via rAF) so we restore focus to the NEW node — and we have
        // to query by aria-controls since the bell renders twice (desktop
        // sidebar + mobile header) and only one is visible per viewport.
        requestAnimationFrame(() => {
          const candidates = document.querySelectorAll<HTMLButtonElement>('button[aria-controls="notifications-panel"]');
          for (const btn of candidates) {
            const rect = btn.getBoundingClientRect();
            if (rect.width > 0 && rect.height > 0) {
              btn.focus();
              break;
            }
          }
        });
      }
    };
    document.addEventListener('mousedown', handleClick);
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('mousedown', handleClick);
      document.removeEventListener('keydown', handleKey);
    };
  }, [showNotifications]);

  const toggleNotifications = () => {
    setShowNotifications(prev => !prev);
    if (unreadCount > 0) {
      api.markNotificationsRead().then(() => setUnreadCount(0)).catch(() => { });
    }
  };

  const NotificationBell = ({ className = '' }: { className?: string }) => (
    <div ref={notifRef} className={`relative ${className}`}>
      <button onClick={toggleNotifications} aria-label="Notifications" aria-expanded={showNotifications} aria-haspopup="true" aria-controls="notifications-panel" className="relative inline-flex items-center justify-center min-w-[44px] min-h-[44px] text-ink-muted hover:text-ink transition-colors">
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
        </svg>
        {unreadCount > 0 && (
          <span className="absolute -top-0.5 -right-0.5 flex h-4 min-w-[16px] items-center justify-center rounded-full bg-claret text-[10px] font-bold text-white dark:text-parchment-deep px-1">
            {unreadCount > 9 ? '9+' : unreadCount}
          </span>
        )}
      </button>
      {showNotifications && (
        <div id="notifications-panel" role="region" aria-label="Notifications" className="absolute right-0 top-full mt-2 w-80 max-h-96 overflow-y-auto rounded-xl bg-parchment shadow-lg border border-divider z-50">
          <div className="px-4 py-3 border-b border-divider">
            <h3 className="text-[15px] font-semibold text-ink">Notifications</h3>
          </div>
          {notifications.length === 0 ? (
            <p className="px-4 py-6 text-[13px] text-ink-muted text-center">No notifications yet</p>
          ) : (
            <div className="divide-y divide-divider">
              {notifications.slice(0, 20).map(n => (
                <div key={n.id} className={`px-4 py-3 ${!n.is_read ? 'bg-inset border-l-[3px] border-forest' : ''}`}>
                  <p className="text-[13px] text-ink">{n.message}</p>
                  <p className="text-[11px] text-ink-muted mt-0.5">
                    {new Date(n.triggered_at).toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })}
                  </p>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );

  const handleLogout = async () => {
    await fetch('/api/auth/logout', { method: 'POST' });
    setAuthenticated(false);
  };

  const ready = authRequired !== null && (!authRequired || authenticated);

  return (
    <>
      {/* Loading/Login overlay — covers everything until auth resolves */}
      {!ready && (
        <div className="fixed inset-0 z-[100] min-h-screen flex items-center justify-center bg-parchment">
          {authRequired === null ? (
            <div className="text-[16px] text-ink-muted">Loading...</div>
          ) : (
            <Login onLogin={() => setAuthenticated(true)} />
          )}
        </div>
      )}
      {/* Skip-link — first tabbable; on focus it lifts above the sidebar so
          keyboard users can jump past the 10+ chrome controls to page content. */}
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-[200] focus:rounded focus:bg-parchment focus:px-3 focus:py-2 focus:text-[13px] focus:font-medium focus:text-forest focus:outline focus:outline-2 focus:outline-forest">
        Skip to main content
      </a>
      <div className={`flex min-h-screen ${!ready ? 'invisible' : ''}`}>
        {/* Desktop sidebar — hidden on mobile */}
        <aside className="hidden md:flex fixed inset-y-0 left-0 z-30 w-56 flex-col border-r border-divider backdrop-blur-xl" style={{ backgroundColor: 'color-mix(in srgb, var(--color-inset) 90%, transparent)' }}>
          <div className="flex h-14 items-center gap-2.5 px-5">
            {/* Brand monogram — serif W in thin circle */}
            <svg viewBox="0 0 32 32" className="w-7 h-7 shrink-0 text-forest" aria-hidden="true">
              <circle cx="16" cy="16" r="15" fill="none" stroke="currentColor" strokeWidth="1" />
              <text x="16" y="22" textAnchor="middle" fontFamily="EB Garamond, Georgia, serif" fontSize="18" fontWeight="500" fill="currentColor">W</text>
            </svg>
            <h1 className="font-serif text-[17px] font-medium text-ink tracking-tight flex-1">Wealth</h1>
            <button onClick={() => setDarkMode(d => !d)} aria-pressed={darkMode} aria-label={darkMode ? 'Switch to light mode' : 'Switch to dark mode'} className="p-1 text-ink-muted hover:text-ink transition-colors" title={darkMode ? 'Light mode' : 'Dark mode'}>
              <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                {darkMode
                  ? <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" />
                  : <path strokeLinecap="round" strokeLinejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 006.002-2.082z" />
                }
              </svg>
            </button>
            <button onClick={() => setPrivacyMode(p => !p)} aria-pressed={privacyMode} aria-label={privacyMode ? 'Show monetary values' : 'Hide monetary values'} className="p-1 text-ink-muted hover:text-ink transition-colors" title={privacyMode ? 'Show values' : 'Hide values (Ctrl+Shift+P)'}>
              <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                {privacyMode
                  ? <path strokeLinecap="round" strokeLinejoin="round" d="M3.98 8.223A10.477 10.477 0 001.934 12C3.226 16.338 7.244 19.5 12 19.5c.993 0 1.953-.138 2.863-.395M6.228 6.228A10.45 10.45 0 0112 4.5c4.756 0 8.773 3.162 10.065 7.498a10.523 10.523 0 01-4.293 5.774M6.228 6.228L3 3m3.228 3.228l3.65 3.65m7.894 7.894L21 21m-3.228-3.228l-3.65-3.65m0 0a3 3 0 10-4.243-4.243m4.242 4.242L9.88 9.88" />
                  : <><path strokeLinecap="round" strokeLinejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" /><path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></>
                }
              </svg>
            </button>
            <NotificationBell />
          </div>

          <nav ref={navRef} className="flex-1 px-3 py-1 relative" aria-label="Main navigation">
            {/* Sliding accent bar */}
            <span
              className="absolute left-0 w-[3px] bg-forest rounded-full"
              style={{ top: navBarPos.top, height: navBarPos.height, transition: 'top 250ms cubic-bezier(0.4,0,0.2,1), height 250ms cubic-bezier(0.4,0,0.2,1)' }}
            />
            <div className="space-y-0.5">
              {navItems.map(({ path, label, icon }) => (
                <NavLink
                  key={path}
                  to={path}
                  end={path === '/'}
                  className={({ isActive }) =>
                    `flex items-center gap-2.5 rounded-[3px] px-2.5 py-[7px] text-[15px] transition-colors duration-[250ms] ${isActive
                      ? 'text-forest font-medium'
                      : 'text-ink-muted hover:text-ink-body'
                    }`
                  }
                >
                  {icon}
                  {label}
                </NavLink>
              ))}
            </div>
          </nav>

          {authRequired && (
            <div className="px-3 py-3 border-t border-divider">
              <button
                onClick={handleLogout}
                className="w-full flex items-center gap-2.5 rounded-[3px] px-2.5 py-[7px] text-[15px] text-ink-muted hover:bg-black/[0.03] dark:hover:bg-white/[0.06] active:bg-black/[0.05] dark:active:bg-white/[0.1] transition-all duration-[250ms]"
              >
                <svg className="w-[18px] h-[18px]" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
                </svg>
                Sign Out
              </button>
            </div>
          )}
        </aside>

        {/* Mobile top header — extends behind dynamic island / notch.
            Placed before the bottom nav in DOM so keyboard tab order visits
            header controls first, matching the visual top-to-bottom flow. */}
        <header className="md:hidden fixed inset-x-0 top-0 z-30 border-b border-divider backdrop-blur-xl" style={{ backgroundColor: 'color-mix(in srgb, var(--color-inset) 95%, transparent)', paddingTop: 'env(safe-area-inset-top, 0px)' }}>
          <div className="h-12 flex items-center justify-between px-4">
            <div className="flex items-center gap-2">
              <svg viewBox="0 0 32 32" className="w-6 h-6 shrink-0 text-forest" aria-hidden="true">
                <circle cx="16" cy="16" r="15" fill="none" stroke="currentColor" strokeWidth="1" />
                <text x="16" y="22" textAnchor="middle" fontFamily="EB Garamond, Georgia, serif" fontSize="18" fontWeight="500" fill="currentColor">W</text>
              </svg>
            </div>
            <div className="flex gap-2">
              <button onClick={() => setDarkMode(d => !d)} aria-pressed={darkMode} className="inline-flex items-center justify-center min-w-[44px] min-h-[44px] text-ink-muted" title={darkMode ? 'Light mode' : 'Dark mode'} aria-label={darkMode ? 'Switch to light mode' : 'Switch to dark mode'}>
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                  {darkMode
                    ? <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" />
                    : <path strokeLinecap="round" strokeLinejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 006.002-2.082z" />
                  }
                </svg>
              </button>
              <button onClick={() => setPrivacyMode(p => !p)} aria-pressed={privacyMode} className="inline-flex items-center justify-center min-w-[44px] min-h-[44px] text-ink-muted" title={privacyMode ? 'Show values' : 'Hide values'} aria-label={privacyMode ? 'Show monetary values' : 'Hide monetary values'}>
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                  {privacyMode
                    ? <path strokeLinecap="round" strokeLinejoin="round" d="M3.98 8.223A10.477 10.477 0 001.934 12C3.226 16.338 7.244 19.5 12 19.5c.993 0 1.953-.138 2.863-.395M6.228 6.228A10.45 10.45 0 0112 4.5c4.756 0 8.773 3.162 10.065 7.498a10.523 10.523 0 01-4.293 5.774M6.228 6.228L3 3m3.228 3.228l3.65 3.65m7.894 7.894L21 21m-3.228-3.228l-3.65-3.65m0 0a3 3 0 10-4.243-4.243m4.242 4.242L9.88 9.88" />
                    : <><path strokeLinecap="round" strokeLinejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" /><path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></>
                  }
                </svg>
              </button>
              <NotificationBell />
              <NavLink to="/settings" className="inline-flex items-center justify-center min-w-[44px] min-h-[44px] text-ink-muted" aria-label="Settings">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" />
                  <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                </svg>
              </NavLink>
            </div>
          </div>
        </header>

        {/* Main content area */}
        <main id="main" tabIndex={-1} className="md:ml-56 flex-1 min-w-0 min-h-screen pb-[88px] md:pb-0 overflow-x-hidden safe-area-header-offset focus:outline-none">
          <div className="mx-auto max-w-5xl px-4 py-5 md:px-8 md:py-8">
            <ErrorBoundary key={location.pathname}>
              <Routes>
                <Route path="/" element={<NetWorth />} />
                <Route path="/portfolio" element={<Portfolio />} />
                <Route path="/analysis" element={<Analysis />} />
                <Route path="/planning" element={<Planning />} />
                <Route path="/cashflow" element={<Cashflow />} />
                <Route path="/tax" element={<Tax />} />
                <Route path="/lab" element={<Navigate to="/tax" replace />} />
                <Route path="/transactions" element={<Transactions />} />
                <Route path="/settings" element={<Settings onLogout={authRequired ? handleLogout : undefined} authEnabled={authRequired || false} />} />
                <Route path="*" element={<NotFound />} />
              </Routes>
            </ErrorBoundary>
          </div>
        </main>

        {/* Mobile bottom tab bar — placed last so keyboard tab order goes
            top header → main content → bottom nav, matching visual flow. */}
        <nav className="md:hidden fixed inset-x-0 bottom-0 z-30 border-t border-divider backdrop-blur-xl safe-area-bottom" aria-label="Main navigation" style={{ backgroundColor: 'color-mix(in srgb, var(--color-inset) 95%, transparent)' }}>
          <div className="flex items-stretch justify-around pb-[4px]">
            {navItems.filter(n => !n.desktopOnly).map(({ path, label, icon }) => (
              <NavLink
                key={path}
                to={path}
                end={path === '/'}
                className={({ isActive }) =>
                  `flex flex-1 flex-col items-center gap-0.5 py-2 pt-2 text-[10px] font-medium transition-colors duration-[250ms] ${isActive ? 'text-forest' : 'text-ink-muted'
                  }`
                }
              >
                {icon}
                <span className="truncate max-w-[48px] text-center">{label}</span>
              </NavLink>
            ))}
          </div>
        </nav>
      </div>
    </>
  );
}

// Wrap App in BrowserRouter at the top level so the router is always mounted
function AppWithRouter() {
  return (
    <BrowserRouter>
      <App />
    </BrowserRouter>
  );
}

export { AppWithRouter };
