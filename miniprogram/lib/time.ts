/**
 * Time formatting for the mobile-v2 wire contract.
 *
 * The protocol uses two shapes (backend/api/mobile-v2.openapi.yaml):
 *   instant: "2006-01-02T15:04:05.000Z"   (UTC, always milliseconds)
 *   date:    "YYYY-MM-DD"                 (projection-local)
 *   time:    "HH:MM:SS"                   (projection-local)
 *
 * The server also requires an IANA `projection_time_zone` on sliding scopes.
 * The Mini Program has no IANA tz database, so V1 carries a fixed offset
 * (Asia/Shanghai has no DST, +08:00 year-round).
 */

function pad(value: number, width: number): string {
  return String(Math.abs(value)).padStart(width, '0')
}

/** "2006-01-02T15:04:05.000Z" — always UTC, always three fractional digits. */
export function formatInstant(input: Date | number): string {
  const date = input instanceof Date ? input : new Date(input)
  return (
    date.getUTCFullYear() +
    '-' +
    pad(date.getUTCMonth() + 1, 2) +
    '-' +
    pad(date.getUTCDate(), 2) +
    'T' +
    pad(date.getUTCHours(), 2) +
    ':' +
    pad(date.getUTCMinutes(), 2) +
    ':' +
    pad(date.getUTCSeconds(), 2) +
    '.' +
    pad(date.getUTCMilliseconds(), 3) +
    'Z'
  )
}

/**
 * Shift an instant into a fixed-offset local view.
 * offsetMinutes uses the JS convention (UTC-120 for UTC+02:00).
 */
function shifted(input: Date | number, offsetMinutes: number): Date {
  const date = input instanceof Date ? new Date(input.getTime()) : new Date(input)
  return new Date(date.getTime() + offsetMinutes * 60_000)
}

/** "YYYY-MM-DD" in the projection time zone. */
export function formatLocalDate(input: Date | number, offsetMinutes: number): string {
  const date = shifted(input, offsetMinutes)
  return date.getUTCFullYear() + '-' + pad(date.getUTCMonth() + 1, 2) + '-' + pad(date.getUTCDate(), 2)
}

/** "HH:MM:SS" in the projection time zone. */
export function formatLocalTime(input: Date | number, offsetMinutes: number): string {
  const date = shifted(input, offsetMinutes)
  return pad(date.getUTCHours(), 2) + ':' + pad(date.getUTCMinutes(), 2) + ':' + pad(date.getUTCSeconds(), 2)
}

/**
 * V1 ships a single fixed zone. Extend this map if more zones are needed.
 * Asia/Shanghai is UTC+08:00 with no DST, so a constant offset is exact.
 */
export const KNOWN_ZONE_OFFSETS: Readonly<Record<string, number>> = {
  'Asia/Shanghai': 480,
}

export function zoneOffsetMinutes(zone: string): number {
  const offset = KNOWN_ZONE_OFFSETS[zone]
  if (offset === undefined) {
    throw new Error('unsupported projection_time_zone: ' + zone)
  }
  return offset
}
