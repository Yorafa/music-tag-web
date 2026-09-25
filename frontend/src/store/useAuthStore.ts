import { create } from 'zustand';
import { readString, writeString, removeString } from '@/utils/persist';

interface AuthState {
  loggedIn: boolean;
  /** raw JWT access token from /api/token/. The request interceptor prefixes
   *  'JWT ' before sending — see api/client.ts.
   *
   *  Persistence: persisted to localStorage under `auth.accessToken` so a page
   *  reload keeps the user signed in (matches the Python simplejwt that the
   *  backend mimics; tokens are 7-day TTL per handler/auth.go::generateJWT).
   *  XSS exposure is bounded to that 7-day window vs. the prior in-memory-only
   *  design that was session-length. Acceptable for a self-hosted admin tool;
   *  the alternative (HttpOnly cookie) is a backend change left out of this
   *  scope.
   *
   *  ALSO mirrored to a non-HttpOnly `AUTHORIZATION` cookie on every login()
   *  / logout(). Reasons:
   *    1. The native `<audio>` element on the bottom player bar fetches
   *       `audio.src` directly — it cannot bear the `Authorization: JWT <token>`
   *       header the Axios interceptor attaches to XHR. Without a cookie
   *       fallback, the JWTAuth middleware (internal/gateway/middleware/auth.go)
   *       aborts the request with 401 and the user sees "试听链接不可用".
   *    2. The middleware already reads the `AUTHORIZATION` cookie as a
   *       fallback to the header — see auth.go — so writing the cookie here
   *       is a one-line storefront, no backend surface.
   *    3. Cookie exposure is bounded to the same 7-day window as the JWT
   *       TTL, and SameSite=Lax + path=/ keeps CSRF on a tight leash for
   *       any cross-site POST. HttpOnly is not settable from JS so XSS
   *       damage is identical to the localStorage copy that's already
   *       written; one more attacker-readable copy of the same secret is
   *       not a meaningful security delta.
   *  Trade-off documented in docs/plugable-plugins.md §11.5 (browsers
   *  without the cookie fallback get exactly the behaviour they had
   *  before this commit). */
  accessToken: string | null;
  login: (accessToken: string) => void;
  logout: () => void;
}

const TOKEN_KEY = 'auth.accessToken';
/** Cookie name matches what internal/gateway/middleware/auth.go reads:
 *  `cookie, _ := c.Cookie("AUTHORIZATION")`. Keep in lockstep with that
 *  file. `path=/` so /api/* (where the cookie is consumed) is covered;
 *  `max-age=604800` mirrors the 7-day TTL on JWT mint in
 *  handler/auth.go::generateJWT so the cookie doesn't outlive the token. */
const AUTH_COOKIE_NAME = 'AUTHORIZATION';
const AUTH_COOKIE_MAX_AGE_SECONDS = 7 * 24 * 3600;

/** Mirror the JWT to a cookie so native HTML elements (notably
 *  `<audio>`) can authenticate /api/stream requests. Best-effort: if
 *  document.cookie write fails (private-browsing mode in some browsers,
 *  third-party-context restrictions, etc.) we swallow silently and let
 *  the localStorage-only path continue working for XHR. */
function setAuthCookie(token: string) {
  if (typeof document === 'undefined') return;
  // SameSite=Lax: cookie is sent on same-site requests (which /api/*
  // is — same-origin SPA) and on top-level GET navigations. Lax blocks
  // cross-site POST CSRF without breaking our cross-page navigation.
  //
  // Secure (REVIEW.md P1-1) is added only under HTTPS. Emitting it
  // unconditionally would make browsers drop the cookie on a plain-HTTP
  // install, breaking every <audio> stream — the common case for a
  // self-hosted tool on http://192.168.x.x.
  const parts = [
    `${AUTH_COOKIE_NAME}=${encodeURIComponent(token)}`,
    'path=/',
    `max-age=${AUTH_COOKIE_MAX_AGE_SECONDS}`,
    'SameSite=Lax',
  ];
  if (typeof location !== 'undefined' && location.protocol === 'https:') {
    parts.push('Secure');
  }
  document.cookie = parts.join('; ');
}

function clearAuthCookie() {
  if (typeof document === 'undefined') return;
  document.cookie = `${AUTH_COOKIE_NAME}=; path=/; max-age=0`;
}

/** Read the persisted token synchronously on store creation. The lazy
 *  initializer in `create()` runs once per page load (not per render), so
 *  the cost is one localStorage.getItem at module-load time. */
function loadStoredToken(): string | null {
  // readString returns `null` after `logout()`'s removeString; treat any
  // falsy raw (null or '') as absent. Defensive against external clobber
  // (e.g. another tab writing '' from DevTools).
  const raw = readString(TOKEN_KEY);
  if (!raw) return null;
  return raw;
}

export const useAuthStore = create<AuthState>()((set) => {
  const initialToken = loadStoredToken();
  // Re-hydrate the cookie mirror if we already had a token in
  // localStorage at boot. Covers the "page reload with persisted token"
  // path so the user doesn't have to log out + in to recover `<audio>`
  // playback after a browser restart. Idempotent — same write produces
  // same cookie state.
  if (initialToken) setAuthCookie(initialToken);
  return {
    loggedIn: initialToken !== null,
    accessToken: initialToken,
    login: (accessToken) => {
      // Persist before set() so any concurrent re-render triggered by set
      // already sees the token in localStorage. Quota errors are swallowed
      // (private-browsing mode, etc.) — falling back to in-memory only, which
      // matches the pre-this-merge behaviour.
      writeString(TOKEN_KEY, accessToken);
      setAuthCookie(accessToken);
      set({ loggedIn: true, accessToken });
    },
    logout: () => {
      // Persist before set() so any concurrent re-render triggered by set
      // already sees the cleared state in localStorage.
      removeString(TOKEN_KEY);
      clearAuthCookie();
      set({ loggedIn: false, accessToken: null });
    },
  };
});

