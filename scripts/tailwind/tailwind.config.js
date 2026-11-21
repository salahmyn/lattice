// @module-feature ui
// Tailwind CSS v3 configuration for the pages `lattice serve` renders.
//
// The binary does not run Tailwind. pkg/lattice/ui/assets/static/tailwind.css
// is generated once from this file and committed; `go build` only embeds it.
// Regenerate it after adding a utility class to a template (from the
// repository root; needs Node.js and network access to npm, once):
//
//   npx tailwindcss@3.4.19 -c scripts/tailwind/tailwind.config.js \
//     -i scripts/tailwind/input.css \
//     -o pkg/lattice/ui/assets/static/tailwind.css --minify
//
// TestTemplateClassesHaveStyles fails when a template uses a utility class
// the committed stylesheet does not define.
//
// The theme is the Crystal brand: Tailwind class names stay, their colours
// and fonts come from the brand tokens.
module.exports = {
  content: ['pkg/lattice/ui/assets/templates/*.html'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // The slate ramp is re-pointed to Crystal's neutral ramp, so every
        // bg-slate-50 / text-slate-700 reads as dark-by-default.
        slate: {
          50: '#0F1218', 100: '#171C25', 200: '#272F3D',
          300: '#3A4356', 400: '#8189A0', 500: '#8189A0',
          600: '#8189A0', 700: '#C7CDD9', 800: '#E2E6EE',
          900: '#E2E6EE',
        },
        // Crystal accent: mica violet.
        mica: {
          50: '#F1EEFB', 100: '#DAD2F4', 200: '#C7BEEC',
          300: '#A899E0', 400: '#8C7DD6', 500: '#6E5FC2',
          600: '#4E4196', 700: '#2E255E',
        },
        // Severity ramps, under the names the templates use.
        emerald: { 50: '#0E2620', 600: '#6BC2A2', 700: '#6BC2A2' },
        amber: { 50: '#26201A', 600: '#E5B66C', 700: '#E5B66C' },
        rose: { 50: '#2A1418', 600: '#E07A75', 700: '#E07A75' },
      },
      fontFamily: {
        sans: ['Geist', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', '"SF Mono"', 'Menlo', 'monospace'],
        serif: ['"IBM Plex Serif"', 'ui-serif', 'Georgia', 'serif'],
      },
    },
  },
};
