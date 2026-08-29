import { lazy, Suspense, type ReactNode } from 'react'
import { Spin } from 'antd'
import { createBrowserRouter, Navigate } from 'react-router-dom'
import AppLayout from './layouts/AppLayout'

const Overview = lazy(() => import('./pages/Overview'))
const Tasks = lazy(() => import('./pages/Tasks'))
const TaskNew = lazy(() => import('./pages/tasks/TaskNew'))
const TaskDetail = lazy(() => import('./pages/tasks/TaskDetail'))
const Bugs = lazy(() => import('./pages/Bugs'))
const Datasets = lazy(() => import('./pages/Datasets'))
const Metadata = lazy(() => import('./pages/Metadata'))
const MetadataWizard = lazy(() => import('./pages/MetadataWizard'))
const Archives = lazy(() => import('./pages/Archives'))
const Settings = lazy(() => import('./pages/Settings'))
const V2Tasks = lazy(() => import('./pages/v2/V2Tasks'))
const V2TaskDetail = lazy(() => import('./pages/v2/V2TaskDetail'))

const page = (element: ReactNode) => <Suspense fallback={<Spin fullscreen tip="加载工作区…" />}>{element}</Suspense>

/** 任务中心路由：列表 / 新建 / 详情（详情页承载全部阶段工作区） */
export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/overview" replace /> },
      { path: 'overview', element: page(<Overview />) },
      { path: 'tasks', element: page(<Tasks />) },
      { path: 'tasks/new', element: page(<TaskNew />) },
      { path: 'tasks/:id', element: page(<TaskDetail />) },
      { path: 'v2/tasks', element: page(<V2Tasks />) },
      { path: 'v2/tasks/:id', element: page(<V2TaskDetail />) },
      { path: 'bugs', element: page(<Bugs />) },
      { path: 'datasets', element: page(<Datasets />) },
      { path: 'metadata', element: page(<Metadata />) },
      { path: 'metadata/wizard', element: page(<MetadataWizard />) },
      { path: 'archives', element: page(<Archives />) },
      { path: 'settings', element: page(<Settings />) },
    ],
  },
])
