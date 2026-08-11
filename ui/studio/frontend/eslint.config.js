import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tailwindcss from 'eslint-plugin-tailwindcss'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
      tailwindcss.configs.recommended,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    settings: {
      tailwindcss: {
        cssConfigPath: 'src/index.css',
      },
    },
    rules: {
      // Scoped down from the plugin's recommended preset to just the two rules that catch
      // real bugs (unrecognized/removed classes, conflicting utilities); the rest are style
      // opinions already covered by prettier-plugin-tailwindcss or too noisy to be worth it.
      'tailwindcss/classnames-order': 'off',
      'tailwindcss/enforces-negative-arbitrary-values': 'off',
      'tailwindcss/enforces-shorthand': 'off',
      'tailwindcss/important-modifier-suffix': 'off',
      'tailwindcss/no-unnecessary-arbitrary-value': 'off',
    },
  },
  {
    // shadcn/ui generated files — do not edit manually; fast-refresh rule doesn't apply
    files: ['src/components/ui/**/*.{ts,tsx}'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
])
