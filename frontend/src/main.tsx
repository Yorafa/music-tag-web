import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { TooltipProvider } from '@/components/ui/tooltip';
import App from './App';
import './index.css';
// Theme is applied by useThemeStore module on import (reads localStorage + system pref).
import { applyThemeClass, useThemeStore } from '@/store/useThemeStore';
applyThemeClass(useThemeStore.getState().theme);

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <TooltipProvider>
      <App />
    </TooltipProvider>
  </StrictMode>,
);
