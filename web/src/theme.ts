import type { ThemeConfig } from 'antd'

/** 品牌主题：靛蓝主色 + 大圆角 + 紧凑行高，区别于默认后台风 */
export const reqflowTheme: ThemeConfig = {
  token: {
    colorPrimary: '#4F46E5',
    colorInfo: '#4F46E5',
    colorLink: '#4F46E5',
    borderRadius: 8,
    fontSize: 14,
    controlHeight: 36,
    colorBgLayout: '#f5f6fa',
    colorTextHeading: '#111827',
  },
  components: {
    Layout: {
      siderBg: '#101322',
      headerBg: '#ffffff',
      bodyBg: '#f5f6fa',
    },
    Menu: {
      darkItemBg: '#101322',
      darkItemSelectedBg: '#4F46E5',
      darkItemColor: '#9aa1b5',
      darkItemSelectedColor: '#ffffff',
      itemBorderRadius: 8,
      itemMarginInline: 10,
    },
    Card: { paddingLG: 20 },
    Steps: { titleLineHeight: 1.6 },
  },
}
