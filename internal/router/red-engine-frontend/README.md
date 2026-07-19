# RED Engine — Frontend

This is the React SPA for the RED Engine. It communicates with the Go backend via REST API.

## Tech Stack

- **React 19** + **TypeScript 6**
- **Vite 8** (bundler / dev server)
- **Tailwind CSS v4** (styling)
- **React Router v7** (routing)

## Directory Structure

```
src/
├── main.tsx              — Entry point, route definitions
├── App.tsx               — Layout (header, nav bar, sidebar)
├── index.css             — Tailwind theme + dark mode
├── i18n.ts               — Translation strings (7 languages)
├── not_found.tsx         — 404 page
├── contexts/
│   └── SettingsContext.tsx — Theme + language state management
├── components/
│   └── SettingsPanel.tsx  — Settings dropdown (theme toggle, language select)
├── pages/
│   ├── main.tsx           — Home / hero page
│   ├── article.tsx        — Article listing + directory browser
│   ├── article-detail.tsx — Single article viewer
│   └── about.tsx          — About page
└── assets/
    └── logo.png
```

## Development

```bash
# From repo root — starts both backend + frontend
cd ../..
./red-dev.sh

# Or from this directory (frontend only — backend must be running separately)
npm install
npm run dev
```

The Vite dev server runs on `http://localhost:5173` and proxies `/api/*` requests to the Go backend at `http://localhost:8080`.

## Production Build

```bash
npm run build
# Output goes to ../../../FRONTEND_BUILD/ (repo root)
```

## Routes

| Path | Page |
|---|---|
| `/` | Home |
| `/articles` | Article listing / directory browser |
| `/article?path=...` | Single article view |
| `/about` | About the RED Engine |

## Features

- Directory browsing — navigate vault folders and files
- Dark mode toggle — persists to localStorage
- 7-language i18n — English, Spanish, French, German, Japanese, Chinese, Hindi
- Article verification badges (verified / unsigned)
- Breadcrumb navigation
- Prev/next article navigation
