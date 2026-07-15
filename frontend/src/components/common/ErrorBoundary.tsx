import { Component, type ReactNode } from 'react';
import { Button } from '@/components/ui/button';

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * Top-level React error boundary. Catches render-time exceptions
 * anywhere below the root and renders a recoverable fallback panel
 * instead of dumping the user onto a blank page.
 *
 * The very class of bug we're currently diagnosing (clicking song
 * detail → white screen) went undetected precisely because no
 * boundary existed; React unmounts the entire tree silently on an
 * uncaught render exception. This boundary surfaces the stack to
 * the user (and to `console.error` for DevTools) so we can localize
 * the next one in seconds rather than hours.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: { componentStack?: string }) {
     
    console.error('[ErrorBoundary] uncaught render exception', error, info.componentStack);
  }

  reset = () => this.setState({ error: null });

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <div className="flex items-center justify-center min-h-screen bg-background text-foreground p-6">
        <div className="max-w-xl w-full rounded-lg border border-border bg-card p-6 shadow-sm space-y-4">
          <div className="space-y-1">
            <h1 className="text-lg font-semibold">出现错误</h1>
            <p className="text-sm text-muted-foreground">
              渲染过程中捕获到未处理的异常。完整堆栈已写入 DevTools 控制台，请复制给开发同学。
            </p>
          </div>
          <pre className="text-xs font-mono whitespace-pre-wrap break-words rounded-md bg-muted p-3 max-h-72 overflow-auto">
            {error.message}
            {error.stack ? `\n\n${error.stack}` : ''}
          </pre>
          <div className="flex gap-2">
            <Button onClick={this.reset}>重试</Button>
            <Button variant="outline" onClick={() => window.location.reload()}>
              刷新页面
            </Button>
          </div>
        </div>
      </div>
    );
  }
}
