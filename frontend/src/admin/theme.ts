export type ThemeChoice = 'system' | 'light' | 'dark'
const STORAGE_KEY = 'ecr-theme'

export function loadThemeChoice(): ThemeChoice {
  if (typeof localStorage === 'undefined') return 'system'
  const value = localStorage.getItem(STORAGE_KEY)
  return value === 'light' || value === 'dark' ? value : 'system'
}

export function applyThemeChoice(choice: ThemeChoice): void {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  const dark = choice === 'dark' || (choice === 'system' && window.matchMedia?.('(prefers-color-scheme: dark)').matches)
  root.classList.toggle('dark', dark)
  root.classList.toggle('light', choice === 'light')
  if (typeof localStorage !== 'undefined') {
    if (choice === 'system') localStorage.removeItem(STORAGE_KEY)
    else localStorage.setItem(STORAGE_KEY, choice)
  }
}

export function nextThemeChoice(choice: ThemeChoice): ThemeChoice {
  return choice === 'system' ? 'dark' : choice === 'dark' ? 'light' : 'system'
}

export function themeLabel(choice: ThemeChoice): string {
  return choice === 'system' ? '跟随系统' : choice === 'dark' ? '暗色' : '亮色'
}
