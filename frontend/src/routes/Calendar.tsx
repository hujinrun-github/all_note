import { useState } from 'react'
import { useEventsList } from '../hooks/useEvents'
import { EventChip } from '../components/ui/EventChip'
import type { Event } from '../api/events'

export default function Calendar() {
  const [currentDate, setCurrentDate] = useState(new Date())
  const year = currentDate.getFullYear()
  const month = currentDate.getMonth()

  const monthStr = `${year}-${String(month + 1).padStart(2, '0')}`
  const { data, isLoading } = useEventsList({ month: monthStr })

  const firstDay = new Date(year, month, 1).getDay()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const today = new Date()

  const days = ['日', '一', '二', '三', '四', '五', '六']

  const prevMonth = () => setCurrentDate(new Date(year, month - 1, 1))
  const nextMonth = () => setCurrentDate(new Date(year, month + 1, 1))

  // Map events to day numbers
  const eventsByDay: Record<number, Event[]> = {}
  data?.events.forEach((e) => {
    const startDay = new Date(e.start_time * 1000).getDate()
    const startMonth = new Date(e.start_time * 1000).getMonth()
    if (startMonth === month) {
      if (!eventsByDay[startDay]) eventsByDay[startDay] = []
      eventsByDay[startDay].push(e)
    }
  })

  const [selectedDay, setSelectedDay] = useState<number | null>(null)
  const selectedEvents = selectedDay ? eventsByDay[selectedDay] ?? [] : []

  return (
    <div className="grid grid-cols-[1fr_280px] gap-6 max-[960px]:grid-cols-1 max-w-[900px]">
      <div>
        <div className="flex justify-between items-center mb-4">
          <div className="flex gap-3 items-center">
            <button onClick={prevMonth} className="border border-fs-border rounded-md px-3 py-1 bg-transparent cursor-pointer hover:bg-fs-hover transition-colors text-sm">←</button>
            <strong className="text-lg">{currentDate.toLocaleDateString('zh-CN', { month: 'long', year: 'numeric' })}</strong>
            <button onClick={nextMonth} className="border border-fs-border rounded-md px-3 py-1 bg-transparent cursor-pointer hover:bg-fs-hover transition-colors text-sm">→</button>
          </div>
          <button onClick={() => setCurrentDate(new Date())} className="border border-fs-border rounded-md px-3 py-1 text-xs bg-transparent cursor-pointer hover:bg-fs-hover transition-colors">
            今天
          </button>
        </div>

        {isLoading ? (
          <div className="h-[400px] bg-fs-hover rounded-lg animate-pulse" />
        ) : (
          <div className="grid grid-cols-7 gap-px bg-fs-border rounded-lg overflow-hidden">
            {days.map((d) => <div key={d} className="bg-fs-surface text-center text-xs text-fs-text-muted py-2 font-medium">{d}</div>)}
            {Array.from({ length: firstDay }).map((_, i) => <div key={`empty-${i}`} className="bg-fs-surface min-h-[80px]" />)}
            {Array.from({ length: daysInMonth }).map((_, i) => {
              const day = i + 1
              const isToday = day === today.getDate() && month === today.getMonth() && year === today.getFullYear()
              const dayEvents = eventsByDay[day] || []
              const isSelected = day === selectedDay

              return (
                <div key={day} onClick={() => setSelectedDay(isSelected ? null : day)}
                  className={`bg-fs-surface min-h-[80px] p-1.5 cursor-pointer transition-colors ${isSelected ? 'ring-2 ring-fs-accent' : ''}`}>
                  <span className={`text-xs tabular-nums ${isToday ? 'bg-fs-accent text-white rounded-full w-5 h-5 inline-grid place-items-center' : ''}`}>{day}</span>
                  {dayEvents.length > 0 && (
                    <div className="flex gap-0.5 mt-0.5 flex-wrap">
                      {dayEvents.slice(0, 3).map((e) => (
                        <div key={e.id} className={`w-1.5 h-1.5 rounded-full ${e.kind === 'work' ? 'bg-fs-accent' : e.kind === 'personal' ? 'bg-fs-success' : 'bg-fs-warning'}`} />
                      ))}
                      {dayEvents.length > 3 && <span className="text-[9px] text-fs-text-muted">+{dayEvents.length - 3}</span>}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {selectedDay && (
        <div className="grid gap-3 content-start border-l border-fs-border pl-5 max-[960px]:border-l-0 max-[960px]:pl-0 max-[960px]:border-t max-[960px]:pt-4">
          <h3 className="text-sm font-semibold">{`${month + 1}月${selectedDay}日`}</h3>
          {selectedEvents.length === 0 ? (
            <p className="text-fs-text-muted text-sm">无日程</p>
          ) : (
            selectedEvents.map((e) => <EventChip key={e.id} event={e} />)
          )}
        </div>
      )}
    </div>
  )
}
