// Shared helpers for the typed service bridges (one file per service area).
// Inside the Wails window a call goes to the Go service main.<Service>.<Method>.
// In a plain browser (bun run proto) `inWails` is false and each bridge file
// answers from its own mock, so the prototype keeps working.
import { Call, Events } from '@wailsio/runtime'

// WebView2 exposes window.chrome.webview; desktop Chrome does not.
export const inWails = typeof (window as unknown as { chrome?: { webview?: unknown } }).chrome?.webview !== 'undefined'

// ServiceError is what Go services return as an error (docs/api-contract.md,
// "Error shape"). `code` is stable ("notFound", "proxy.down"), `details` fills the
// translated message. Never show `message` to the user; translate `code`.
export class ServiceError extends Error {
  code: string
  details: Record<string, unknown>
  retryable: boolean
  constructor(code: string, details: Record<string, unknown> = {}, message = code, retryable = false) {
    super(message)
    this.code = code
    this.details = details
    this.retryable = retryable
  }
}

export async function callService<T>(service: string, method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(`main.${service}.${method}`, ...args)) as T
  } catch (e) {
    throw toServiceError(e)
  }
}

// Go errors reach the UI as text. A service error is JSON {"code":..,"details":..}.
export function toServiceError(e: unknown): ServiceError {
  const text = e instanceof Error ? e.message : String(e)
  try {
    const j = JSON.parse(text.slice(text.indexOf('{'))) as { code?: string; details?: Record<string, unknown>; message?: string; retryable?: boolean }
    if (j.code) return new ServiceError(j.code, j.details ?? {}, j.message ?? j.code, j.retryable ?? false)
  } catch {
    /* not JSON */
  }
  return new ServiceError('internal', {}, text)
}

export function onEvent<T>(name: string, cb: (data: T) => void): () => void {
  if (!inWails) return () => {}
  return Events.On(name, (e: { data: T }) => cb(e.data))
}
