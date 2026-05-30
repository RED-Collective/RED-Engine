/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./internal/router/templates/**/*.html",
    "./internal/router/static/**/*.js",
  ],
  corePlugins: {
    preflight: false,   // prevents Tailwind from overriding your existing styles
  },
  theme: {
    extend: {},
  },
  plugins: [],
}