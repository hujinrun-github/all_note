const TASK_COLOR_STORAGE_KEY = 'flowspace:task-colors:v1'
const TASK_COLOR_PATTERN = /^hsl\(\d{1,3} \d{1,3}% \d{1,3}%\)$/

let cachedRegistry: Record<string, string> | null = null

function hashString(value: string) {
  let hash = 2166136261
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return hash >>> 0
}

function loadRegistry(): Record<string, string> {
  if (cachedRegistry !== null) return cachedRegistry

  let registry: Record<string, string> = {}
  if (typeof window === 'undefined') {
    cachedRegistry = registry
    return registry
  }

  try {
    const stored = JSON.parse(
      window.localStorage.getItem(TASK_COLOR_STORAGE_KEY) ?? '{}'
    )
    if (!stored || typeof stored !== 'object' || Array.isArray(stored)) {
      cachedRegistry = registry
      return registry
    }
    registry = Object.fromEntries(
      Object.entries(stored).filter(
        ([taskID, color]) =>
          Boolean(taskID) &&
          typeof color === 'string' &&
          TASK_COLOR_PATTERN.test(color)
      )
    ) as Record<string, string>
  } catch {
    // Private browsing or a malformed previous value should not block rendering.
  }

  cachedRegistry = registry
  return registry
}

function saveRegistry(registry: Record<string, string>) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(
      TASK_COLOR_STORAGE_KEY,
      JSON.stringify(registry)
    )
  } catch {
    // The in-memory registry still keeps colors stable for this session.
  }
}

function createCandidate(taskID: string, attempt: number) {
  const hue = hashString(`${taskID}:hue:${attempt}`) % 360
  const saturation = 58 + (hashString(`${taskID}:sat:${attempt}`) % 17)
  const lightness = 42 + (hashString(`${taskID}:light:${attempt}`) % 13)
  return `hsl(${hue} ${saturation}% ${lightness}%)`
}

export function getTaskColor(taskID: string, explicitColor?: string) {
  if (explicitColor?.trim()) return explicitColor.trim()

  const registry = loadRegistry()
  if (registry[taskID]) return registry[taskID]

  const usedColors = new Set(Object.values(registry))
  let attempt = 0
  let color = createCandidate(taskID, attempt)
  while (usedColors.has(color)) {
    attempt += 1
    color = createCandidate(taskID, attempt)
  }

  registry[taskID] = color
  saveRegistry(registry)
  return color
}
