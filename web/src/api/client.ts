// Thin fetch wrapper: JSON, CSRF header for unsafe methods, typed errors.

export class ApiError extends Error {
  status: number
  code: string
  field?: string
  requestId?: string
  constructor(status: number, code: string, message: string, field?: string, requestId?: string) {
    super(message)
    this.status = status
    this.code = code
    this.field = field
    this.requestId = requestId
  }
}

let csrfToken = ''
let onUnauthorized: (() => void) | null = null

export function setCSRF(token: string) {
  csrfToken = token
}

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

async function parseError(res: Response): Promise<ApiError> {
  let code = 'http_' + res.status
  let message = res.statusText
  let field: string | undefined
  let requestId: string | undefined
  try {
    const body = await res.json()
    if (body?.error) {
      code = body.error.code ?? code
      message = body.error.message ?? message
      field = body.error.field
      requestId = body.error.request_id
    }
  } catch {
    // non-JSON error body
  }
  return new ApiError(res.status, code, message, field, requestId)
}

export async function request<T>(method: string, path: string, body?: unknown, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined && !(body instanceof Blob)) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) headers['X-CSRF-Token'] = csrfToken
  let res: Response
  try {
    res = await fetch('/api/v1' + path, {
      method,
      headers,
      credentials: 'same-origin',
      body: body === undefined ? undefined : body instanceof Blob ? body : JSON.stringify(body),
      ...init,
    })
  } catch {
    throw new ApiError(0, 'network', 'network error')
  }
  if (res.status === 401 && !path.startsWith('/auth/login')) {
    onUnauthorized?.()
  }
  if (!res.ok) throw await parseError(res)
  if (res.status === 204) return undefined as T
  const ct = res.headers.get('Content-Type') ?? ''
  if (!ct.includes('application/json')) return undefined as T
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {}),
  del: <T>(path: string) => request<T>('DELETE', path),
}

// upload streams a file with progress via XHR (fetch has no upload progress).
export function upload(path: string, file: File, onProgress: (loaded: number, total: number) => void): { promise: Promise<void>; abort: () => void } {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<void>((resolve, reject) => {
    xhr.open('PUT', '/api/v1' + path)
    xhr.setRequestHeader('X-CSRF-Token', csrfToken)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.upload.onprogress = (e) => onProgress(e.loaded, e.total)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) return resolve()
      let code = 'http_' + xhr.status
      let message = xhr.statusText
      try {
        const b = JSON.parse(xhr.responseText)
        code = b.error?.code ?? code
        message = b.error?.message ?? message
      } catch {
        // ignore
      }
      reject(new ApiError(xhr.status, code, message))
    }
    xhr.onerror = () => reject(new ApiError(0, 'network', 'network error'))
    xhr.onabort = () => reject(new ApiError(0, 'aborted', 'aborted'))
    xhr.send(file)
  })
  return { promise, abort: () => xhr.abort() }
}

export function wsURL(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/api/v1${path}`
}
