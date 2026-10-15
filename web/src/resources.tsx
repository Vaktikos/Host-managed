import { ReactNode, createContext, useContext } from 'react'
import { api } from './api/client'
import type { Container, Job, VM } from './api/types'
import { useAuth } from './auth'
import { Loadable, usePoll } from './lib/hooks'

// Shared, polled resource lists for the tree, search and lists. One request
// per interval instead of one per component.
interface Resources {
  vms: Loadable<VM[]>
  cts: Loadable<Container[]>
  jobs: Loadable<{ items: Job[]; total: number }>
  refreshAll: () => void
}

const Ctx = createContext<Resources>(null as unknown as Resources)

export function ResourceProvider({ children }: { children: ReactNode }) {
  const { can } = useAuth()
  const vms = usePoll(() => (can('vm.view') ? api.get<VM[]>('/vms') : Promise.resolve([])), 4000)
  const cts = usePoll(() => (can('ct.view') ? api.get<Container[]>('/containers') : Promise.resolve([])), 5000)
  const jobs = usePoll(() => (can('jobs.view') ? api.get<{ items: Job[]; total: number }>('/jobs?limit=20') : Promise.resolve({ items: [], total: 0 })), 3000)
  const refreshAll = () => {
    vms.reload()
    cts.reload()
    jobs.reload()
  }
  return <Ctx.Provider value={{ vms, cts, jobs, refreshAll }}>{children}</Ctx.Provider>
}

export const useResources = () => useContext(Ctx)
