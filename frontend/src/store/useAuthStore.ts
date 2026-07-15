import { create } from 'zustand';

interface AuthState {
  loggedIn: boolean;
  /** raw JWT access token from /api/token/. The request interceptor prefixes
   *  'JWT ' before sending — see api/client.ts. NOT persisted: a page reload
   *  drops the token and forces re-login, which is acceptable and limits
   *  XSS-token-theft exposure window. */
  accessToken: string | null;
  login: (accessToken: string) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()((set) => ({
  loggedIn: false,
  accessToken: null,
  login: (accessToken) => set({ loggedIn: true, accessToken }),
  logout: () => set({ loggedIn: false, accessToken: null }),
}));
