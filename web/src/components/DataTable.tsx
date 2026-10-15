import { ReactNode, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useStoredState } from '../lib/hooks'
import { Icon } from './icons'

export interface Column<T> {
  id: string
  label: string
  render: (row: T) => ReactNode
  sort?: (row: T) => string | number
  search?: (row: T) => string
  num?: boolean
  hidden?: boolean // hidden by default
}

interface View {
  sort: string
  dir: 1 | -1
  hidden: string[]
}

const RENDER_LIMIT = 500

// DataTable: sortable, filterable, column selection; the view (sort, hidden
// columns) is remembered per table.
export function DataTable<T>({
  id, columns, rows, rowKey, onRowClick, empty, tools, rowMenu, selected,
}: {
  id: string
  columns: Column<T>[]
  rows: T[] | undefined
  rowKey: (r: T) => string
  onRowClick?: (r: T) => void
  empty?: string
  tools?: ReactNode
  rowMenu?: (r: T) => ReactNode
  selected?: string
}) {
  const { t } = useTranslation()
  const [view, setView] = useStoredState<View>(`hm.table.${id}`, {
    sort: columns.find((c) => c.sort)?.id ?? '',
    dir: 1,
    hidden: columns.filter((c) => c.hidden).map((c) => c.id),
  })
  const [filter, setFilter] = useState('')
  const [showAll, setShowAll] = useState(false)
  const [colMenu, setColMenu] = useState(false)
  const filterRef = useRef<HTMLInputElement>(null)
  const colRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    // "/" focuses the table filter unless the user is typing somewhere.
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement).tagName
      if (e.key === '/' && tag !== 'INPUT' && tag !== 'TEXTAREA' && tag !== 'SELECT') {
        e.preventDefault()
        filterRef.current?.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    if (!colMenu) return
    const close = (e: MouseEvent) => !colRef.current?.contains(e.target as Node) && setColMenu(false)
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [colMenu])

  const visible = columns.filter((c) => !view.hidden.includes(c.id))
  const sorted = useMemo(() => {
    if (!rows) return []
    const q = filter.trim().toLowerCase()
    let out = rows
    if (q) {
      out = rows.filter((r) =>
        columns.some((c) => {
          const v = c.search ? c.search(r) : c.sort ? String(c.sort(r)) : ''
          return v.toLowerCase().includes(q)
        }),
      )
    }
    const col = columns.find((c) => c.id === view.sort)
    if (col?.sort) {
      const s = col.sort
      out = [...out].sort((a, b) => {
        const x = s(a)
        const y = s(b)
        if (typeof x === 'number' && typeof y === 'number') return (x - y) * view.dir
        return String(x).localeCompare(String(y), undefined, { numeric: true }) * view.dir
      })
    }
    return out
  }, [rows, filter, view.sort, view.dir, columns])

  const shown = showAll ? sorted : sorted.slice(0, RENDER_LIMIT)

  const toggleSort = (c: Column<T>) => {
    if (!c.sort) return
    setView({ ...view, sort: c.id, dir: view.sort === c.id ? ((view.dir * -1) as 1 | -1) : 1 })
  }
  const toggleCol = (id: string) => {
    const hidden = view.hidden.includes(id) ? view.hidden.filter((h) => h !== id) : [...view.hidden, id]
    if (columns.length - hidden.length < 1) return
    setView({ ...view, hidden })
  }

  return (
    <div>
      <div className="table-tools">
        <input
          ref={filterRef}
          className="input"
          placeholder={t('table.filter')}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          aria-label={t('table.filter')}
        />
        <span className="faint nowrap">{rows ? t('table.count', { shown: sorted.length, total: rows.length }) : ''}</span>
        <div className="grow" />
        {tools}
        <div className="menu-wrap" ref={colRef}>
          <button className="btn sm" onClick={() => setColMenu(!colMenu)} aria-expanded={colMenu}>
            {t('table.columns')}
          </button>
          {colMenu && (
            <div className="menu" style={{ padding: '6px 10px' }}>
              {columns.map((c) => (
                <label key={c.id} className="check" style={{ display: 'flex', height: 26 }}>
                  <input type="checkbox" checked={!view.hidden.includes(c.id)} onChange={() => toggleCol(c.id)} />
                  {c.label}
                </label>
              ))}
            </div>
          )}
        </div>
      </div>
      <div className="table-wrap">
        <table className="data">
          <thead>
            <tr>
              {visible.map((c) => (
                <th
                  key={c.id}
                  className={`${c.sort ? 'sortable' : ''} ${c.num ? 'num' : ''}`}
                  onClick={() => toggleSort(c)}
                  aria-sort={view.sort === c.id ? (view.dir === 1 ? 'ascending' : 'descending') : undefined}
                >
                  {c.label}
                  {view.sort === c.id && <span className="arrow">{view.dir === 1 ? '▲' : '▼'}</span>}
                </th>
              ))}
              {rowMenu && <th style={{ width: 44 }} />}
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => {
              const key = rowKey(r)
              return (
                <tr
                  key={key}
                  className={`${onRowClick ? 'clickable' : ''} ${selected === key ? 'selected' : ''}`}
                  onClick={() => onRowClick?.(r)}
                  onKeyDown={(e) => e.key === 'Enter' && onRowClick?.(r)}
                  tabIndex={onRowClick ? 0 : undefined}
                >
                  {visible.map((c) => (
                    <td key={c.id} className={c.num ? 'num' : ''}>{c.render(r)}</td>
                  ))}
                  {rowMenu && <td onClick={(e) => e.stopPropagation()}>{rowMenu(r)}</td>}
                </tr>
              )
            })}
          </tbody>
        </table>
        {rows && sorted.length === 0 && <div className="empty">{filter ? t('table.no_match') : empty ?? t('table.empty')}</div>}
        {!rows && <div className="empty">{t('common.loading')}</div>}
        {sorted.length > RENDER_LIMIT && !showAll && (
          <div className="empty">
            <button className="btn sm" onClick={() => setShowAll(true)}>
              <Icon name="chevronDown" size={12} /> {t('table.show_all', { n: sorted.length })}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
