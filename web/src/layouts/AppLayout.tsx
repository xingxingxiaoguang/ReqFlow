import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { ProLayout } from '@ant-design/pro-components'
import {
  DashboardOutlined, FileAddOutlined, BugOutlined,
  CloudSyncOutlined, HistoryOutlined, SettingOutlined, ThunderboltFilled,
} from '@ant-design/icons'
import type React from 'react'
import { Badge, Tooltip, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { SettingsView } from '../api/types'

const menu = {
  path: '/',
  routes: [
    { path: '/overview', name: '概览', icon: <DashboardOutlined /> },
    { path: '/import', name: '需求导入', icon: <FileAddOutlined /> },
    { path: '/bugs', name: 'Bug 处理', icon: <BugOutlined /> },
    { path: '/sync', name: '数据同步', icon: <CloudSyncOutlined /> },
    { path: '/records', name: '导入记录', icon: <HistoryOutlined /> },
    { path: '/settings', name: '设置', icon: <SettingOutlined /> },
  ],
}

/** 全局外壳：侧边栏流程导航 + 顶栏连接状态灯 */
export default function AppLayout() {
  const location = useLocation()
  const navigate = useNavigate()
  const { data: settings } = useQuery({
    queryKey: ['settings'],
    queryFn: () => api.get<SettingsView>('/api/settings'),
  })

  const okCount =
    settings ? [settings.llm.configured, settings.pingcode.configured, settings.embedding.configured].filter(Boolean).length : 0
  const allOk = okCount === 3

  return (
    <ProLayout
      title="ReqFlow"
      logo={<ThunderboltFilled style={{ color: '#818cf8', fontSize: 26 }} />}
      layout="mix"
      fixSiderbar
      route={menu}
      location={{ pathname: location.pathname }}
      menuItemRender={(item: { path?: string }, dom: React.ReactNode) => (
        <a onClick={() => item.path && navigate(item.path)}>{dom}</a>
      )}
      token={{ header: { colorHeaderTitle: '#111827' } }}
      avatarProps={{
        render: () => (
          <Tooltip title={allOk ? '所有连接已配置' : '部分依赖未配置（见设置页）'}>
            <Badge status={allOk ? 'success' : 'warning'} text={<Typography.Text style={{ color: '#6b7280' }}>连接 {okCount}/3</Typography.Text>} />
          </Tooltip>
        ),
      }}
      contentStyle={{ paddingInline: 0 }}
    >
      <Outlet />
    </ProLayout>
  )
}
