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
    // Installs an in-memory `localStorage` when the environment's own one
    // is missing or broken (Node >= 22 shadows jsdom's with an
    // experimental `undefined` stub). See src/test/setup.ts for the
    // long-form rationale — do not "simplify" it away, the store specs
    // depend on it.
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
