import { StrictMode, type ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import App from './App.tsx';
import { queryClient } from './queryClient';
import './index.css';

// The app's single provider tree, at the root and wrapped in StrictMode. It is
// exported so queryClient.test.tsx can render the exact composition main mounts
// and prove the tree is handed the shared client: regressing this to a
// locally-built `new QueryClient` fails that test, instead of quietly reviving
// the second-client trap. main.tsx below only mounts it.
export function AppProviders({ children }: { children: ReactNode }) {
  return (
    <StrictMode>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </StrictMode>
  );
}

// Guard on the container so importing this module in a test does not mount the
// app; in the browser #root is always present.
const container = document.getElementById('root');
if (container) {
  createRoot(container).render(
    <AppProviders>
      <App />
    </AppProviders>,
  );
}
