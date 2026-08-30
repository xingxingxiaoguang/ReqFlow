import { lazy, Suspense, type ReactNode } from 'react'
import { Spin } from 'antd'
import { createBrowserRouter, Navigate } from 'react-router-dom'
import AppLayout from './layouts/AppLayout'

const Settings = lazy(() => import('./pages/Settings'))
const V2Tasks = lazy(() => import('./pages/v2/V2Tasks'))
const V2TaskDetail = lazy(() => import('./pages/v2/V2TaskDetail'))
const NoCodeTaskNew = lazy(() => import('./pages/v2/NoCodeTaskNew'))
const V2Definitions = lazy(() => import('./pages/v2/V2Definitions'))
const V2Datasets = lazy(() => import('./pages/v2/V2Datasets'))
const V2Metadata = lazy(() => import('./pages/v2/V2Metadata'))
const V2Retrieval = lazy(() => import('./pages/v2/V2Retrieval'))
const V2Artifacts = lazy(() => import('./pages/v2/V2Artifacts'))
const V2Archives = lazy(() => import('./pages/v2/V2Archives'))

const page = (element: ReactNode) => <Suspense fallback={<Spin fullscreen tip="加载工作区…" />}>{element}</Suspense>

/** 纯 V2 路由。旧 TaskManager 页面不再进入产品路由树。 */
export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/tasks" replace /> },
      { path: 'tasks', element: page(<V2Tasks />) },
      { path: 'tasks/new', element: page(<NoCodeTaskNew />) },
      { path: 'tasks/:id', element: page(<V2TaskDetail />) },
      { path: 'definitions', element: page(<V2Definitions />) },
      { path: 'datasets', element: page(<V2Datasets />) },
      { path: 'metadata', element: page(<V2Metadata />) },
      { path: 'retrieval', element: page(<V2Retrieval />) },
      { path: 'artifacts', element: page(<V2Artifacts />) },
      { path: 'archives', element: page(<V2Archives />) },
      { path: 'settings', element: page(<Settings />) },
      { path: '*', element: <Navigate to="/tasks" replace /> },
    ],
  },
])
