import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './index.css';

import { BrowserRouter, Routes, Route } from 'react-router';

import App from './App.tsx';
import NotFound from './not_found.tsx';
import MainPage from './pages/main.tsx';
import Articles from './pages/article.tsx';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <Routes>
        <Route path="*" element={<NotFound />} />

        <Route path="/" element={<App />}>
          <Route index element={<MainPage />} />
          <Route path="articles" element={<Articles />} />
        </Route>
      </Routes>
    </BrowserRouter>
  </StrictMode>,
);
