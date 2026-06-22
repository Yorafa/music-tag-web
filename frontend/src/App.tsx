import { LoginPage } from '@/pages/LoginPage';
import { HomePage } from '@/pages/HomePage';
import { useAuthStore } from '@/store/useAuthStore';

function App() {
  const loggedIn = useAuthStore((s) => s.loggedIn);

  if (!loggedIn) {
    return <LoginPage />;
  }
  return <HomePage />;
}

export default App;
