import { useState } from 'react'

export function MiniCalendar() {
  const [currentDate] = useState(new Date())
  const year = currentDate.getFullYear()
  const month = currentDate.getMonth()

  const firstDay = new Date(year, month, 1).getDay()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const today = currentDate.getDate()

  const days = ['日', '一', '二', '三', '四', '五', '六']

  return (
    <div>
      <div className="mb-2">
        <strong className="text-[13px] font-semibold">{currentDate.toLocaleDateString('zh-CN', { month: 'long', year: 'numeric' })}</strong>
      </div>
      <div className="grid grid-cols-7 gap-[3px] text-center text-xs">
        {days.map((d) => <div key={d} className="text-fs-text-muted text-[11px] pb-1">{d}</div>)}
        {Array.from({ length: firstDay }).map((_, i) => <div key={`empty-${i}`} className="min-h-[30px]" />)}
        {Array.from({ length: daysInMonth }).map((_, i) => {
          const day = i + 1
          const isToday = day === today
          return (
            <div
              key={day}
              className={`min-h-[30px] grid place-items-center rounded-md text-xs tabular-nums ${
                isToday ? 'bg-fs-accent text-white font-semibold' : 'hover:bg-fs-hover'
              }`}
            >
              {day}
            </div>
          )
        })}
      </div>
    </div>
  )
}
