// Shared transport-level retry for the controlled browser specs.
//
// Docker network changes surface in Chromium as navigation or fetch errors
// (`ERR_NETWORK_CHANGED`, `net::ERR_*`, `Failed to fetch`, ...). Retry only
// those transport errors: HTTP statuses are returned values, not exceptions
// (including 409 idempotency conflicts), and assertion failures must never be
// retried. Callers generate POST Idempotency-Key values before entering retry
// so every attempt replays the same key.
const TRANSIENT = /ERR_NETWORK_CHANGED|net::ERR_[A-Z_]+|Failed to fetch|ECONNRESET|ETIMEDOUT/i

export function isTransportError(error) {
  if (!error || error.name === 'AssertionError') return false
  return TRANSIENT.test(String(error?.message ?? error))
}

export async function retry(fn, { attempts = 3, delay = 500 } = {}) {
  let last
  for (let attempt = 0; attempt < attempts; attempt++) {
    try {
      return await fn()
    } catch (error) {
      last = error
      if (attempt === attempts - 1 || !isTransportError(error)) throw error
      // 500ms, then 1000ms: bounded, and long enough for the network to settle.
      await new Promise(resolve => setTimeout(resolve, delay * (attempt + 1)))
    }
  }
  throw last
}
