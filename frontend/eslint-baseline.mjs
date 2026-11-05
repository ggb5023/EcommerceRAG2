import { readFileSync } from 'node:fs'
import path from 'node:path'

import { ESLint } from 'eslint'

const baselinePath = new URL('./eslint-baseline.txt', import.meta.url)
const messagePattern = /^(.*?):\d+:\d+ (?:warning|error) (\S+) (.*)$/

function parseBaseline() {
  const counts = new Map()
  for (const line of readFileSync(baselinePath, 'utf8').split('\n')) {
    if (!line || line.startsWith('#')) continue
    const match = line.match(messagePattern)
    if (!match) continue
    const key = `${match[1]}|${match[2]}|${match[3]}`
    counts.set(key, (counts.get(key) || 0) + 1)
  }
  return counts
}

function messageKey(file, rule, message) {
  return `${file}|${rule || 'fatal'}|${message}`
}

const eslint = new ESLint({ cwd: process.cwd() })
const results = await eslint.lintFiles(['src'])
const messages = []
for (const result of results) {
  const file = path.relative(process.cwd(), result.filePath)
  for (const item of result.messages) {
    messages.push({
      file,
      line: item.line,
      column: item.column,
      severity: item.severity,
      rule: item.ruleId,
      message: item.message,
    })
  }
}

for (const item of messages) {
  const level = item.severity === 2 ? 'error' : 'warning'
  console.log(`${item.file}:${item.line}:${item.column} ${level} ${item.rule || 'fatal'} ${item.message}`)
}

const baseline = parseBaseline()
const current = new Map()
for (const item of messages) {
  const key = messageKey(item.file, item.rule, item.message)
  current.set(key, (current.get(key) || 0) + 1)
}

const added = []
for (const [key, count] of current) {
  const allowed = baseline.get(key) || 0
  if (count > allowed) added.push({ key, count, allowed })
}
const errors = messages.filter((item) => item.severity === 2)
const removed = []
for (const [key, count] of baseline) {
  const currentCount = current.get(key) || 0
  if (currentCount < count) removed.push({ key, count, currentCount })
}

if (errors.length || added.length) {
  for (const item of added) {
    console.log(`FAIL new lint warning: ${item.key} (${item.count} > baseline ${item.allowed})`)
  }
  for (const item of errors) {
    console.log(`FAIL lint error: ${item.file}:${item.line}:${item.column} ${item.rule || 'fatal'} ${item.message}`)
  }
  console.log(`FAIL eslint: ${errors.length} error(s), ${added.length} new warning group(s)`)
  process.exit(1)
}

if (removed.length) {
  console.log(`NOTE ${removed.length} baseline warning group(s) disappeared; lower eslint-baseline.txt in the next cleanup.`)
}
console.log(`PASS eslint: ${messages.length} warning(s), 0 errors, baseline respected`)
