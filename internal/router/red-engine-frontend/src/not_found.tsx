import { Link } from 'react-router';
import { useSettings } from './contexts/SettingsContext';

export default function NotFound() {
  const { t } = useSettings();
  return (
    <div className="min-h-screen text-center align-middle justify-center gap-16 flex flex-col font-mono overflow-hidden">
      <img
        className="fixed inset-0 min-h-screen min-w-screen -z-10 opacity-70 sepia-[0.5] grayscale -mt-32"
        src="/images/Cole_Thomas_The_Course_of_Empire_Destruction_1836.jpg"
      />
      <div className="flex flex-col gap-8">
        <p className="italic text-3xl">
          {t('not_found.message')}
        </p>
        <p className="font-black text-8xl">{t('not_found.title')}</p>
      </div>
      <div>
        <Link
          className="text-4xl text-brand-400 bg-brand-950 p-4 rounded-2xl"
          to={{
            pathname: '/',
          }}
        >
          {t('not_found.home')}
        </Link>
      </div>
    </div>
  );
}
