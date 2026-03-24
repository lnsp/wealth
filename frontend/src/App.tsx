import { BrowserRouter, Routes, Route, NavLink } from 'react-router-dom';
import NetWorth from './pages/NetWorth';
import Portfolio from './pages/Portfolio';
import Analysis from './pages/Analysis';
import Transactions from './pages/Transactions';
import Settings from './pages/Settings';

const navItems = [
  { path: '/', label: 'Net Worth' },
  { path: '/portfolio', label: 'Portfolio' },
  { path: '/analysis', label: 'Analysis' },
  { path: '/transactions', label: 'Transactions' },
  { path: '/settings', label: 'Settings' },
];

export default function App() {
  return (
    <BrowserRouter>
      <div className="min-h-screen">
        <nav className="border-b border-gray-200 bg-white">
          <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
            <div className="flex h-14 items-center justify-between">
              <div className="flex items-center space-x-1">
                <span className="mr-6 text-lg font-bold text-gray-900">Finance Tracker</span>
                {navItems.map(({ path, label }) => (
                  <NavLink
                    key={path}
                    to={path}
                    end={path === '/'}
                    className={({ isActive }) =>
                      `rounded-md px-3 py-2 text-sm font-medium transition-colors ${
                        isActive
                          ? 'bg-gray-100 text-gray-900'
                          : 'text-gray-500 hover:bg-gray-50 hover:text-gray-700'
                      }`
                    }
                  >
                    {label}
                  </NavLink>
                ))}
              </div>
            </div>
          </div>
        </nav>

        <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">
          <Routes>
            <Route path="/" element={<NetWorth />} />
            <Route path="/portfolio" element={<Portfolio />} />
            <Route path="/analysis" element={<Analysis />} />
            <Route path="/transactions" element={<Transactions />} />
            <Route path="/settings" element={<Settings />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
