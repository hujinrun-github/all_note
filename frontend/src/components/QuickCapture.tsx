import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listTaskProjects } from '../api/tasks'
import { useUIStore } from '../stores/ui'
import { useCreateInboxItem } from '../hooks/useInbox'
import { useCreateEvent } from '../hooks/useEvents'
import { useCreateNote } from '../hooks/useNotes'
import { useCreateTask } from '../hooks/useTasks'
import { formatTaskProjectOption } from '../utils/taskProjects'

type Kind = 'task' | 'event' | 'note' | 'idea'

const initialCaptureText = '明天晚上8点复习N2语法'

export function QuickCapture() {
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)
  const createInboxItem = useCreateInboxItem()
  const createEvent = useCreateEvent()
  const createNote = useCreateNote()
  const createTask = useCreateTask()
  const [kind, setKind] = useState<Kind>('event')
  const [title, setTitle] = useState(initialCaptureText)
  const [dateTimeValue, setDateTimeValue] = useState(() => inferDateTimeFromText(initialCaptureText))
  const [timeTouched, setTimeTouched] = useState(false)
  const [selectedProjectID, setSelectedProjectID] = useState('personal')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const { data: taskProjects = [], isLoading: projectsLoading } = useQuery({
    queryKey: ['task-projects'],
    queryFn: listTaskProjects,
  })

  const selectedProject = taskProjects.find((project) => project.id === selectedProjectID)
  const selectedProjectLabel = selectedProject ? formatTaskProjectOption(selectedProject) : 'Personal'

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') setCaptureOpen(false)
      if (e.metaKey && e.shiftKey && e.key === 'K') {
        e.preventDefault()
        setCaptureOpen(true)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [setCaptureOpen])

  useEffect(() => {
    if (taskProjects.length === 0) return
    if (!taskProjects.some((project) => project.id === selectedProjectID)) {
      setSelectedProjectID(taskProjects[0].id)
    }
  }, [selectedProjectID, taskProjects])

  function resetAndClose() {
    setTitle('')
    setCaptureOpen(false)
  }

  async function handleSaveToInbox() {
    if (!title.trim()) return
    setSubmitting(true)
    setError(null)
    try {
      const inboxKind = kind === 'idea' ? 'note' : kind
      await createInboxItem.mutateAsync({
        kind: inboxKind,
        title: title.trim(),
        body: `时间：${formatDateTimeForHint(dateTimeValue)}\n项目：${selectedProjectLabel}`,
      })
      resetAndClose()
    } catch {
      setError('保存失败，请稍后重试。')
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDirectCreate() {
    const trimmedTitle = title.trim()
    if (!trimmedTitle) return

    setSubmitting(true)
    setError(null)
    try {
      const startTime = dateTimeLocalToUnix(dateTimeValue)
      if (kind === 'event') {
        await createEvent.mutateAsync({
          title: trimmedTitle,
          start_time: startTime,
          end_time: startTime + 60 * 60,
          location: selectedProjectLabel,
          kind: selectedProject?.type === 'personal' ? 'personal' : 'work',
          project_id: selectedProjectID,
        })
      } else if (kind === 'task') {
        await createTask.mutateAsync({
          title: trimmedTitle,
          project_id: selectedProjectID,
          due: startTime,
          planned_date: dateTimeLocalToDate(dateTimeValue),
          horizon: 'week',
          scope: 'daily',
        })
      } else {
        await createNote.mutateAsync({
          title: trimmedTitle,
          body: kind === 'idea' ? trimmedTitle : '',
          folder_id: '__uncategorized',
          tags: kind === 'idea' ? '["想法"]' : '[]',
          project_ids: selectedProjectID ? [selectedProjectID] : undefined,
        })
      }
      resetAndClose()
    } catch {
      setError('直接创建失败，请检查时间和项目后重试。')
    } finally {
      setSubmitting(false)
    }
  }

  const kinds: { value: Kind; label: string; icon: string }[] = [
    { value: 'task', label: '任务', icon: '✓' },
    { value: 'event', label: '日程', icon: '◇' },
    { value: 'note', label: '笔记', icon: '▣' },
    { value: 'idea', label: '想法', icon: '▱' },
  ]
  const timeLabel = kind === 'event' ? '日程时间' : kind === 'task' ? '截止时间' : '记录时间'
  const hintCopy =
    kind === 'event'
      ? `已识别为日程，时间为 ${formatDateTimeForHint(dateTimeValue)}。你可以修改时间和项目，直接创建会写入日历。`
      : kind === 'task'
        ? `已识别为任务，计划时间为 ${formatDateTimeForHint(dateTimeValue)}。直接创建会进入所选项目的任务流。`
        : `已识别为${kind === 'idea' ? '想法' : '笔记'}，将归入 ${selectedProjectLabel}；也可以先保存到收件箱稍后整理。`

  return (
    <div className="quick-capture-overlay" onClick={() => setCaptureOpen(false)}>
      <section
        className="quick-capture-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="quick-capture-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="quick-capture-heading">
          <div>
            <h2 id="quick-capture-title">快速捕获</h2>
            <p>快速记录你的想法、任务或日程</p>
          </div>
          <button type="button" className="quick-capture-close" aria-label="关闭快速捕获" onClick={() => setCaptureOpen(false)}>
            ×
          </button>
        </div>

        <textarea
          value={title}
          onChange={(e) => {
            const nextTitle = e.target.value
            setTitle(nextTitle)
            if (!timeTouched) setDateTimeValue(inferDateTimeFromText(nextTitle))
          }}
          placeholder="例如：明天晚上8点复习N2语法"
          className="quick-capture-textarea"
          rows={3}
          autoFocus
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') void handleDirectCreate()
          }}
        />

        <div className="quick-capture-result">
          <span>识别结果</span>
          <div className="quick-capture-kind-grid">
            {kinds.map(({ value, label, icon }) => (
              <button
                key={value}
                type="button"
                onClick={() => setKind(value)}
                className={kind === value ? 'is-active' : ''}
              >
                <span>{icon}</span>
                {label}
              </button>
            ))}
          </div>
        </div>

        <div className="quick-capture-fields">
          <label>
            <span>{timeLabel}</span>
            <div className="quick-capture-time-group">
              <select
                value={dateTimeLocalToDate(dateTimeValue)}
                aria-label="捕获日期"
                onChange={(event) => {
                  setTimeTouched(true)
                  setDateTimeValue(mergeDateTimeParts(event.target.value, dateTimeLocalToTime(dateTimeValue)))
                }}
              >
                {buildDateOptions().map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
              <select
                value={dateTimeLocalToTime(dateTimeValue)}
                aria-label="捕获时间"
                onChange={(event) => {
                  setTimeTouched(true)
                  setDateTimeValue(mergeDateTimeParts(dateTimeLocalToDate(dateTimeValue), event.target.value))
                }}
              >
                {buildTimeOptions().map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </div>
          </label>
          <label>
            <span>项目</span>
            <select
              value={selectedProjectID}
              aria-label="捕获项目"
              onChange={(event) => setSelectedProjectID(event.target.value)}
              disabled={projectsLoading || taskProjects.length === 0}
            >
              {taskProjects.length === 0 ? (
                <option value="personal">{projectsLoading ? '项目加载中...' : 'Personal'}</option>
              ) : (
                taskProjects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {formatTaskProjectOption(project)}
                  </option>
                ))
              )}
            </select>
          </label>
        </div>

        <p className="quick-capture-hint">{hintCopy}</p>

        {error && <div className="quick-capture-error">{error}</div>}

        <div className="quick-capture-actions">
          <button type="button" className="secondary-action" onClick={handleSaveToInbox} disabled={submitting || !title.trim()}>
            保存到收件箱
          </button>
          <button type="button" className="primary-action" onClick={handleDirectCreate} disabled={submitting || !title.trim()}>
            {submitting ? '创建中...' : '直接创建'}
          </button>
        </div>
      </section>
    </div>
  )
}

