import { createBrowserRouter, Navigate } from 'react-router-dom'
import AppLayout from './layouts/AppLayout'
import Overview from './pages/Overview'
import ImportLayout from './pages/import/ImportLayout'
import Upload from './pages/import/Upload'
import Review from './pages/import/Review'
import Analyzing from './pages/import/Analyzing'
import Result from './pages/import/Result'
import Bugs from './pages/Bugs'
import Sync from './pages/Sync'
import Records from './pages/Records'
import Settings from './pages/Settings'

/** URL 驱动的向导阶段：刷新/回退/分享不丢状态 */
export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/overview" replace /> },
      { path: 'overview', element: <Overview /> },
      {
        path: 'import',
        element: <ImportLayout />,
        children: [
          { index: true, element: <Navigate to="/import/upload" replace /> },
          { path: 'upload', element: <Upload /> },
          { path: 'review', element: <Review /> },
          { path: 'analyzing', element: <Analyzing /> },
          { path: 'result', element: <Result /> },
        ],
      },
      { path: 'bugs', element: <Bugs /> },
      { path: 'sync', element: <Sync /> },
      { path: 'records', element: <Records /> },
      { path: 'settings', element: <Settings /> },
    ],
  },
])
