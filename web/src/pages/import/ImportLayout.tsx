import { Steps } from 'antd'
import { Outlet, useLocation, Navigate } from 'react-router-dom'
import { useImportWizard } from '../../stores/importWizard'

/** 需求导入向导壳：阶段条 + 子路由。阶段由 URL 驱动（反「隐形跳转」设计）。 */
export default function ImportLayout() {
  const location = useLocation()
  const stage = location.pathname.split('/')[2] ?? 'upload'
  const { analyzing } = useImportWizard()

  const stepIndex = { upload: 0, review: 1, analyzing: 2, result: 3 }[stage] ?? 0
  // 分析完成自动落在 result；analyzing 页仅在流进行中停留
  if (stage === 'analyzing' && !analyzing) {
    return <Navigate to="/import/result" replace />
  }

  return (
    <div style={{ padding: '0 4px' }}>
      <Steps
        current={stepIndex}
        size="small"
        style={{ marginBottom: 20, maxWidth: 760 }}
        items={[
          { title: '上传文档', description: 'docx / pdf / md / txt' },
          { title: '确认解析', description: '预览与修正全文' },
          { title: 'AI 分析', description: '流式提取工作项' },
          { title: '匹配与导入', description: '项目推荐 · 查重 · 建单' },
        ]}
      />
      <Outlet />
    </div>
  )
}
