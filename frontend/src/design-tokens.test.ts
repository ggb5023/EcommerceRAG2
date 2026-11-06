import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// Colours belong to tokens.css only. These are the surfaces migrated in WP72;
// later workpackages extend this list as the workbench/admin styles move over.
const MIGRATED = [
  'channel.css',
  'SuggestionPanel.vue',
  'components/PageHeader.vue',
  'components/StatePanel.vue',
  'components/ConfirmDialog.vue',
]

const root = fileURLToPath(new URL('.', import.meta.url))

describe('design tokens', () => {
  it('keeps raw hex colours out of migrated surfaces', () => {
    const offenders = MIGRATED.flatMap((file) => {
      const text = readFileSync(root + file, 'utf8')
      const matches = text.match(/#[0-9a-fA-F]{3,8}\b/g) ?? []
      return matches.map((value) => `${file}: ${value}`)
    })
    expect(offenders).toEqual([])
  })
})
