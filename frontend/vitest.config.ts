import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'path';

// Vitest shares the Vite resolution rules + path aliases with src/ so
// test files import via `@/lib/...` exactly like production code does.
// jsdom gives us a window/document so component-level tests can run
// without extra setup. The `src` glob picks up `*.test.ts(x)` files
// alongside regular source; vitest ignores non-matching files by default.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    environment: 'jsdom',
    globals: false,
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
