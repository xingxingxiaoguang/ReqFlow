import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { ProLayout } from '@ant-design/pro-components'
import {
  DatabaseOutlined, UnorderedListOutlined, ThunderboltFilled, InboxOutlined,
  BranchesOutlined, RobotOutlined, SettingOutlined,
} from '@ant-design/icons'
import type React from 'react'
import { Badge, Tooltip, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { platformConfigsApi } from '../api/platformConfigs'

const menu = {
  path: '/',
  routes: [
    { path: '/agent', name: '数字大脑', icon: <RobotOutlined /> },
    { path: '/workflows', name: '线性工作流', icon: <BranchesOutlined /> },
    { path: '/datasets', name: '数据管理', icon: <DatabaseOutlined /> },
    { path: '/tasks', name: '任务管理', icon: <UnorderedListOutlined /> },
    { path: '/archives', name: '归档管理', icon: <InboxOutlined /> },
    { path: '/settings', name: '平台配置', icon: <SettingOutlined /> },
  ],
}

/** 全局外壳：侧边栏流程导航 + 顶栏连接状态灯 */
export default function AppLayout() {
  const location = useLocation()
  const navigate = useNavigate()
  const { data: configs } = useQuery({
    queryKey: ['platform-configs'],
    queryFn: platformConfigsApi.catalog,
  })

  const okCount =
    configs
      ? [configs.summary.llm, configs.summary.embedding, configs.summary.rerank, configs.summary.mineru]
          .filter(Boolean).length
      : 0
  const allOk = okCount === 4

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
          <Tooltip title={allOk ? '所有连接已配置；点击查看设置' : '部分依赖未配置；点击查看设置'}>
            <Badge onClick={() => navigate('/settings')} style={{ cursor: 'pointer' }} status={allOk ? 'success' : 'warning'} text={<Typography.Text style={{ color: '#6b7280' }}>连接 {okCount}/4</Typography.Text>} />
          </Tooltip>
        ),
      }}
      contentStyle={{ paddingInline: 0 }}
    >
      <Outlet />
    </ProLayout>
  )
}
