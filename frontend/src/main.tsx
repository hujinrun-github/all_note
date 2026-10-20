import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { router } from './router'
import { installDeployRecovery } from './utils/deployRecovery'
import './styles/index.css'
import './styles/task-domain-v2.css'
import './styles/roadmap-plan.css'
import './styles/workspace-responsive.css'
import './styles/workspace-desktop.css'

installDeployRecovery()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>
)
