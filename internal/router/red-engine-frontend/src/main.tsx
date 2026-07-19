import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './index.css';

import { BrowserRouter, Routes, Route } from 'react-router';
import { SettingsProvider } from './contexts/SettingsContext';

import App from './App.tsx';
import NotFound from './not_found.tsx';
import MainPage from './pages/main.tsx';
import Articles from './pages/article.tsx';
import ArticleDetail from './pages/article-detail.tsx';
import About from './pages/about.tsx';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <SettingsProvider>
      <BrowserRouter>
        <Routes>
          <Route path="*" element={<NotFound />} />

          <Route path="/" element={<App />}>
            <Route index element={<MainPage />} />
            <Route path="articles" element={<Articles />} />
            <Route path="article" element={<ArticleDetail />} />
            <Route path="about" element={<About />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </SettingsProvider>
  </StrictMode>,
);
