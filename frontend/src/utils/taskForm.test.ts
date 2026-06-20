import { describe, expect, it } from 'vitest'
import { mergeTaskProjects } from './taskForm'

describe('task form project helpers', () => {
  it('accepts project objects from shared task project caches', () => {
    expect(mergeTaskProjects([{ name: '学习写小说' } as never])).toContain('学习写小说')
  })
})
