import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import de from './de.json'
import en from './en.json'

type Tree = { [k: string]: string | Tree }

function flatten(t: Tree, prefix = ''): string[] {
  return Object.entries(t).flatMap(([k, v]) => (typeof v === 'string' ? [prefix + k] : flatten(v, prefix + k + '.')))
}

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f)
    if (statSync(p).isDirectory()) return sources(p)
    return /\.tsx?$/.test(f) && !f.endsWith('.test.ts') ? [p] : []
  })
}

const deKeys = new Set(flatten(de as Tree))
const enKeys = new Set(flatten(en as Tree))

// Families of keys built at runtime from API values.
const dynamic: Record<string, string[]> = {
  'console.': ['connecting', 'connected', 'disconnected', 'error'],
  'vm.action.': ['start', 'shutdown', 'stop', 'reboot', 'reset', 'pause', 'resume'],
  'vm.confirm_': ['stop', 'reset'],
  'ct.action.': ['start', 'shutdown', 'stop', 'reboot', 'freeze', 'unfreeze'],
  'job.status.': ['queued', 'running', 'succeeded', 'failed', 'cancelled'],
  'job.type.': ['vm_create', 'vm_delete', 'ct_create', 'ct_delete'],
  'roles.': ['admin', 'vm_admin', 'operator', 'storage_admin', 'network_admin', 'auditor'],
  'state.': ['running', 'blocked', 'paused', 'shutting_down', 'shutoff', 'crashed', 'suspended', 'unknown', 'stopped', 'frozen', 'active', 'inactive', 'ok', 'error', 'up', 'down'],
  'storage.state.': ['inactive', 'building', 'running', 'degraded', 'inaccessible'],
  'storage.content_': ['images', 'iso'],
  'net.kinds.': ['physical', 'bridge', 'bond', 'vlan', 'virtual', 'loopback'],
  'vm.cpu_mode_hint.': ['host_passthrough', 'host_model', 'qemu64'],
  // API error codes produced by the control plane.
  'errors.': ['invalid_credentials', 'rate_limited', 'unauthenticated', 'csrf', 'forbidden', 'network', 'agent_unavailable', 'not_found', 'conflict', 'validation', 'internal', 'locked', 'last_admin', 'self_delete', 'too_large', 'length_required'],
}

describe('translations', () => {
  it('de and en have the same keys', () => {
    expect([...deKeys].filter((k) => !enKeys.has(k))).toEqual([])
    expect([...enKeys].filter((k) => !deKeys.has(k))).toEqual([])
  })

  it('every static key used in the source exists', () => {
    const missing: string[] = []
    for (const f of sources(join(__dirname, '..'))) {
      const src = readFileSync(f, 'utf8')
      for (const m of src.matchAll(/\bt\(\s*'([a-z_][a-zA-Z0-9_.]*)'/g)) {
        if (!deKeys.has(m[1])) missing.push(`${f}: ${m[1]}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('dynamic key families are complete', () => {
    const missing: string[] = []
    for (const [prefix, names] of Object.entries(dynamic)) {
      for (const n of names) if (!deKeys.has(prefix + n)) missing.push(prefix + n)
    }
    expect(missing).toEqual([])
  })

  it('interpolation placeholders match between languages', () => {
    const vars = (s: string) => [...s.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]).sort().join(',')
    const get = (t: Tree, k: string) => k.split('.').reduce<Tree | string>((o, p) => (o as Tree)[p], t) as string
    const diff = [...deKeys].filter((k) => vars(get(de as Tree, k)) !== vars(get(en as Tree, k)))
    expect(diff).toEqual([])
  })
})
