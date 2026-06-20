export function parseSyncTagsInput(value: string): string[] {
  const seen = new Set<string>()
  const tags: string[] = []

  for (const item of value.split(/[,，\n]/)) {
    const tag = item.trim().replace(/^#+/, '').trim()
    if (!tag) continue
    const key = tag.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    tags.push(tag)
  }

  return tags
}

export function syncTagsInputFromConfig(raw: string | undefined): string {
  return syncTagsFromConfig(raw).join(', ')
}

export function syncTagsFromConfig(raw: string | undefined): string[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
      return []
    const tags = (parsed as Record<string, unknown>).required_tags
    if (!Array.isArray(tags)) return []
    return tags.filter((tag): tag is string => typeof tag === 'string')
  } catch {
    return []
  }
}
