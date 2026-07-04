import { useState } from 'react';
import { Outlet, Link, useLocation } from 'react-router';
import { FaCog, FaCircle } from 'react-icons/fa';
import { useSettings } from './contexts/SettingsContext';
import SettingsPanel from './components/SettingsPanel';

function App() {
  const location = useLocation();
  const { t } = useSettings();
  const [settingsOpen, setSettingsOpen] = useState(false);

  const navLinks = [
    { path: '/articles', label: t('nav.articles') },
    { path: '/network', label: t('nav.network') },
    { path: '/about', label: t('nav.about') },
  ];

  return (
    <div className="flex flex-col min-h-screen text-brand font-display">
      <header className="border-b border-brand-200/50 dark:border-brand-800/50">
        <div className="flex flex-row items-center h-24 px-8 max-w-screen-2xl mx-auto w-full">
          <Link to={{ pathname: '/' }} className="shrink-0">
            <div className="flex flex-row gap-4 items-center">
              <img src="logo.png" className="h-16 w-16" />
              <h1 className="text-4xl font-black tracking-tight dark:text-white">
                The RED Engine
              </h1>
            </div>
          </Link>

          <nav className="flex mx-auto text-xl font-light gap-12">
            {navLinks.map((link) => {
              const isActive = location.pathname === link.path;
              return (
                <Link
                  key={link.path}
                  to={{ pathname: link.path }}
                  className={`pb-1 border-b-2 transition duration-200 ease-in-out ${
                    isActive
                      ? 'border-brand-400 text-brand-600 dark:text-brand-400 font-medium'
                      : 'border-transparent hover:border-brand-300 hover:text-brand-500 dark:hover:text-brand-300'
                  }`}
                >
                  {link.label}
                </Link>
              );
            })}
          </nav>

          <div className="relative shrink-0">
            <button onClick={() => setSettingsOpen((p) => !p)} aria-label="Open settings">
              <FaCog className="text-2xl text-brand-400 hover:text-brand-600 dark:hover:text-brand-300 transition-colors duration-200 cursor-pointer" />
            </button>
            <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} />
          </div>
        </div>
      </header>

      <div className="flex flex-1 flex-row">
        <aside className="flex flex-col w-56 border-r border-brand-200/50 dark:border-brand-800/50 p-6 gap-4 shrink-0">
          <h2 className="text-sm font-body font-semibold uppercase tracking-widest text-brand-400">
            {t('sidebar.node')}
          </h2>
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-3 text-brand-600 dark:text-brand-400 font-body text-sm">
              <FaCircle className="text-green-500 text-[8px]" />
              <span>{t('sidebar.local')}</span>
            </div>
            <div className="flex items-center gap-3 text-brand-300 font-body text-sm">
              <FaCircle className="text-brand-300 text-[8px]" />
              <span>{t('sidebar.no_peers')}</span>
            </div>
          </div>
        </aside>

        <main className="flex-1 flex flex-col">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

export default App;
