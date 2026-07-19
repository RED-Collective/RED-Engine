import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// The compiled SPA is embedded into the Go binary from internal/router/static/dist
// (see router.go's `go:embed static`) and served at "/", so assets keep the default
// "/" base. @vitejs/plugin-vue compiles the .vue SFCs; @tailwindcss/vite builds the
// Tailwind v4 styles imported from src/style.css.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  build: {
    outDir: 'internal/router/static/dist',
    emptyOutDir: true,
  },
})
