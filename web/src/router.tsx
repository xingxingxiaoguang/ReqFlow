import { createBrowserRouter, Navigate } from 'react-router-dom'
import AppLayout from './layouts/AppLayout'
import Overview from './pages/Overview'
import Tasks from './pages/Tasks'
import TaskNew from './pages/tasks/TaskNew'
import TaskDetail from './pages/tasks/TaskDetail'
import Bugs from './pages/Bugs'
import Datasets from './pages/Datasets'
import Metadata from './pages/Metadata'
import Archives from './pages/Archives'
import Settings from './pages/Settings'

/** 任务中心路由：列表 / 新建 / 详情（详情页承载全部阶段工作区） */
export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/overview" replace /> },
      { path: 'overview', element: <Overview /> },
      { path: 'tasks', element: <Tasks /> },
      { path: 'tasks/new', element: <TaskNew /> },
      { path: 'tasks/:id', element: <TaskDetail /> },
      { path: 'bugs', element: <Bugs /> },
      { path: 'datasets', element: <Datasets /> },
      { path: 'metadata', element: <Metadata /> },
      { path: 'archives', element: <Archives /> },
      { path: 'settings', element: <Settings /> },
    ],
  },
])
