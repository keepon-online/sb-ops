import { App as AntApp } from 'antd';
import { Suspense, lazy } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { Spin } from 'antd';
import { AuthGuard } from '../components/AuthGuard';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { AppLayout } from '../layout/AppLayout';
import { NotFoundPage } from '../pages/NotFoundPage';

const SetupPage = lazy(() => import('../features/auth/pages/SetupPage').then((m) => ({ default: m.SetupPage })));
const LoginPage = lazy(() => import('../features/auth/pages/LoginPage').then((m) => ({ default: m.LoginPage })));
const UsersPage = lazy(() => import('../features/users/pages/UsersPage').then((m) => ({ default: m.UsersPage })));
const ProtocolsPage = lazy(() => import('../features/protocols/pages/ProtocolsPage').then((m) => ({ default: m.ProtocolsPage })));
const PoliciesPage = lazy(() => import('../features/policies/pages/PoliciesPage').then((m) => ({ default: m.PoliciesPage })));
const SingBoxPage = lazy(() => import('../features/singbox/pages/SingBoxPage').then((m) => ({ default: m.SingBoxPage })));
const OverviewPage = lazy(() => import('../features/overview/pages/OverviewPage').then((m) => ({ default: m.OverviewPage })));
const AuditPage = lazy(() => import('../features/audit/pages/AuditPage').then((m) => ({ default: m.AuditPage })));
const TrafficPage = lazy(() => import('../features/traffic/pages/TrafficPage').then((m) => ({ default: m.TrafficPage })));
const AlertsPage = lazy(() => import('../features/alerts/pages/AlertsPage').then((m) => ({ default: m.AlertsPage })));
const MonitorPage = lazy(() => import('../features/monitor/pages/MonitorPage').then((m) => ({ default: m.MonitorPage })));
const SystemPage = lazy(() => import('../features/system/pages/SystemPage').then((m) => ({ default: m.SystemPage })));

export default function App() {
  return (
    <AntApp>
      <BrowserRouter>
        <Suspense fallback={<div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100vh' }}><Spin size="large" /></div>}>
          <Routes>
            <Route path="/setup" element={<SetupPage />} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/" element={<AuthGuard><AppLayout /></AuthGuard>}>
              <Route index element={<Navigate to="/overview" replace />} />
              <Route path="overview" element={<ErrorBoundary><OverviewPage /></ErrorBoundary>} />
              <Route path="users" element={<ErrorBoundary><UsersPage /></ErrorBoundary>} />
              <Route path="protocols" element={<ErrorBoundary><ProtocolsPage /></ErrorBoundary>} />
              <Route path="traffic" element={<ErrorBoundary><TrafficPage /></ErrorBoundary>} />
              <Route path="alerts" element={<ErrorBoundary><AlertsPage /></ErrorBoundary>} />
              <Route path="policies" element={<ErrorBoundary><PoliciesPage /></ErrorBoundary>} />
              <Route path="singbox" element={<ErrorBoundary><SingBoxPage /></ErrorBoundary>} />
              <Route path="monitor" element={<ErrorBoundary><MonitorPage /></ErrorBoundary>} />
              <Route path="system" element={<ErrorBoundary><SystemPage /></ErrorBoundary>} />
              <Route path="audit" element={<ErrorBoundary><AuditPage /></ErrorBoundary>} />
              <Route path="*" element={<NotFoundPage />} />
            </Route>
            <Route path="*" element={<NotFoundPage />} />
          </Routes>
        </Suspense>
      </BrowserRouter>
    </AntApp>
  );
}
