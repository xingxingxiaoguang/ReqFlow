import { lazy, Suspense, type ReactNode } from 'react'
import { Spin } from 'antd'
import { createBrowserRouter, Navigate } from 'react-router-dom'
import AppLayout from './layouts/AppLayout'

const Settings = lazy(() => import('./pages/Settings'))
const AgentHome = lazy(() => import('./pages/agent/AgentHome'))
const V2Tasks = lazy(() => import('./pages/v2/V2Tasks'))
const V2TaskDetail = lazy(() => import('./pages/v2/V2TaskDetail'))
const NoCodeTaskNew = lazy(() => import('./pages/v2/NoCodeTaskNew'))
const V2Definitions = lazy(() => import('./pages/v2/V2Definitions'))
const V2DefinitionNew = lazy(() => import('./pages/v2/V2DefinitionNew'))
const V2DefinitionDetail = lazy(() => import('./pages/v2/V2DefinitionDetail'))
const V2Datasets = lazy(() => import('./pages/v2/V2Datasets'))
const V2DatasetDetail = lazy(() => import('./pages/v2/V2DatasetDetail'))
const V2Archives = lazy(() => import('./pages/v2/V2Archives'))

const page = (element: ReactNode) => <Suspense fallback={<Spin fullscreen tip="加载工作区…" />}>{element}</Suspense>

/** 新产品路由树：元数据能力只嵌入流程和数据上下文，不再提供独立模块。 */
export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/agent" replace /> },
      { path: 'agent', element: page(<AgentHome />) },
      { path: 'agent/:sessionId', element: page(<AgentHome />) },
      { path: 'definitions', element: page(<V2Definitions />) },
      { path: 'definitions/new', element: page(<V2DefinitionNew />) },
      { path: 'definitions/:id', element: page(<V2DefinitionDetail />) },
      { path: 'datasets', element: page(<V2Datasets />) },
      { path: 'datasets/:id', element: page(<V2DatasetDetail />) },
      { path: 'tasks', element: page(<V2Tasks />) },
      { path: 'tasks/new', element: page(<NoCodeTaskNew />) },
      { path: 'tasks/:id', element: page(<V2TaskDetail />) },
      { path: 'archives', element: page(<V2Archives />) },
      { path: 'settings', element: page(<Settings />) },
      { path: '*', element: <Navigate to="/agent" replace /> },
    ],
  },
])
