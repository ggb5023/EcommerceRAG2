import { inject, type InjectionKey } from 'vue'

export const AdminContextKey: InjectionKey<Record<string, any>> = Symbol('admin-context')

export function useAdminContext(): Record<string, any> {
  const context = inject(AdminContextKey)
  if (!context) throw new Error('admin context is missing')
  return context
}
