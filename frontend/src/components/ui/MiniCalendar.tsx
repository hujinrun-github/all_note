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
    <div className="page-card">
      <div className="mb-3">
        <strong className="text-[14px] font-semibold text-fs-text font-[family-name:var(--font-family-heading)]">
          {currentDate.toLocaleDateString('zh-CN', { month: 'long', year: 'numeric' })}
        </strong>
      </div>
      <div className="grid grid-cols-7 gap-1 text-center text-xs">
        {days.map((d) => <div key={d} className="text-fs-text-muted text-[11px] pb-1.5 font-medium">{d}</div>)}
        {Array.from({ length: firstDay }).map((_, i) => <div key={`empty-${i}`} className="min-h-[30px]" />)}
        {Array.from({ length: daysInMonth }).map((_, i) => {
          const day = i + 1
          const isToday = day === today
          return (
            <div
              key={day}
              className={`min-h-[30px] grid place-items-center rounded-md text-xs tabular-nums transition-colors ${
                isToday ? 'bg-fs-accent text-white font-semibold shadow-sm' : 'hover:bg-fs-hover text-fs-text-secondary'
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
