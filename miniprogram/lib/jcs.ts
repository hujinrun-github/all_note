/**
 * RFC 8785 JSON Canonicalization Scheme (JCS), ported to match the Go server
 * byte-for-byte: backend/internal/mobilev2contract/jcs.go
 *
 * Two deliberate deviations from the RFC exist on the server side and MUST be
 * reproduced here:
 *
 *   1. Object keys sort by UTF-16 code unit  (jcs.go:211-224, lessUTF16)
 *   2. Semantic sets sort by Unicode CODE POINT (digest.go:144-157, lessCodePoint)
 *
 * Rule 1 is the RFC behaviour; rule 2 is a server-specific choice made in
 * `normalizeMobileV2SemanticSets`. They disagree only for characters outside
 * the BMP: U+E000 (one UTF-16 unit) sorts AFTER U+1F600 under UTF-16 but
 * BEFORE it under code point. See the golden vectors in
 * backend/testdata/mobile-v2/miniprogram-vectors.json.
 */

export type JCSValue = null | boolean | number | string | JCSValue[] | { [key: string]: JCSValue }

/** Compare by UTF-16 code unit, matching Go's lessUTF16. */
export function lessUTF16(left: string, right: string): boolean {
  const limit = Math.min(left.length, right.length)
  for (let index = 0; index < limit; index += 1) {
    const leftUnit = left.charCodeAt(index)
    const rightUnit = right.charCodeAt(index)
    if (leftUnit !== rightUnit) {
      return leftUnit < rightUnit
    }
  }
  return left.length < right.length
}

/** Compare by Unicode code point, matching Go's lessCodePoint. */
export function lessCodePoint(left: string, right: string): boolean {
  const leftRunes = Array.from(left)
  const rightRunes = Array.from(right)
  const limit = Math.min(leftRunes.length, rightRunes.length)
  for (let index = 0; index < limit; index += 1) {
    const leftCode = leftRunes[index].codePointAt(0) as number
    const rightCode = rightRunes[index].codePointAt(0) as number
    if (leftCode !== rightCode) {
      return leftCode < rightCode
    }
  }
  return leftRunes.length < rightRunes.length
}

/**
 * Serialize a number the way Go does (jcs.go:182-209):
 *   zero          -> "0"                (including -0)
 *   1e-6 <= |x| < 1e21 -> shortest fixed-point form
 *   otherwise     -> lowercase exponent form with an explicit sign, no padding
 */
export function formatJCSNumber(value: number): string {
  if (!Number.isFinite(value)) {
    throw new Error('number is not finite binary64')
  }
  if (value === 0) {
    return '0'
  }
  const absolute = Math.abs(value)
  if (absolute >= 1e-6 && absolute < 1e21) {
    return value.toString()
  }
  return value.toExponential()
}

/** Serialize a string the way Go does (jcs.go:154-180). Non-ASCII stays raw. */
export function writeJCSString(value: string): string {
  let output = '"'
  for (const character of value) {
    switch (character) {
      case '"':
        output += '\\"'
        break
      case '\\':
        output += '\\\\'
        break
      case '\b':
        output += '\\b'
        break
      case '\t':
        output += '\\t'
        break
      case '\n':
        output += '\\n'
        break
      case '\f':
        output += '\\f'
        break
      case '\r':
        output += '\\r'
        break
      default: {
        const code = character.codePointAt(0) as number
        if (code < 0x20) {
          output += '\\u' + code.toString(16).padStart(4, '0')
        } else {
          output += character
        }
      }
    }
  }
  return output + '"'
}

/** Canonicalize an already-parsed JSON value into JCS bytes (as a string). */
export function canonicalizeValue(value: JCSValue): string {
  if (value === null) {
    return 'null'
  }
  if (typeof value === 'boolean') {
    return value ? 'true' : 'false'
  }
  if (typeof value === 'number') {
    return formatJCSNumber(value)
  }
  if (typeof value === 'string') {
    return writeJCSString(value)
  }
  if (Array.isArray(value)) {
    // Array order is preserved; only semantic sets are re-sorted (see digest.ts).
    return '[' + value.map((item) => canonicalizeValue(item)).join(',') + ']'
  }
  const record = value as { [key: string]: JCSValue }
  const keys = Object.keys(record).sort((left, right) => (lessUTF16(left, right) ? -1 : lessUTF16(right, left) ? 1 : 0))
  return (
    '{' +
    keys
      .map((key) => writeJCSString(key) + ':' + canonicalizeValue(record[key]))
      .join(',') +
    '}'
  )
}

/** Canonicalize raw JSON text into JCS bytes (as a string). */
export function canonicalizeJSON(text: string): string {
  return canonicalizeValue(JSON.parse(text) as JCSValue)
}
