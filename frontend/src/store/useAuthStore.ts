import { create } from 'zustand';

function getCookie(name: string): string | null {
  const m = document.cookie.match(new RegExp(`(?:^|;\\s*)${name}=([^;]*)`));
  return m ? decodeURIComponent(m[1]) : null;
}

function clearCookie(name: string) {
  document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/`;
}

interface AuthState {
  loggedIn: boolean;
  login: () => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()((set) => ({
  loggedIn: !!getCookie('AUTHORIZATION'),
  login: () => set({ loggedIn: true }),
  logout: () => {
    clearCookie('AUTHORIZATION');
    set({ loggedIn: false });
  },
}));
