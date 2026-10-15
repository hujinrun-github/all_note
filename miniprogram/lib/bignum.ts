/**
 * Decimal-string arithmetic for protocol counters.
 *
 * Every revision, sequence, epoch, duration and byte size on the mobile-v2 wire
 * is a decimal STRING, not a JSON number — the server decodes with
 * `decoder.UseNumber()` and validates with `strconv.ParseUint(..., 10, 64)`
 * (see backend/internal/mobilev2command/envelope.go:265-268).
 *
 * JS numbers lose precision past 2^53, so these values must never be converted
 * to `number`. Compare them as strings instead.
 */

/** Compare two non-negative decimal strings. Returns -1, 0 or 1. */
export function compareDecimalString(left: string, right: string): number {
  const a = stripSign(left)
  const b = stripSign(right)
  const leftNegative = left.trim().startsWith('-')
  const rightNegative = right.trim().startsWith('-')
  if (leftNegative !== rightNegative) {
    return leftNegative ? -1 : 1
  }
  const magnitude = compareMagnitude(a, b)
  return leftNegative ? -magnitude : magnitude
}

function compareMagnitude(a: string, b: string): number {
  const left = a.replace(/^0+/, '') || '0'
  const right = b.replace(/^0+/, '') || '0'
  if (left.length !== right.length) {
    return left.length < right.length ? -1 : 1
  }
  if (left === right) {
    return 0
  }
  return left < right ? -1 : 1
}

function stripSign(value: string): string {
  const trimmed = value.trim()
  return trimmed.startsWith('-') ? trimmed.slice(1) : trimmed
}

export function isDecimalUint(value: string): boolean {
  return /^(0|[1-9][0-9]*)$/.test(value.trim())
}

/** True when `revision` is at least `minimum` (both decimal strings). */
export function atLeast(revision: string, minimum: string): boolean {
  return compareDecimalString(revision, minimum) >= 0
}
