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
   *  scope. */
  accessToken: string | null;
  login: (accessToken: string) => void;
  logout: () => void;
}

const TOKEN_KEY = 'auth.accessToken';

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
  return {
    loggedIn: initialToken !== null,
    accessToken: initialToken,
    login: (accessToken) => {
      // Persist before set() so any concurrent re-render triggered by set
      // already sees the token in localStorage. Quota errors are swallowed
      // (private-browsing mode, etc.) — falling back to in-memory only, which
      // matches the pre-this-merge behaviour.
      writeString(TOKEN_KEY, accessToken);
      set({ loggedIn: true, accessToken });
    },
    logout: () => {
      // Persist before set() so any concurrent re-render triggered by set
      // already sees the cleared state in localStorage.
      removeString(TOKEN_KEY);
      set({ loggedIn: false, accessToken: null });
    },
  };
});
