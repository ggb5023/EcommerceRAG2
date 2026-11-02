import { describe, expect, it } from 'vitest'
import { h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import Badge from './Badge.vue'
import Button from './Button.vue'
import Card from './Card.vue'
import DataTable from './DataTable.vue'
import Drawer from './Drawer.vue'
import Empty from './Empty.vue'
import Input from './Input.vue'
import Table from './Table.vue'

describe('design-system components', () => {
  it('Button renders its label, variant colour class, disabled state and data-testid', async () => {
    const html = await renderToString(h(Button, {
      variant: 'primary', disabled: true, 'data-testid': 'save-button',
    }, () => '保存'))
    expect(html).toContain('保存')
    expect(html).toContain('ds-button-primary')
    expect(html).toContain('disabled')
    expect(html).toContain('data-testid="save-button"')
  })

  it('Input forwards model value and data-testid', async () => {
    const html = await renderToString(h(Input, {
      modelValue: 'hello', type: 'text', 'data-testid': 'name-input',
    }))
    expect(html).toContain('value="hello"')
    expect(html).toContain('data-testid="name-input"')
  })

  it('Card renders its slot and caller class', async () => {
    const html = await renderToString(h(Card, { class: 'health-cell', 'data-testid': 'metric-card' }, () => '42'))
    expect(html).toContain('ds-card')
    expect(html).toContain('health-cell')
    expect(html).toContain('42')
    expect(html).toContain('data-testid="metric-card"')
  })

  it('Badge maps tone to its colour class', async () => {
    const html = await renderToString(h(Badge, { tone: 'warning', 'data-testid': 'status-badge' }, () => '待处理'))
    expect(html).toContain('ds-badge-warning')
    expect(html).toContain('待处理')
    expect(html).toContain('data-testid="status-badge"')
  })

  it('Table keeps the caller rows and data-testid', async () => {
    const html = await renderToString(h(Table, { 'data-testid': 'rows-table' }, () =>
      h('tbody', [h('tr', [h('td', 'row')])])))
    expect(html).toContain('table-wrap')
    expect(html).toContain('row')
    expect(html).toContain('data-testid="rows-table"')
  })

  it('Empty renders the caller title and body', async () => {
    const html = await renderToString(h(Empty, { 'data-testid': 'empty-state' }, {
      title: () => '暂无内容',
      default: () => '请稍后再试。',
    }))
    expect(html).toContain('暂无内容')
    expect(html).toContain('请稍后再试。')
    expect(html).toContain('data-testid="empty-state"')
  })

  it('DataTable renders the title, search value and actions', async () => {
    const html = await renderToString(h(DataTable, { title: '账号', modelValue: 'dev' }, {
      subtitle: () => '搜索账号',
      actions: () => '新增',
      default: () => 'rows',
    }))
    expect(html).toContain('账号')
    expect(html).toContain('搜索账号')
    expect(html).toContain('value="dev"')
    expect(html).toContain('新增')
    expect(html).toContain('data-testid="data-table"')
  })

  it('Drawer renders only while open and exposes its dialog title', async () => {
    const open = await renderToString(h(Drawer, { open: true, title: '编辑店铺' }, () => 'form'))
    expect(open).toContain('data-testid="drawer"')
    expect(open).toContain('aria-label="编辑店铺"')
    expect(open).toContain('form')
    const closed = await renderToString(h(Drawer, { open: false, title: '编辑店铺' }, () => 'form'))
    expect(closed).not.toContain('data-testid="drawer"')
  })
})
