import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
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
    ],
    languageOptions: {
      globals: globals.browser,
    },
  },
  // shadcn-style UI primitives (button / badge / tabs / etc.) ship a
  // VariantProps helper alongside the component itself so downstream code
  // can do `buttonVariants({ variant: 'ghost' })`. Vite's
  // react-refresh rule considers those helper exports "non-component
  // re-exports" and asks you to split files — that's a refactor for code
  // we copy-paste from the shadcn CLI. Accept the noise for /components/ui.
  {
    files: ['src/components/ui/**/*.{ts,tsx}'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
  // Two specific files hold intentional stable-projection / mount-only
  // patterns that the exhaustive-deps rule would otherwise force into
  // re-render loops. Scope the override to those files so the safety net
  // stays active everywhere else.
  {
    files: [
      'src/components/files/FileBrowser.tsx',
      'src/pages/HomePage.tsx',
    ],
    rules: {
      'react-hooks/exhaustive-deps': 'off',
    },
  },
])
