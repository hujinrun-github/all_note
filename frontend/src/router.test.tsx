import { describe, expect, it } from 'vitest'
import { router } from './router'

function flattenedPaths() {
  return router.routes.flatMap((route) => [
    route.path,
    ...(route.children ?? []).map((child) => {
      if (!route.path || route.path === '/') return `/${child.path ?? ''}`.replace(/\/$/, '') || '/'
      return `${route.path}/${child.path ?? ''}`
    }),
  ])
}

describe('router', () => {
  it('registers account management pages instead of falling through to React Router 404', () => {
    const paths = flattenedPaths()

    expect(paths).toContain('/change-password')
    expect(paths).toContain('/admin/users')
  })
})
