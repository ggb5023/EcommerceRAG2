export const adminPages = [
  'overview', 'outcomes', 'reports', 'report', 'intel', 'gaps', 'inspections', 'accounts', 'requests', 'audit', 'shops', 'sources', 'ingestion', 'reviews', 'answers',
] as const

export type AdminPage = typeof adminPages[number]
export type AdminRoutePage = AdminPage | 'not-found'

const paths: Record<AdminPage, string> = {
  overview: '/admin/overview',
  outcomes: '/admin/merchant/outcomes',
  reports: '/admin/reports',
  report: '/admin/report',
  intel: '/admin/intel',
  gaps: '/admin/merchant/gaps',
  inspections: '/admin/inspections',
  accounts: '/admin/access/accounts',
  requests: '/admin/access/requests',
  audit: '/admin/access/audit',
  shops: '/admin/merchant/shops',
  sources: '/admin/merchant/sources',
  ingestion: '/admin/merchant/ingestion',
  reviews: '/admin/merchant/reviews',
  answers: '/admin/merchant/answers',
}

const pagesByPath = new Map(Object.entries(paths).map(([page, path]) => [path, page as AdminPage]))

export function adminPageFromPath(path: string): AdminRoutePage {
  return pagesByPath.get(path.replace(/\/$/, '') || '/') ?? 'not-found'
}

export function adminPathForPage(page: AdminPage): string {
  return paths[page]
}
