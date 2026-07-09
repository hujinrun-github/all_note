// @ts-expect-error -- Vitest runs in Node, while the app tsconfig intentionally omits Node types.
import { readFileSync } from 'node:fs'
// @ts-expect-error -- Vitest runs in Node, while the app tsconfig intentionally omits Node types.
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

declare const process: {
  cwd(): string
}

const stylesheet = readFileSync(resolve(process.cwd(), 'src/styles/index.css'), 'utf8')

function getRootVariable(name: string) {
  const match = stylesheet.match(new RegExp(`${name}:\\s*([^;]+);`))
  return match?.[1]?.trim()
}

describe('workspace layout width contract', () => {
  it('keeps logged-in pages wide enough to avoid large side gutters', () => {
    expect(getRootVariable('--fs-content-max')).toBe('1560px')
    expect(getRootVariable('--fs-page-x')).toBe('clamp(20px, 1.8vw, 28px)')
  })
})
