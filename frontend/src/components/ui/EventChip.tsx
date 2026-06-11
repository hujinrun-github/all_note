export interface EventData {
  id: string; title: string; start_time: number; end_time: number; location?: string; kind: string
}

const kindColors: Record<string, string> = {
  work: 'bg-fs-accent',
  personal: 'bg-fs-success',
  reminder: 'bg-fs-warning',
}

export function EventChip({ event }: { event: EventData }) {
  const start = new Date(event.start_time * 1000)
  const end = new Date(event.end_time * 1000)
  const timeStr = `${start.getHours().toString().padStart(2, '0')}:${start.getMinutes().toString().padStart(2, '0')} - ${end.getHours().toString().padStart(2, '0')}:${end.getMinutes().toString().padStart(2, '0')}`

  return (
    <div className="event-chip">
      <div className={`w-2.5 h-2.5 rounded-full mt-1.5 shrink-0 ${kindColors[event.kind] ?? 'bg-fs-border'}`} />
      <div className="grid gap-0.5 min-w-0">
        <strong className="text-[13px] leading-snug font-medium text-fs-text">{event.title}</strong>
        <small className="text-fs-text-muted text-xs tabular-nums">
          {timeStr}
          {event.location && ` · ${event.location}`}
        </small>
      </div>
    </div>
  )
}
