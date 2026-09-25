import { useState } from 'react';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Radio, Lock, User, Loader2 } from 'lucide-react';
import { useAuthStore } from '@/store/useAuthStore';
import { ThemeToggle } from '@/components/ThemeToggle';

const API_BASE = '';

export function LoginPage() {
  const login = useAuthStore((s) => s.login);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const res = await fetch(`${API_BASE}/api/token/`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      });
      const data = (await res.json()) as {
        result: boolean;
        message: string;
        data?: { access?: string; refresh?: string };
      };
      if (!data.result || !data.data?.access) {
        setError(data.message || '登录失败');
        return;
      }
      login(data.data.access);
    } catch {
      setError('网络错误，请重试');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-4 relative overflow-hidden select-none">
      {/* Subtle ambient lighting gradients */}
      <div className="absolute top-[-10%] left-[-10%] w-[500px] h-[500px] rounded-full bg-indigo-500/10 blur-[120px] pointer-events-none" />
      <div className="absolute bottom-[-10%] right-[-10%] w-[500px] h-[500px] rounded-full bg-purple-500/10 blur-[120px] pointer-events-none" />

      <div className="absolute top-5 right-5 z-20">
        <ThemeToggle />
      </div>

      <Card className="w-full max-w-sm glass-floating border-border/80 rounded-2xl shadow-2xl relative z-10 overflow-hidden">
        <div className="h-1.5 w-full bg-gradient-to-r from-indigo-500 via-purple-500 to-pink-500" />
        <CardHeader className="text-center pb-3 pt-6">
          <div className="mx-auto mb-3.5 w-14 h-14 rounded-2xl bg-gradient-to-tr from-indigo-500 via-purple-500 to-pink-500 flex items-center justify-center shadow-lg shadow-indigo-500/25 text-white">
            <Radio className="w-7 h-7 animate-pulse" />
          </div>
          <CardTitle className="text-lg font-bold tracking-tight">Music Tag Web</CardTitle>
          <p className="text-xs text-muted-foreground mt-1">音乐元数据管理与即时试听控制台</p>
        </CardHeader>
        <CardContent className="pb-6">
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-foreground/80 flex items-center gap-1.5">
                <User className="w-3.5 h-3.5 text-muted-foreground" />
                <span>用户名</span>
              </label>
              <Input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="admin"
                className="h-10 text-xs rounded-xl bg-background/60"
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-foreground/80 flex items-center gap-1.5">
                <Lock className="w-3.5 h-3.5 text-muted-foreground" />
                <span>密码</span>
              </label>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••"
                className="h-10 text-xs rounded-xl bg-background/60"
              />
            </div>
            {error && (
              <div className="p-2.5 rounded-xl bg-red-500/10 border border-red-500/30 text-red-400 text-xs">
                {error}
              </div>
            )}
            <Button
              type="submit"
              className="w-full h-10 rounded-xl text-xs font-semibold shadow-md shadow-primary/20 mt-2"
              disabled={loading}
            >
              {loading ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  登录中…
                </>
              ) : (
                '登 录'
              )}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
