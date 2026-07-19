import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';
import { translations, type Lang } from '../i18n';

type Theme = 'light' | 'dark';

type SettingsContextType = {
  theme: Theme;
  lang: Lang;
  toggleTheme: () => void;
  setLang: (lang: Lang) => void;
  t: (key: string, params?: Record<string, string | number>) => string;
};

const SettingsContext = createContext<SettingsContextType | null>(null);

function loadFromStorage<T>(key: string, fallback: T): T {
  try {
    const val = localStorage.getItem(key);
    return val ? (JSON.parse(val) as T) : fallback;
  } catch {
    return fallback;
  }
}

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(() => loadFromStorage('red-theme', 'light'));
  const [lang, setLangState] = useState<Lang>(() => loadFromStorage<Lang>('red-lang', 'en'));

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark');
    localStorage.setItem('red-theme', JSON.stringify(theme));
  }, [theme]);

  useEffect(() => {
    localStorage.setItem('red-lang', JSON.stringify(lang));
    document.documentElement.lang = lang;
  }, [lang]);

  function toggleTheme() {
    setTheme((prev) => (prev === 'light' ? 'dark' : 'light'));
  }

  function setLang(next: Lang) {
    setLangState(next);
  }

  function t(key: string, params?: Record<string, string | number>): string {
    const dict = translations[lang] ?? translations.en;
    let val = dict[key] ?? translations.en[key] ?? key;
    if (params) {
      for (const [k, v] of Object.entries(params)) {
        val = val.replace(`{${k}}`, String(v));
      }
    }
    return val;
  }

  return (
    <SettingsContext.Provider value={{ theme, lang, toggleTheme, setLang, t }}>
      {children}
    </SettingsContext.Provider>
  );
}

export function useSettings() {
  const ctx = useContext(SettingsContext);
  if (!ctx) throw new Error('useSettings must be used within SettingsProvider');
  return ctx;
}