function inferDateTimeFromText(text: string) {
  const date = new Date()
  if (text.includes('后天')) date.setDate(date.getDate() + 2)
  else if (text.includes('明天')) date.setDate(date.getDate() + 1)

  const timeMatch = text.match(/(\d{1,2})\s*(?:点|:|：)\s*(\d{1,2})?/)
  let hour = 9
  let minute = 0
  if (timeMatch) {
    hour = Number(timeMatch[1])
    minute = timeMatch[2] ? Number(timeMatch[2]) : 0
  }
  if ((text.includes('晚上') || text.includes('下午')) && hour < 12) hour += 12
  date.setHours(hour, minute, 0, 0)
  return dateToDateTimeLocal(date)
}

function dateToDateTimeLocal(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  return `${year}-${month}-${day}T${hour}:${minute}`
}

function dateTimeLocalToUnix(value: string) {
  const time = new Date(value).getTime()
  if (Number.isNaN(time)) return Math.floor(Date.now() / 1000)
  return Math.floor(time / 1000)
}

function dateTimeLocalToDate(value: string) {
  return value.split('T')[0] || dateToDateTimeLocal(new Date()).split('T')[0]
}

function dateTimeLocalToTime(value: string) {
  return value.split('T')[1] || '09:00'
}

function mergeDateTimeParts(date: string, time: string) {
  return `${date || dateTimeLocalToDate(dateToDateTimeLocal(new Date()))}T${time || '09:00'}`
}

function buildDateOptions() {
  const today = new Date()
  return Array.from({ length: 14 }).map((_, index) => {
    const date = new Date(today)
    date.setDate(today.getDate() + index)
    const value = dateToDateTimeLocal(date).split('T')[0]
    const dayLabel =
      index === 0
        ? '今天'
        : index === 1
          ? '明天'
          : index === 2
            ? '后天'
            : new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', weekday: 'short' }).format(date)
    return { value, label: dayLabel }
  })
}

function buildTimeOptions() {
  const options: string[] = []
  for (let hour = 0; hour < 24; hour += 1) {
    for (const minute of [0, 30]) {
      options.push(`${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`)
    }
  }
  return options
}

function formatDateTimeForHint(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '未设置时间'
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}
