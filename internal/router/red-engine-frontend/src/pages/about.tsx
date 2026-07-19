import { useSettings } from '../contexts/SettingsContext';

export default function About() {
  const { t } = useSettings();
  return (
    <div className="flex-1 p-8 max-w-4xl mx-auto w-full">
      <div className="mb-10">
        <h1 className="text-3xl font-black text-neutral-900 dark:text-neutral-900">
          {t('about.title')}
        </h1>
        <p className="text-neutral-500 font-body text-sm mt-1">
          {t('about.subtitle')}
        </p>
      </div>

      <div className="flex flex-col gap-8">
        <section>
          <h2 className="text-xl font-bold text-brand-600 mb-3">{t('about.what')}</h2>
          <p className="font-body text-neutral-700 dark:text-neutral-700 leading-relaxed">
            {t('about.what_desc')}
          </p>
        </section>

        <section>
          <h2 className="text-xl font-bold text-brand-600 mb-3">{t('about.principles')}</h2>
          <div className="grid gap-4 md:grid-cols-3">
            <div className="border border-brand-100 dark:border-brand-800 rounded-lg p-5 bg-white dark:bg-neutral-100">
              <h3 className="font-bold text-lg text-neutral-900">{t('about.resilient')}</h3>
              <p className="font-body text-sm text-neutral-600 mt-2 leading-relaxed">
                {t('about.resilient_desc')}
              </p>
            </div>
            <div className="border border-brand-100 dark:border-brand-800 rounded-lg p-5 bg-white dark:bg-neutral-100">
              <h3 className="font-bold text-lg text-neutral-900">{t('about.encrypted')}</h3>
              <p className="font-body text-sm text-neutral-600 mt-2 leading-relaxed">
                {t('about.encrypted_desc')}
              </p>
            </div>
            <div className="border border-brand-100 dark:border-brand-800 rounded-lg p-5 bg-white dark:bg-neutral-100">
              <h3 className="font-bold text-lg text-neutral-900">{t('about.decentralized')}</h3>
              <p className="font-body text-sm text-neutral-600 mt-2 leading-relaxed">
                {t('about.decentralized_desc')}
              </p>
            </div>
          </div>
        </section>

        <section>
          <h2 className="text-xl font-bold text-brand-600 mb-3">{t('about.how')}</h2>
          <div className="font-body text-neutral-700 dark:text-neutral-700 leading-relaxed space-y-3">
            <p>{t('about.how_p1')}</p>
            <p>{t('about.how_p2')}</p>
          </div>
        </section>
      </div>
    </div>
  );
}
