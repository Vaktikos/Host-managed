// Minimal stroke icon set (16px). Kept local to avoid an icon dependency.
const paths: Record<string, string> = {
  datacenter: 'M2 13h12M3 13V6l5-3 5 3v7M6 13V9h4v4',
  server: 'M2.5 3h11v4h-11zM2.5 9h11v4h-11zM5 5h.01M5 11h.01',
  vm: 'M2 3h12v8H2zM5.5 14h5M8 11v3',
  container: 'M2 5l6-3 6 3v6l-6 3-6-3zM2 5l6 3 6-3M8 8v6',
  storage: 'M3 4c0-1.1 2.2-2 5-2s5 .9 5 2-2.2 2-5 2-5-.9-5-2zM3 4v8c0 1.1 2.2 2 5 2s5-.9 5-2V4M3 8c0 1.1 2.2 2 5 2s5-.9 5-2',
  network: 'M8 2v4M8 10v4M4 6h8v4H4zM2 14h4M10 14h4',
  tasks: 'M5 4h9M5 8h9M5 12h9M2 4h.01M2 8h.01M2 12h.01',
  audit: 'M8 2l5 2v4c0 3-2.2 5.2-5 6-2.8-.8-5-3-5-6V4zM6 8l1.5 1.5L10.5 6',
  users: 'M6 7a2.5 2.5 0 100-5 2.5 2.5 0 000 5zM1.5 14c0-2.5 2-4.5 4.5-4.5s4.5 2 4.5 4.5M11 2.2a2.5 2.5 0 010 4.6M12.5 9.7c1.2.6 2 1.9 2 3.3',
  account: 'M8 8a3 3 0 100-6 3 3 0 000 6zM2.5 14.5c0-3 2.5-5 5.5-5s5.5 2 5.5 5',
  play: 'M5 3l8 5-8 5z',
  stop: 'M4 4h8v8H4z',
  power: 'M8 2v6M4.5 4.5a5 5 0 107 0',
  pause: 'M5 3h2v10H5zM9 3h2v10H9z',
  refresh: 'M13.5 8A5.5 5.5 0 113 5.5M3 2v3.5h3.5',
  terminal: 'M2 3h12v10H2zM4.5 6l2 2-2 2M8 10h3.5',
  plus: 'M8 3v10M3 8h10',
  trash: 'M3 4h10M6 4V2.5h4V4M4.5 4l.5 9.5h6l.5-9.5',
  search: 'M7 12A5 5 0 107 2a5 5 0 000 10zM10.5 10.5L14 14',
  chevronLeft: 'M10 3L5 8l5 5',
  chevronRight: 'M6 3l5 5-5 5',
  chevronDown: 'M3 6l5 5 5-5',
  menu: 'M2 4h12M2 8h12M2 12h12',
  logout: 'M6 2H3v12h3M10.5 5l3 3-3 3M13.5 8H6',
  more: 'M3.5 8h.01M8 8h.01M12.5 8h.01',
  upload: 'M8 11V2.5M4.5 6L8 2.5 11.5 6M2.5 11v2.5h11V11',
  close: 'M4 4l8 8M12 4l-8 8',
  copy: 'M5 5h8v8H5zM3 11V3h8',
  overview: 'M2 2h5v5H2zM9 2h5v5H9zM2 9h5v5H2zM9 9h5v5H9z',
}

export function Icon({ name, size = 16, className }: { name: keyof typeof paths | string; size?: number; className?: string }) {
  const d = paths[name] ?? paths.more
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      <path d={d} />
    </svg>
  )
}

export function Logo({ size = 20 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="4" fill="#141a22" stroke="#2a3340" />
      <path d="M8 8h4v6.5h8V8h4v16h-4v-6h-8v6H8z" fill="#7c98b8" />
    </svg>
  )
}
