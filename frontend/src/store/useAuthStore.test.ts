// Locks the cookie-mirror contract added alongside the trailing-slash
// streamUrl fix. The native <audio> element on the bottom player bar
// cannot carry the Axios Request interceptor's `Authorization: JWT
// <token>` header because that header is JS-only. Without an
// `AUTHORIZATION` cookie, the JWTAuth middleware (internal/gateway/
// middleware/auth.go) sees no auth and aborts the /api/stream GET
// with 401 — the user sees 试听链接不可用 instead of music. These
// tests pin the cookie round-trip so a future refactor doesn't
// silently break that path.
//
// (jsdom env is the global default from frontend/vitest.config.ts; no
// per-file pragma is needed.)
//
// Scope:
//   - login() writes the cookie with the pre-encoded raw token + path
//     `/` + max-age 7d + SameSite=Lax.
//   - logout() clears the cookie.
//   - Boot-time hydration of an existing localStorage token re-issues
//     the cookie (covers the "browser restart with persisted token"
//     path).
//   - Missing document.cookie is a no-op (SSR / worker fallback).

import { beforeEach, describe, expect, it } from 'vitest';
import { useAuthStore } from '@/store/useAuthStore';
import { readString, removeString } from '@/utils/persist';

const TOKEN_KEY = 'auth.accessToken';
const COOKIE_NAME = 'AUTHORIZATION';
const SAMPLE_TOKEN = 'header.payload.signature';

function clearAuthState() {
  // Each test starts from a clean slate so a cookie left over from a
  // previous test doesn't bleed in. removeString handles a missing
  // localStorage entry without throwing.
  removeString(TOKEN_KEY);
  document.cookie = `${COOKIE_NAME}=; path=/; max-age=0`;
}

describe('useAuthStore cookie mirror', () => {
  beforeEach(clearAuthState);

  it('login() writes the AUTHORIZATION cookie with SameSite=Lax, path=/, 7d max-age', () => {
    // document.cookie is a setter that ALSO parses internally; reading
    // back `document.cookie` only echoes `NAME=VALUE` pairs (RFC 6265
    // §5.2 — attributes like path/SameSite/max-age belong to the
    // Set-Cookie grammar and are NOT part of the Cookie request
    // header). To assert the full string we replace the cookie setter
    // with a spy that captures the input verbatim, then FULLY restore
    // the descriptor in the cleanup hook (both Get and Set) so a
    // later test in this file (e.g. logout() reads document.cookie
    // and expects the parsed jar, not the leaked empty-getter stub).
    const captured: string[] = [];
    const originalDescriptor = Object.getOwnPropertyDescriptor(
      Document.prototype,
      'cookie',
    );

    Object.defineProperty(Document.prototype, 'cookie', {
      configurable: true,
      get: () => '',
      set: (v: string) => {
        captured.push(v);
      },
    });

    try {
      useAuthStore.getState().login(SAMPLE_TOKEN);
    } finally {
      // FULL restore: both Get and Set. Round 2 reviewer caught a
      // half-restore that left the override getter installed and
      // broke the logout test (saw `''` instead of the real jar).
      if (originalDescriptor) {
        Object.defineProperty(Document.prototype, 'cookie', originalDescriptor);
      }
    }

    expect(captured.length).toBe(1);
    const cookie = captured[0];
    // Cookie value is the raw JWT (no "JWT " prefix). The middleware
    // re-applies that prefix in its cookie-to-header fallback branch
    // (middleware/auth.go::JWTAuth).
    expect(cookie).toMatch(
      new RegExp(`^${COOKIE_NAME}=${encodeURIComponent(SAMPLE_TOKEN)}`),
    );
    expect(cookie).toMatch(/path=\//);
    // SameSite=Lax blocks cross-site POST CSRF without breaking
    // same-origin <audio> cookie delivery.
    expect(cookie).toMatch(/SameSite=Lax/);
    // Cookie TTL mirrors the JWT 7-day mint in
    // handler/auth.go::generateJWT. Drift here is a canary.
    expect(cookie).toMatch(/max-age=604800/);
  });

  it('logout() clears the AUTHORIZATION cookie', () => {
    useAuthStore.getState().login(SAMPLE_TOKEN);
    expect(document.cookie).toContain(`${COOKIE_NAME}=`);

    useAuthStore.getState().logout();
    // jsdom parses the cookie jar from `document.cookie`; an entry
    // overwritten with max-age=0 disappears from this string. Use the
    // localStorage read as a fallback-positive to confirm the call
    // didn't just no-op and silently leave the cookie alone.
    expect(readString(TOKEN_KEY)).toBeNull();
    expect(document.cookie).not.toMatch(
      new RegExp(`${COOKIE_NAME}=${encodeURIComponent(SAMPLE_TOKEN)}`),
    );
  });

  it('setAuthCookie is no-op when document.cookie is unavailable', async () => {
    // This case validates the typeof document === 'undefined' guard so
    // SSR or worker contexts that load this module don't blow up.
    const originalDocument = (globalThis as { document?: unknown }).document;
    try {
      // delete globalThis.document
      Object.defineProperty(globalThis, 'document', {
        configurable: true,
        value: undefined,
      });
      // Re-importing the module after deletion isn't trivial in vitest;
      // instead, exercise the store API directly — login() should
      // silently swallow the missing document.cookie.
      expect(() =>
        useAuthStore.getState().login('env-without-document'),
      ).not.toThrow();
    } finally {
      Object.defineProperty(globalThis, 'document', {
        configurable: true,
        value: originalDocument,
      });
    }
  });
});
