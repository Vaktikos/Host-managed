import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import './i18n'
import './styles.css'
import { AuthProvider, useAuth } from './auth'
import { ResourceProvider } from './resources'
import { Layout } from './components/Layout'
import { ToastProvider } from './components/ui'
import { Login } from './pages/Login'
import { Overview } from './pages/Overview'
import { VMs } from './pages/VMs'
import { VMDetail } from './pages/VMDetail'
import { ContainerDetail, Containers } from './pages/Containers'
import { Backups } from './pages/Backups'
import { Storage } from './pages/Storage'
import { Network } from './pages/Network'
import { TaskDetail, Tasks } from './pages/Tasks'
import { Audit } from './pages/Audit'
import { Users } from './pages/Users'
import { Account } from './pages/Account'

function Guard({ perm, children }: { perm: string; children: JSX.Element }) {
  const { can } = useAuth()
  const { t } = useTranslation()
  if (!can(perm)) return <div className="page"><div className="alert warn">{t('errors.forbidden')}</div></div>
  return children
}

function App() {
  const { me, ready } = useAuth()
  const { t } = useTranslation()
  if (!ready) return <div className="login faint">{t('common.loading')}</div>
  if (!me) return <Login />
  return (
    <ResourceProvider>
      <Layout>
        <Routes>
          <Route path="/" element={<Guard perm="sys.view"><Overview /></Guard>} />
          <Route path="/vms" element={<Guard perm="vm.view"><VMs /></Guard>} />
          <Route path="/vms/:name" element={<Guard perm="vm.view"><VMDetail /></Guard>} />
          <Route path="/containers" element={<Guard perm="ct.view"><Containers /></Guard>} />
          <Route path="/containers/:name" element={<Guard perm="ct.view"><ContainerDetail /></Guard>} />
          <Route path="/storage" element={<Guard perm="storage.view"><Storage /></Guard>} />
          <Route path="/network" element={<Guard perm="network.view"><Network /></Guard>} />
          <Route path="/backups" element={<Guard perm="backup.view"><Backups /></Guard>} />
          <Route path="/tasks" element={<Guard perm="jobs.view"><Tasks /></Guard>} />
          <Route path="/tasks/:id" element={<Guard perm="jobs.view"><TaskDetail /></Guard>} />
          <Route path="/audit" element={<Guard perm="sys.audit"><Audit /></Guard>} />
          <Route path="/users" element={<Guard perm="sys.users"><Users /></Guard>} />
          <Route path="/account" element={<Account />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </Layout>
    </ResourceProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <ToastProvider>
        <AuthProvider>
          <App />
        </AuthProvider>
      </ToastProvider>
    </BrowserRouter>
  </StrictMode>,
)
