import type { AdminPage } from './routes'

export type NavItem = { label: string; page: AdminPage; icon: string }
export type NavGroup = { label: string; items: NavItem[] }
export type NavRoleFlags = { access: boolean; manage: boolean; review: boolean; observe: boolean }

// Grouped management navigation. Empty groups are omitted; visibility mirrors
// the server-side role checks (the UI never grants anything by itself).
export function groupedNavigation(flags: NavRoleFlags): NavGroup[] {
  const operations = flags.manage || flags.review || flags.observe
  const groups: NavGroup[] = [
    {
      label: '运营',
      items: ([
        { label: '运行状态', page: 'overview', icon: '◫' },
        { label: '结果度量', page: 'outcomes', icon: '◔' },
        { label: '报告', page: 'reports', icon: '▥' },
        { label: '评测报告', page: 'report', icon: '▣' },
        { label: '情报观察', page: 'intel', icon: '◉' },
        { label: '内容缺口', page: 'gaps', icon: '◌' },
        { label: '内容巡检', page: 'inspections', icon: '◎' },
        { label: '店铺范围', page: 'shops', icon: '⌂' },
      ] as NavItem[]).filter(() => operations),
    },
    {
      label: '知识',
      items: ([
        { label: '资料来源', page: 'sources', icon: '▤' },
        { label: '摄取任务', page: 'ingestion', icon: '↻' },
        { label: '版本审核', page: 'reviews', icon: '✓' },
        { label: '回答与引用', page: 'answers', icon: '❝' },
      ] as NavItem[]).filter((item) => {
        if (item.page === 'answers') return flags.review || flags.manage
        return item.page === 'reviews' ? flags.review : operations
      }),
    },
    {
      label: '权限',
      items: ([
        { label: '账号与权限', page: 'accounts', icon: '♙' },
        { label: '权限申请', page: 'requests', icon: '⇄' },
        { label: '授权审计', page: 'audit', icon: '≡' },
      ] as NavItem[]).filter((item) => {
        if (item.page === 'accounts') return flags.access || flags.manage
        if (item.page === 'audit') return flags.access || flags.review
        return true
      }),
    },
  ]
  return groups.filter((group) => group.items.length > 0)
}
