import { useEffect, useRef } from 'react';
import { FaSun, FaMoon, FaGlobe, FaTimes } from 'react-icons/fa';
import { useSettings } from '../contexts/SettingsContext';
import { languages } from '../i18n';

type Props = {
  open: boolean;
  onClose: () => void;
};

export default function SettingsPanel({ open, onClose }: Props) {
  const { theme, lang, toggleTheme, setLang, t } = useSettings();
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function handleClick(e: MouseEvent) {
      if (panelRef.current && !panelRef.current.contains(e.target as Node)) {
        onClose();
      }
    }
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('mousedown', handleClick);
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('mousedown', handleClick);
      document.removeEventListener('keydown', handleKey);
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      ref={panelRef}
      className="absolute top-16 right-4 z-50 w-72 bg-white dark:bg-neutral-100 border border-brand-100 dark:border-brand-800 rounded-xl shadow-xl p-5 flex flex-col gap-5"
    >
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-body font-semibold uppercase tracking-widest text-brand-400">
          {t('settings.title')}
        </h3>
        <button
          onClick={onClose}
          aria-label="Close settings"
          className="text-neutral-400 hover:text-neutral-600 transition-colors"
        >
          <FaTimes />
        </button>
      </div>

      <div className="flex flex-col gap-2">
        <span className="text-xs font-body font-medium text-neutral-500 uppercase tracking-wider">
          {t('settings.theme')}
        </span>
        <div className="flex gap-2">
          <button
            onClick={() => theme !== 'light' && toggleTheme()}
            aria-label={t('settings.light')}
            className={`flex-1 flex items-center justify-center gap-2 py-2 rounded-lg text-sm font-body font-medium transition-all ${
              theme === 'light'
                ? 'bg-brand-100 text-brand-700 border border-brand-300'
                : 'bg-neutral-50 text-neutral-500 border border-neutral-200 hover:bg-neutral-100'
            }`}
          >
            <FaSun />
            {t('settings.light')}
          </button>
          <button
            onClick={() => theme !== 'dark' && toggleTheme()}
            aria-label={t('settings.dark')}
            className={`flex-1 flex items-center justify-center gap-2 py-2 rounded-lg text-sm font-body font-medium transition-all ${
              theme === 'dark'
                ? 'bg-brand-100 text-brand-700 border border-brand-300'
                : 'bg-neutral-50 text-neutral-500 border border-neutral-200 hover:bg-neutral-100'
            }`}
          >
            <FaMoon />
            {t('settings.dark')}
          </button>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <label htmlFor="settings-lang" className="text-xs font-body font-medium text-neutral-500 uppercase tracking-wider">
          {t('settings.language')}
        </label>
        <div className="relative">
          <FaGlobe className="absolute left-3 top-1/2 -translate-y-1/2 text-neutral-400 text-sm" />
          <select
            id="settings-lang"
            value={lang}
            onChange={(e) => setLang(e.target.value as typeof lang)}
            className="w-full pl-9 pr-3 py-2 rounded-lg border border-neutral-200 bg-neutral-50 text-sm font-body text-neutral-700 focus:outline-none focus:border-brand-300 focus:ring-1 focus:ring-brand-200 appearance-none cursor-pointer"
          >
            {Object.entries(languages).map(([code, label]) => (
              <option key={code} value={code}>
                {label}
              </option>
            ))}
          </select>
        </div>
      </div>
    </div>
  );
}
