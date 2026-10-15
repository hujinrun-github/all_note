/**
 * SHA-256 (FIPS 180-4) in pure TypeScript.
 *
 * The Mini Program runtime has no Web Crypto (`crypto.subtle`) and no Node
 * `crypto` module, so the digest required by the mobile-v2 contract has to be
 * computed here. Output format matches the Go server: lowercase hex.
 *
 * Reference for the mobile-v2 contract:
 *   backend/internal/mobilev2contract/digest.go:59-66
 *     digest := sha256.Sum256(canonical.Bytes())
 *     return "sha256:" + hex.EncodeToString(digest[:]), nil
 */

const ROUND_CONSTANTS = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

const INITIAL_STATE = new Uint32Array([
  0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
])

function rotr(value: number, bits: number): number {
  return ((value >>> bits) | (value << (32 - bits))) >>> 0
}

/**
 * UTF-8 encode a JS string. Lone surrogates are replaced with U+FFFD so the
 * byte stream matches Go, which decodes invalid UTF-8 to U+FFFD
 * (backend/internal/mobilev2contract/jcs.go:156).
 */
export function utf8Encode(text: string): Uint8Array {
  const bytes: number[] = []
  for (let index = 0; index < text.length; index += 1) {
    const unit = text.charCodeAt(index)
    if (unit >= 0xd800 && unit <= 0xdbff && index + 1 < text.length) {
      const low = text.charCodeAt(index + 1)
      if (low >= 0xdc00 && low <= 0xdfff) {
        const codePoint = (unit - 0xd800) * 0x400 + (low - 0xdc00) + 0x10000
        bytes.push(
          0xf0 | (codePoint >>> 18),
          0x80 | ((codePoint >>> 12) & 0x3f),
          0x80 | ((codePoint >>> 6) & 0x3f),
          0x80 | (codePoint & 0x3f),
        )
        index += 1
        continue
      }
    }
    if (unit >= 0xd800 && unit <= 0xdfff) {
      // Lone surrogate: Go would have produced U+FFFD.
      bytes.push(0xef, 0xbf, 0xbd)
      continue
    }
    if (unit < 0x80) {
      bytes.push(unit)
    } else if (unit < 0x800) {
      bytes.push(0xc0 | (unit >>> 6), 0x80 | (unit & 0x3f))
    } else {
      bytes.push(0xe0 | (unit >>> 12), 0x80 | ((unit >>> 6) & 0x3f), 0x80 | (unit & 0x3f))
    }
  }
  return new Uint8Array(bytes)
}

/** SHA-256 over raw bytes, returning the 32-byte digest. */
export function sha256Bytes(data: Uint8Array): Uint8Array {
  const padded = pad(data)
  const state = INITIAL_STATE.slice()
  const schedule = new Uint32Array(64)

  for (let offset = 0; offset < padded.length; offset += 64) {
    for (let index = 0; index < 16; index += 1) {
      const base = offset + index * 4
      schedule[index] =
        ((padded[base] << 24) | (padded[base + 1] << 16) | (padded[base + 2] << 8) | padded[base + 3]) >>> 0
    }
    for (let index = 16; index < 64; index += 1) {
      const s0 = (rotr(schedule[index - 15], 7) ^ rotr(schedule[index - 15], 18) ^ (schedule[index - 15] >>> 3)) >>> 0
      const s1 = (rotr(schedule[index - 2], 17) ^ rotr(schedule[index - 2], 19) ^ (schedule[index - 2] >>> 10)) >>> 0
      schedule[index] = (schedule[index - 16] + s0 + schedule[index - 7] + s1) >>> 0
    }

    let [a, b, c, d, e, f, g, h] = state
    for (let index = 0; index < 64; index += 1) {
      const S1 = (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) >>> 0
      const ch = ((e & f) ^ (~e & g)) >>> 0
      const temp1 = (h + S1 + ch + ROUND_CONSTANTS[index] + schedule[index]) >>> 0
      const S0 = (rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) >>> 0
      const maj = ((a & b) ^ (a & c) ^ (b & c)) >>> 0
      const temp2 = (S0 + maj) >>> 0
      h = g
      g = f
      f = e
      e = (d + temp1) >>> 0
      d = c
      c = b
      b = a
      a = (temp1 + temp2) >>> 0
    }
    state[0] = (state[0] + a) >>> 0
    state[1] = (state[1] + b) >>> 0
    state[2] = (state[2] + c) >>> 0
    state[3] = (state[3] + d) >>> 0
    state[4] = (state[4] + e) >>> 0
    state[5] = (state[5] + f) >>> 0
    state[6] = (state[6] + g) >>> 0
    state[7] = (state[7] + h) >>> 0
  }

  const digest = new Uint8Array(32)
  for (let index = 0; index < 8; index += 1) {
    digest[index * 4] = (state[index] >>> 24) & 0xff
    digest[index * 4 + 1] = (state[index] >>> 16) & 0xff
    digest[index * 4 + 2] = (state[index] >>> 8) & 0xff
    digest[index * 4 + 3] = state[index] & 0xff
  }
  return digest
}

function pad(data: Uint8Array): Uint8Array {
  const bitLength = data.length * 8
  const withOne = data.length + 1
  const total = withOne + ((64 - ((withOne + 8) % 64)) % 64) + 8
  const padded = new Uint8Array(total)
  padded.set(data)
  padded[data.length] = 0x80
  // 64-bit big-endian length; JS numbers are safe up to 2^53 bytes here.
  const high = Math.floor(bitLength / 0x100000000)
  const low = bitLength >>> 0
  padded[total - 8] = (high >>> 24) & 0xff
  padded[total - 7] = (high >>> 16) & 0xff
  padded[total - 6] = (high >>> 8) & 0xff
  padded[total - 5] = high & 0xff
  padded[total - 4] = (low >>> 24) & 0xff
  padded[total - 3] = (low >>> 16) & 0xff
  padded[total - 2] = (low >>> 8) & 0xff
  padded[total - 1] = low & 0xff
  return padded
}

/** SHA-256 hex digest. Accepts a string (UTF-8 encoded) or raw bytes. */
export function sha256Hex(input: string | Uint8Array): string {
  const bytes = typeof input === 'string' ? utf8Encode(input) : input
  const digest = sha256Bytes(bytes)
  let hex = ''
  for (let index = 0; index < digest.length; index += 1) {
    hex += digest[index].toString(16).padStart(2, '0')
  }
  return hex
}
