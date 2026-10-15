/**
 * Lowercase UUID v4 generation.
 *
 * The server validates command ids with `validUUID`
 * (backend/internal/mobilev2command/envelope.go) which requires the canonical
 * 8-4-4-4-12 lowercase form with version 4 and variant 8/9/a/b.
 *
 * The Mini Program runtime exposes neither `crypto.randomUUID` nor
 * `crypto.getRandomValues`, so the default entropy source is Math.random. That
 * is sufficient here: ids only need uniqueness (the server does not verify
 * cryptographic quality). Pass `random` in tests for determinism.
 */

const HEX = '0123456789abcdef'

export function uuidV4(random: () => number = Math.random): string {
  const bytes = new Uint8Array(16)
  for (let index = 0; index < 16; index += 1) {
    bytes[index] = Math.floor(random() * 256) & 0xff
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80

  let text = ''
  for (let index = 0; index < 16; index += 1) {
    text += HEX[bytes[index] >>> 4] + HEX[bytes[index] & 0x0f]
    if (index === 3 || index === 5 || index === 7 || index === 9) {
      text += '-'
    }
  }
  return text
}

/** Validate the canonical lowercase v4 form the server expects. */
export function isUUID(value: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)
}
