import { type ReactNode, useLayoutEffect, useRef } from 'react'
import { Badge } from '../ui/badge'
import { Skeleton } from '../ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../ui/tabs'

export function PlanningHeading({
  id,
  title,
  date,
  description,
  children,
}: {
  id: string
  title: string
  date?: string
  description: string
  children?: ReactNode
}) {
  return (
    <header className="flex flex-col gap-4 pb-6 pt-2 min-[821px]:flex-row min-[821px]:items-center min-[821px]:justify-between min-[821px]:pb-7 min-[821px]:pt-8">
      <div className="min-w-0">
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <h1
            id={id}
            className="text-[2rem] font-bold leading-tight tracking-tight"
          >
            {title}
          </h1>
          {date ? (
            <time className="text-sm text-muted-foreground">{date}</time>
          ) : null}
        </div>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          {description}
        </p>
      </div>
      {children ? (
        <div className="flex shrink-0 flex-wrap gap-2 [&>[data-slot=button]]:min-h-11">
          {children}
        </div>
      ) : null}
    </header>
  )
}

export function PlanningTabs<T extends string>({
  label,
  value,
  tabs,
  onChange,
  children,
}: {
  label: string
  value: T
  tabs: ReadonlyArray<{
    id: T
    label: string
    count?: number
    accessibleLabel?: string
  }>
  onChange: (value: T) => void
  children: ReactNode
}) {
  const scrollerRef = useRef<HTMLDivElement>(null)
  const selectedTabRef = useRef<HTMLButtonElement>(null)

  useLayoutEffect(() => {
    const scroller = scrollerRef.current
    const selectedTab = selectedTabRef.current
    if (!scroller || !selectedTab) return

    const viewport = scroller.getBoundingClientRect()
    const tab = selectedTab.getBoundingClientRect()
    const gutter = 8
    if (tab.left < viewport.left + gutter) {
      scroller.scrollLeft += tab.left - viewport.left - gutter
    } else if (tab.right > viewport.right - gutter) {
      scroller.scrollLeft += tab.right - viewport.right + gutter
    }
  }, [value])

  return (
    <Tabs
      className="min-w-0 gap-0"
      activationMode="manual"
      value={value}
      onValueChange={(next) => {
        const tab = tabs.find(({ id }) => id === next)
        if (tab) onChange(tab.id)
      }}
    >
      <div
        ref={scrollerRef}
        className="planning-tab-scroll min-w-0 overflow-x-auto border-b border-border px-3 py-2 sm:px-4"
      >
        <TabsList aria-label={label} className="planning-tabs">
          {tabs.map(({ id, label, count, accessibleLabel }) => (
            <TabsTrigger
              key={id}
              value={id}
              aria-label={accessibleLabel}
              ref={id === value ? selectedTabRef : undefined}
            >
              {label}{' '}
              {count === undefined ? null : (
                <Badge variant="secondary">{count}</Badge>
              )}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      {tabs.map(({ id }) => (
        <TabsContent key={id} value={id} className="min-w-0">
          {id === value ? children : null}
        </TabsContent>
      ))}
    </Tabs>
  )
}

export function PlanningLoading({ label }: { label: string }) {
  return (
    <div role="status" aria-label={label} className="grid gap-3 py-3">
      <Skeleton className="h-12 w-full rounded-md" />
      <Skeleton className="h-12 w-full rounded-md" />
      <Skeleton className="h-12 w-full rounded-md" />
    </div>
  )
}
