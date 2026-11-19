import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Check, LoaderCircle, Mic, Square, WandSparkles, X } from 'lucide-react'
import * as planning from '../../api/planning'
import { APIError, apiResourceURL } from '../../api/client'
import type { ScheduleV2Input } from '../../api/taskDomain'
import { useProjects, taskDomainQueryKeys } from '../../hooks/useTaskDomain'
import { workspaceQueryKeys } from '../../hooks/useWorkspace'
import { useVoiceRecorder } from '../../hooks/useVoiceRecorder'
import { useUIStore } from '../../stores/ui'
import { Modal } from '../ui/Modal'
import { Button } from '../ui/button'
import { Input } from '../ui/input'
import { NativeSelect, NativeSelectOption } from '../ui/native-select'
import { Alert, AlertDescription, AlertTitle } from '../ui/alert'
import { Badge } from '../ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '../ui/card'
import {
  TaskScheduleFields,
  buildTaskScheduleInput,
  createTaskScheduleDraft,
  type TaskScheduleDraft,
} from '../taskDomain/TaskScheduleFields'

type Fields = Pick<
  planning.PlanDraft,
  'input_text' | 'project_id' | 'title' | 'items'
>
const activeJob = (job?: planning.PlanJob) =>
  job?.state === 'queued' || job?.state === 'processing'
const fieldsOf = (draft: planning.PlanDraft): Fields => ({
  input_text: draft.input_text || '',
  project_id: draft.project_id || '',
  title: draft.title || '',
  items: draft.items || [],
})
const emptyFields = (projectID: string): Fields => ({
  input_text: '',
  project_id: projectID,
  title: '',
  items: [],
})
const statusText: Record<string, string> = {
  queued: '排队中',
  processing: '处理中',
  completed: '已完成',
  failed: '失败',
  cancelled: '已取消',
  stale: '输入已更新，请重新生成',
}

const jobErrorMessages: Record<string, string> = {
  invalid_audio: '录音格式无法识别，请重新录制或改用文字输入。',
  audio_too_large: '录音文件过大，请缩短后重新录制。',
  audio_too_long: '录音超过时长限制，请分成更短的录音。',
  audio_decoder_unavailable:
    '音频处理服务暂不可用，请稍后重试，或使用文字输入。',
  empty_transcript: '没有识别到文字，请检查录音后重录，或输入文字。',
  invalid_plan: '生成结果无法形成有效计划，请重试或补充更具体的要求。',
  provider_timeout: '服务响应超时，输入已保留，请稍后重试。',
  service_unavailable: '所需服务暂不可用，请在用户设置中检查文本或语音服务。',
  provider_failed: '处理服务暂时失败，输入已保留，请稍后重试。',
  input_changed: '输入已更新，请使用当前文字重新生成，或重新转写录音。',
}

export default function PlanCapture() {
  const queryClient = useQueryClient()
  const projectContext = useUIStore((state) => state.captureProjectID)
  const closeCapture = useUIStore((state) => state.setCaptureOpen)
  const [draft, setDraft] = useState<planning.PlanDraft | null>(null)
  const draftRef = useRef<planning.PlanDraft | null>(null)
  const [fields, setFields] = useState<Fields>(() =>
    emptyFields(projectContext)
  )
  const fieldsRef = useRef(fields)
  const [timezone, setTimezone] = useState(
    () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai'
  )
  const [job, setJob] = useState<planning.PlanJob>()
  const [busy, setBusy] = useState('')
  const busyRef = useRef(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [dirty, setDirty] = useState(false)
  const dirtyRef = useRef(false)
  const editVersion = useRef(0)
  const [validation, setValidation] = useState<planning.PlanValidation>()
  const [allowConflicts, setAllowConflicts] = useState(false)
  const [publish, setPublish] = useState(true)
  const [newProject, setNewProject] = useState<{
    name: string
    kind: 'standard' | 'learning'
    horizon: 'short' | 'long'
  } | null>(null)
  const [receipt, setReceipt] = useState<planning.PlanReceipt>()
  const createRequest = useRef<
    Parameters<typeof planning.createPlanDraft>[0] | null
  >(null)
  const requestIDs = useRef(new Map<string, string>())
  const capabilities = useQuery({
    queryKey: ['planning', 'capabilities'],
    queryFn: planning.getPlanningCapabilities,
    retry: false,
  })
  const recent = useQuery({
    queryKey: ['planning', 'drafts'],
    queryFn: planning.listPlanDrafts,
    retry: false,
  })
  const projects = useProjects()
  const recorder = useVoiceRecorder(
    capabilities.data?.max_duration_seconds,
    capabilities.data?.max_audio_bytes
  )
  const [audioURL, setAudioURL] = useState('')
  const polling = useQuery({
    queryKey: ['planning', 'job', job?.id],
    queryFn: () => planning.getPlanJob(job!.id),
    enabled: !!job && activeJob(job),
    refetchInterval: (query) =>
      activeJob(query.state.data) || !query.state.data ? 1200 : false,
    refetchIntervalInBackground: false,
    retry: 1,
  })
  const completedJob = useRef('')

  function assignDraft(next: planning.PlanDraft) {
    draftRef.current = next
    setDraft(next)
  }
  function applyDraft(next: planning.PlanDraft) {
    assignDraft(next)
    const nextFields = fieldsOf(next)
    fieldsRef.current = nextFields
    setFields(nextFields)
    setTimezone(next.timezone)
    dirtyRef.current = false
    setDirty(false)
    setJob(next.latest_job)
    setReceipt(next.receipt)
    setValidation(undefined)
    setAllowConflicts(false)
  }
  function edit(patch: Partial<Fields>) {
    if (
      patch.input_text !== undefined &&
      patch.input_text !== fieldsRef.current.input_text
    ) {
      patch = { ...patch, items: [], title: '' }
    }
    fieldsRef.current = { ...fieldsRef.current, ...patch }
    setFields(fieldsRef.current)
    editVersion.current++
    dirtyRef.current = true
    setDirty(true)
    setValidation(undefined)
    setAllowConflicts(false)
    setNotice('尚未保存')
    setError('')
  }
  function requestID(key: string) {
    if (!requestIDs.current.has(key))
      requestIDs.current.set(key, planning.newPlanningRequestID())
    return requestIDs.current.get(key)!
  }
  async function run(label: string, action: () => Promise<void>) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(label)
    setError('')
    setNotice('')
    try {
      await action()
    } catch (caught) {
      if (caught instanceof APIError && caught.status === 409) {
        const current = draftRef.current
        if (current) {
          try {
            assignDraft(await planning.getPlanDraft(current.id))
          } catch {
            /* preserve the local input */
          }
        }
        setValidation(undefined)
        setError(
          '草稿或项目已更新，你的输入仍保留。请保存草稿并重新检查后再提交。'
        )
      } else
        setError(
          caught instanceof Error
            ? caught.message
            : '操作失败，输入已保留，请重试。'
        )
    } finally {
      busyRef.current = false
      setBusy('')
    }
  }
  async function saveCurrent() {
    const version = editVersion.current
    const snapshot = fieldsRef.current
    let current = draftRef.current
    if (!current) {
      createRequest.current ??= {
        request_id: planning.newPlanningRequestID(),
        input_text: snapshot.input_text,
        timezone,
        ...(snapshot.project_id ? { project_id: snapshot.project_id } : {}),
      }
      current = await planning.createPlanDraft(createRequest.current)
      assignDraft(current)
    }
    if (
      dirtyRef.current ||
      JSON.stringify(fieldsOf(current)) !== JSON.stringify(snapshot)
    ) {
      current = await planning.updatePlanDraft(current.id, {
        expected_revision: current.revision,
        ...snapshot,
      })
      assignDraft(current)
    }
    if (editVersion.current === version) {
      dirtyRef.current = false
      setDirty(false)
      setNotice('草稿已保存，可关闭后继续')
    }
    await queryClient.invalidateQueries({ queryKey: ['planning', 'drafts'] })
    return current
  }
  function close() {
    if (busyRef.current) return
    if (
      recorder.status === 'recording' ||
      recorder.status === 'requesting' ||
      recorder.status === 'stopping' ||
      recorder.blob ||
      dirtyRef.current ||
      (!draftRef.current && fieldsRef.current.input_text)
    ) {
      if (
        !window.confirm(
          '录音或文字尚未全部保存，关闭会丢失未上传录音和未保存修改。仍要关闭吗？'
        )
      )
        return
    }
    recorder.cancel()
    closeCapture(false)
  }
  function launch(stage: planning.PlanJob['stage']) {
    void run(
      stage === 'generate' ? '正在生成草稿' : '正在上传与转写',
      async () => {
        let saved = await saveCurrent()
        if (stage === 'transcribe' && recorder.blob) {
          saved = await planning.uploadPlanAudio(
            saved.id,
            saved.revision,
            recorder.blob
          )
          assignDraft(saved)
          recorder.cancel()
        }
        const result = await planning.startPlanJob(
          saved.id,
          stage,
          saved.revision,
          requestID(`${saved.id}:${saved.revision}:${stage}`)
        )
        assignDraft(result.draft)
        queryClient.setQueryData(['planning', 'job', result.job.id], result.job)
        setJob(result.job)
        setValidation(undefined)
      }
    )
  }
  function editItem(id: string, patch: Partial<planning.PlanItem>) {
    edit({
      items: fieldsRef.current.items.map((item) =>
        item.id === id ? { ...item, ...patch } : item
      ),
    })
  }

  useEffect(() => {
    if (!recorder.blob) return
    const url = URL.createObjectURL(recorder.blob)
    setAudioURL(url)
    return () => {
      URL.revokeObjectURL(url)
      setAudioURL('')
    }
  }, [recorder.blob])

  useEffect(() => {
    const unload = (event: BeforeUnloadEvent) => {
      if (
        dirtyRef.current ||
        recorder.blob ||
        ['recording', 'requesting', 'stopping'].includes(recorder.status)
      ) {
        event.preventDefault()
        event.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', unload)
    return () => window.removeEventListener('beforeunload', unload)
  }, [recorder.blob, recorder.status])

  useEffect(() => {
    const result = polling.data
    if (!result || activeJob(result) || completedJob.current === result.id)
      return
    const version = editVersion.current
    let disposed = false
    planning
      .getPlanDraft(result.draft_id)
      .then((next) => {
        if (disposed) return
        completedJob.current = result.id
        assignDraft(next)
        setJob(result)
        if (editVersion.current === version && !dirtyRef.current)
          applyDraft(next)
        else setNotice('处理已结束，你编辑的内容已保留；保存后可重新生成。')
        void queryClient.invalidateQueries({ queryKey: ['planning', 'drafts'] })
      })
      .catch(() => {
        if (!disposed) {
          setJob(result)
          setError('无法刷新处理结果，请从最近草稿重新打开。')
        }
      })
    return () => {
      disposed = true
    }
  }, [polling.data, queryClient])

  useEffect(() => {
    if (
      !dirty ||
      busy ||
      error ||
      receipt ||
      activeJob(job) ||
      !capabilities.data ||
      (!draft?.id && !fields.input_text.trim())
    )
      return
    const timer = setTimeout(() => {
      void run('正在保存草稿', async () => {
        await saveCurrent()
      })
    }, 1200)
    return () => clearTimeout(timer)
    // The saved snapshot is read from refs to keep typing responsive.
  }, [
    fields,
    draft?.id,
    dirty,
    busy,
    error,
    receipt,
    job?.state,
    capabilities.data,
  ])

  const recording = ['recording', 'requesting', 'stopping'].includes(
    recorder.status
  )
  const selectedCount = fields.items.filter((item) => item.selected).length
  const availableProjects = (projects.data || []).filter(
    (project) => project.status === 'active' || project.status === 'planning'
  )
  const selectedProject = availableProjects.find(
    (project) => project.id === fields.project_id
  )
  const readOnly = !!receipt || draft?.status === 'committed'
  const isJobActive = activeJob(job)
  const audioSource = recorder.blob
    ? audioURL
    : draft?.audio_available
      ? apiResourceURL(`/api/plan-drafts/${encodeURIComponent(draft.id)}/audio`)
      : ''
  const canCommit =
    !!validation &&
    validation.draft_revision === draft?.revision &&
    !dirty &&
    validation.issues.length === 0 &&
    (!validation.conflicts.length || allowConflicts)

  return (
    <Modal
      className="plan-capture-dialog"
      labelledBy="plan-capture-title"
      onClose={close}
    >
      <section className="plan-capture">
        <header className="plan-capture-header">
          <div>
            <h2 id="plan-capture-title">语音与文字生成计划</h2>
            <p>说出目标，核对草稿，再创建到任务和日历。</p>
          </div>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            aria-label="关闭计划面板"
            disabled={!!busy}
            onClick={close}
          >
            <X />
          </Button>
        </header>
        {error ? (
          <Alert variant="destructive">
            <AlertTitle>操作未完成</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        {capabilities.isError ? (
          <Alert>
            <AlertTitle>暂时无法读取计划服务</AlertTitle>
            <AlertDescription>
              输入仍可编辑和保存。
              <Button
                variant="link"
                onClick={() => void capabilities.refetch()}
              >
                重试连接
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}
        {capabilities.data?.reason ? (
          <p role="status">{capabilities.data.reason}</p>
        ) : null}
        {notice || busy ? (
          <p className="plan-save-status" role="status">
            {busy ? (
              <>
                <LoaderCircle className="animate-spin" aria-hidden="true" />
                {busy}
              </>
            ) : (
              notice
            )}
          </p>
        ) : null}
        {receipt || readOnly ? (
          <Card>
            <CardHeader>
              <CardTitle>
                <Check aria-hidden="true" />
                计划已创建
              </CardTitle>
              <CardDescription>
                {receipt
                  ? `本次创建 ${receipt.task_ids.length} 个任务。`
                  : '这份草稿已提交，无法再次创建。'}
                未勾选项没有创建，可复制文字为新草稿。
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap gap-2">
              <Button asChild>
                <Link
                  to={`/projects/${encodeURIComponent(receipt?.project_id || draft!.project_id)}?section=tasks${receipt?.task_ids[0] ? `&task_id=${encodeURIComponent(receipt.task_ids[0])}` : ''}`}
                  onClick={() => closeCapture(false)}
                >
                  查看本次创建
                </Link>
              </Button>
              <Button asChild variant="outline">
                <Link to="/calendar" onClick={() => closeCapture(false)}>
                  查看日历
                </Link>
              </Button>
              <Button
                variant="outline"
                onClick={() => {
                  const input = fields.input_text
                  applyDraft({
                    ...draft!,
                    id: '',
                    status: 'input',
                    items: [],
                    receipt: undefined,
                  })
                  draftRef.current = null
                  setDraft(null)
                  createRequest.current = null
                  edit({ input_text: input, items: [], title: '' })
                  setReceipt(undefined)
                  setJob(undefined)
                }}
              >
                复制文字为新草稿
              </Button>
            </CardContent>
          </Card>
        ) : null}
        {!draft && recent.data?.length ? (
          <aside className="plan-recent" aria-label="最近未完成草稿">
            <strong>继续上次草稿</strong>
            <div className="flex flex-wrap gap-2">
              {recent.data.slice(0, 5).map((item) => (
                <Button
                  key={item.id}
                  variant="outline"
                  size="sm"
                  disabled={!!busy}
                  onClick={() =>
                    void run('正在恢复草稿', async () => {
                      if (
                        (dirtyRef.current || recorder.blob || recording) &&
                        !window.confirm(
                          '打开已保存草稿将替换当前未保存输入，是否继续？'
                        )
                      )
                        return
                      const next = await planning.getPlanDraft(item.id)
                      recorder.cancel()
                      applyDraft(next)
                      createRequest.current = null
                    })
                  }
                >
                  {item.title ||
                    item.input_text?.slice(0, 18) ||
                    '未命名语音草稿'}
                </Button>
              ))}
            </div>
          </aside>
        ) : null}
        {recent.isError && !draft ? (
          <p>
            最近草稿暂不可用。
            <Button variant="link" onClick={() => void recent.refetch()}>
              重新加载
            </Button>
          </p>
        ) : null}
        <div className="plan-capture-columns">
          <Card>
            <CardHeader>
              <CardTitle>1. 描述你的安排</CardTitle>
              <CardDescription>
                文字和录音共用同一份草稿，不会额外生成笔记。
              </CardDescription>
            </CardHeader>
            <CardContent className="plan-fields">
              <fieldset disabled={!!busy || readOnly} className="plan-fields">
                <label className="plan-field">
                  <span>归属项目</span>
                  <NativeSelect
                    aria-label="计划归属项目"
                    value={newProject ? '__new__' : fields.project_id}
                    onChange={(event) => {
                      if (event.target.value === '__new__') {
                        setNewProject({
                          name: '',
                          kind: 'standard',
                          horizon: 'short',
                        })
                        edit({ project_id: '' })
                      } else {
                        setNewProject(null)
                        edit({ project_id: event.target.value })
                      }
                    }}
                  >
                    <NativeSelectOption value="">系统收件箱</NativeSelectOption>
                    {availableProjects.map((project) => (
                      <NativeSelectOption key={project.id} value={project.id}>
                        {project.name}
                        {project.status === 'planning' ? '（规划中）' : ''}
                      </NativeSelectOption>
                    ))}
                    <NativeSelectOption value="__new__">
                      新建项目…
                    </NativeSelectOption>
                  </NativeSelect>
                </label>
                {newProject ? (
                  <fieldset className="plan-fields">
                    <legend>确认时新建项目</legend>
                    <label className="plan-field">
                      <span>新项目名称</span>
                      <Input
                        aria-label="新项目名称"
                        value={newProject.name}
                        onChange={(event) => {
                          setNewProject({
                            ...newProject,
                            name: event.target.value,
                          })
                          setValidation(undefined)
                        }}
                      />
                    </label>
                    <label className="plan-field">
                      <span>项目类型</span>
                      <NativeSelect
                        aria-label="新项目类型"
                        value={newProject.kind}
                        onChange={(event) => {
                          setNewProject({
                            ...newProject,
                            kind: event.target.value as 'standard' | 'learning',
                          })
                          setValidation(undefined)
                        }}
                      >
                        <NativeSelectOption value="standard">
                          标准项目
                        </NativeSelectOption>
                        <NativeSelectOption value="learning">
                          学习项目
                        </NativeSelectOption>
                      </NativeSelect>
                    </label>
                    <p className="text-sm text-muted-foreground">
                      确认创建时才会新建项目。恢复草稿后请重新确认新项目信息。
                    </p>
                  </fieldset>
                ) : null}
                <label className="plan-field">
                  <span>时区</span>
                  <Input
                    aria-label="计划时区"
                    value={timezone}
                    disabled={!!draft}
                    onChange={(event) => setTimezone(event.target.value)}
                  />
                </label>
                <div className="plan-recording">
                  <div className="flex flex-wrap items-center gap-2">
                    {!recording ? (
                      <Button
                        type="button"
                        variant="outline"
                        disabled={
                          !capabilities.data?.transcription_available ||
                          isJobActive
                        }
                        onClick={() => {
                          if (
                            !recorder.blob ||
                            window.confirm(
                              '重新录音会丢弃尚未上传的录音，是否继续？'
                            )
                          ) {
                            setValidation(undefined)
                            void recorder.start()
                          }
                        }}
                      >
                        <Mic data-icon="inline-start" />
                        开始录音
                      </Button>
                    ) : (
                      <Button
                        type="button"
                        variant="destructive"
                        disabled={recorder.status !== 'recording'}
                        onClick={recorder.stop}
                      >
                        <Square data-icon="inline-start" />
                        {recorder.status === 'requesting'
                          ? '正在请求权限'
                          : recorder.status === 'stopping'
                            ? '正在结束'
                            : '结束录音'}
                      </Button>
                    )}
                    {recording ? (
                      <Button
                        type="button"
                        variant="ghost"
                        onClick={recorder.cancel}
                      >
                        取消录音
                      </Button>
                    ) : null}
                    <span role="timer" aria-label="录音时长">
                      {Math.floor(recorder.seconds / 60)}:
                      {String(recorder.seconds % 60).padStart(2, '0')}
                    </span>
                  </div>
                  <p>
                    每段最长{' '}
                    {Math.floor(
                      (capabilities.data?.max_duration_seconds || 180) / 60
                    )}{' '}
                    分钟。录音上传完成前请保留此页面。
                  </p>
                  {recorder.error ? <p role="alert">{recorder.error}</p> : null}
                  {audioSource ? (
                    <audio
                      controls
                      src={audioSource}
                      aria-label="计划原始录音"
                    />
                  ) : null}
                  {recorder.blob || draft?.audio_available ? (
                    <Button
                      type="button"
                      variant="secondary"
                      disabled={
                        isJobActive ||
                        !capabilities.data?.transcription_available ||
                        recording
                      }
                      onClick={() => launch('transcribe')}
                    >
                      {recorder.blob ? '上传并转写' : '重新转写录音'}
                    </Button>
                  ) : null}
                </div>
                <label className="plan-field">
                  <span>计划描述 / 可编辑转写</span>
                  <textarea
                    aria-label="计划描述"
                    rows={7}
                    value={fields.input_text}
                    placeholder="例如：下周一到周五，每晚八点复习单词半小时，周三再练半小时听力。"
                    onChange={(event) =>
                      edit({ input_text: event.target.value })
                    }
                  />
                </label>
                {draft?.transcript ? (
                  <details>
                    <summary>查看原始转写</summary>
                    <p className="whitespace-pre-wrap">{draft.transcript}</p>
                  </details>
                ) : null}
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    disabled={
                      !fields.input_text.trim() ||
                      isJobActive ||
                      recording ||
                      !capabilities.data?.generation_available
                    }
                    onClick={() => launch('generate')}
                  >
                    <WandSparkles data-icon="inline-start" />
                    {fields.items.length ? '重新生成草稿' : '生成计划草稿'}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={
                      recording || (!fields.input_text.trim() && !draft)
                    }
                    onClick={() =>
                      void run('正在保存草稿', async () => {
                        await saveCurrent()
                      })
                    }
                  >
                    保存草稿
                  </Button>
                </div>
              </fieldset>
              {job ? (
                <div className="plan-job" role="status">
                  <Badge variant="secondary">
                    {job.stage === 'transcribe' ? '语音转写' : '计划生成'} ·{' '}
                    {statusText[
                      polling.data?.id === job.id
                        ? polling.data.state
                        : job.state
                    ] || job.state}
                  </Badge>
                  {job.error_code ? (
                    <p>
                      {jobErrorMessages[job.error_code] ||
                        '处理未完成，输入已保留，请重试或重新生成。'}
                    </p>
                  ) : null}
                  <div className="flex gap-2">
                    {isJobActive ? (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={!!busy}
                        onClick={() =>
                          void run('正在取消处理', async () => {
                            const result = await planning.controlPlanJob(
                              job.id,
                              'cancel',
                              requestID(`${job.id}:cancel`)
                            )
                            assignDraft(result.draft)
                            queryClient.setQueryData(
                              ['planning', 'job', result.job.id],
                              result.job
                            )
                            setJob(result.job)
                          })
                        }
                      >
                        取消处理
                      </Button>
                    ) : job.retryable ? (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={!!busy || dirty}
                        onClick={() =>
                          void run('正在重试', async () => {
                            completedJob.current = ''
                            const result = await planning.controlPlanJob(
                              job.id,
                              'retry',
                              planning.newPlanningRequestID()
                            )
                            assignDraft(result.draft)
                            queryClient.setQueryData(
                              ['planning', 'job', result.job.id],
                              result.job
                            )
                            setJob(result.job)
                          })
                        }
                      >
                        重试处理
                      </Button>
                    ) : null}
                    {polling.isError ? (
                      <Button
                        variant="link"
                        onClick={() => void polling.refetch()}
                      >
                        重查状态
                      </Button>
                    ) : null}
                  </div>
                </div>
              ) : null}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>2. 核对计划草稿</CardTitle>
              <CardDescription>
                生成内容是建议，确认前不会创建任务。未提供的时间可以保留未安排。
              </CardDescription>
            </CardHeader>
            <CardContent className="plan-fields">
              {!fields.items.length ? (
                <p className="text-muted-foreground">
                  生成后，在这里调整任务、日期和重复规则。
                </p>
              ) : (
                <fieldset disabled={!!busy || readOnly} className="plan-fields">
                  <label className="plan-field">
                    <span>计划标题</span>
                    <Input
                      aria-label="计划标题"
                      value={fields.title}
                      onChange={(event) => edit({ title: event.target.value })}
                    />
                  </label>
                  {draft?.assumptions?.length ? (
                    <Alert>
                      <AlertTitle>采用的建议</AlertTitle>
                      <AlertDescription>
                        <ul>
                          {draft.assumptions.map((value, index) => (
                            <li key={index}>{value}</li>
                          ))}
                        </ul>
                      </AlertDescription>
                    </Alert>
                  ) : null}
                  {draft?.clarifications?.length ? (
                    <Alert>
                      <AlertTitle>需要你核对</AlertTitle>
                      <AlertDescription>
                        <ul>
                          {draft.clarifications.map((value, index) => (
                            <li key={index}>{value}</li>
                          ))}
                        </ul>
                      </AlertDescription>
                    </Alert>
                  ) : null}
                  {fields.items.map((item, index) => (
                    <PlanItemEditor
                      key={item.id}
                      item={item}
                      index={index}
                      onChange={(patch) => editItem(item.id, patch)}
                    />
                  ))}
                </fieldset>
              )}
            </CardContent>
          </Card>
        </div>
        {fields.items.length && !readOnly ? (
          <footer className="plan-confirm plan-fields">
            <p>
              已选 {selectedCount} / {fields.items.length}{' '}
              个任务。未勾选项本次不创建；提交后可复制文字为新草稿。
            </p>
            {validation ? (
              <div aria-live="polite">
                {validation.issues.length ? (
                  <Alert variant="destructive">
                    <AlertTitle>请先补充或修改</AlertTitle>
                    <AlertDescription>
                      <ul>
                        {validation.issues.map((issue, index) => (
                          <li key={index}>{issue}</li>
                        ))}
                      </ul>
                    </AlertDescription>
                  </Alert>
                ) : (
                  <p>必填信息检查通过。</p>
                )}
                {validation.conflicts.length ? (
                  <Alert>
                    <AlertTitle>未来 14 天的安排存在重叠</AlertTitle>
                    <AlertDescription>
                      <ul>
                        {validation.conflicts.map((conflict, index) => (
                          <li key={index}>{conflict}</li>
                        ))}
                      </ul>
                      <label className="plan-check">
                        <input
                          type="checkbox"
                          checked={allowConflicts}
                          onChange={(event) =>
                            setAllowConflicts(event.target.checked)
                          }
                        />
                        我已核对，保留重叠安排
                      </label>
                    </AlertDescription>
                  </Alert>
                ) : null}
                <p className="text-sm text-muted-foreground">
                  仅检查有限日期窗口，之后的重复安排需继续核对。提交时会重新检查。
                </p>
              </div>
            ) : null}
            <div className="plan-confirm-actions">
              <label className="plan-field">
                <span>创建方式</span>
                <NativeSelect
                  aria-label="计划创建方式"
                  value={publish ? 'publish' : 'draft'}
                  disabled={!!busy}
                  onChange={(event) =>
                    setPublish(event.target.value === 'publish')
                  }
                >
                  <NativeSelectOption value="publish">
                    创建并开始执行
                  </NativeSelectOption>
                  <NativeSelectOption value="draft">
                    仅保存为任务草稿
                  </NativeSelectOption>
                </NativeSelect>
              </label>
              {publish &&
              selectedProject?.status === 'planning' &&
              !newProject ? (
                <p>“创建并开始执行”将启用当前规划中的项目。</p>
              ) : null}
              <Button
                type="button"
                variant="outline"
                disabled={
                  !!busy ||
                  !selectedCount ||
                  isJobActive ||
                  recording ||
                  !!recorder.blob
                }
                onClick={() =>
                  void run('正在检查安排', async () => {
                    const saved = await saveCurrent()
                    const result = await planning.validatePlanDraft(
                      saved.id,
                      saved.revision
                    )
                    setValidation(result)
                    setAllowConflicts(false)
                  })
                }
              >
                检查安排
              </Button>
              <Button
                type="button"
                disabled={
                  !!busy ||
                  !selectedCount ||
                  !canCommit ||
                  isJobActive ||
                  recording ||
                  !!recorder.blob ||
                  (!!newProject && !newProject.name.trim())
                }
                onClick={() =>
                  void run('正在创建任务', async () => {
                    const saved = draftRef.current!
                    const effectivePublish = publish
                    const result = await planning.commitPlanDraft(saved.id, {
                      expected_revision: saved.revision,
                      request_id: requestID(
                        `${saved.id}:${saved.revision}:commit:${effectivePublish}:${allowConflicts}:${JSON.stringify(newProject)}`
                      ),
                      publish: effectivePublish,
                      allow_conflicts: allowConflicts,
                      ...(newProject
                        ? {
                            new_project: {
                              ...newProject,
                              name: newProject.name.trim(),
                            },
                          }
                        : {}),
                    })
                    setReceipt(result)
                    dirtyRef.current = false
                    setDirty(false)
                    assignDraft({
                      ...saved,
                      status: 'committed',
                      receipt: result,
                    })
                    recorder.cancel()
                    await Promise.allSettled([
                      queryClient.invalidateQueries({
                        queryKey: taskDomainQueryKeys.all,
                      }),
                      queryClient.invalidateQueries({
                        queryKey: workspaceQueryKeys.all,
                      }),
                      queryClient.invalidateQueries({
                        queryKey: ['planning', 'drafts'],
                      }),
                    ])
                  })
                }
              >
                {publish
                  ? `确认创建 ${selectedCount} 个任务`
                  : `保存为 ${selectedCount} 个任务草稿`}
              </Button>
            </div>
          </footer>
        ) : null}
        {draft && !readOnly ? (
          <div className="flex justify-end">
            <Button
              type="button"
              variant="ghost"
              disabled={!!busy || isJobActive}
              onClick={() =>
                void run('正在放弃草稿', async () => {
                  if (
                    !window.confirm(
                      '放弃这份草稿并删除录音？此操作不会影响已创建任务。'
                    )
                  )
                    return
                  await planning.deletePlanDraft(draft.id)
                  recorder.cancel()
                  closeCapture(false)
                  void queryClient.invalidateQueries({
                    queryKey: ['planning', 'drafts'],
                  })
                })
              }
            >
              放弃这份草稿
            </Button>
          </div>
        ) : null}
      </section>
    </Modal>
  )
}

function scheduleDraft(schedule: ScheduleV2Input): TaskScheduleDraft {
  const initial = createTaskScheduleDraft()
  const minutes = (schedule.local_start_time || initial.startTime)
    .split(':')
    .map(Number)
  const end = minutes[0] * 60 + minutes[1] + (schedule.duration_minutes || 30)
  return {
    recurrenceType: schedule.recurrence_type,
    timingType: schedule.timing_type,
    startsOn: schedule.starts_on || '',
    startTime: schedule.local_start_time?.slice(0, 5) || initial.startTime,
    endTime: `${String(Math.floor(end / 60) % 24).padStart(2, '0')}:${String(end % 60).padStart(2, '0')}`,
    weeklyDays: schedule.rule?.weekdays,
  }
}
function PlanItemEditor({
  item,
  index,
  onChange,
}: {
  item: planning.PlanItem
  index: number
  onChange: (patch: Partial<planning.PlanItem>) => void
}) {
  return (
    <fieldset className="plan-item">
      <legend>
        <label className="plan-check">
          <input
            aria-label={`选择任务 ${index + 1}`}
            type="checkbox"
            checked={item.selected}
            onChange={(event) => onChange({ selected: event.target.checked })}
          />
          任务 {index + 1}
        </label>
      </legend>
      <div className="plan-fields">
        <label className="plan-field">
          <span>任务标题</span>
          <Input
            aria-label={`任务 ${index + 1} 标题`}
            value={item.title}
            onChange={(event) => onChange({ title: event.target.value })}
          />
        </label>
        <label className="plan-field">
          <span>说明</span>
          <textarea
            aria-label={`任务 ${index + 1} 说明`}
            rows={2}
            value={item.description || ''}
            onChange={(event) => onChange({ description: event.target.value })}
          />
        </label>
        <TaskScheduleFields
          labelPrefix={`任务 ${index + 1}`}
          value={scheduleDraft(item.schedule)}
          onChange={(next) => {
            const schedule = buildTaskScheduleInput(
              next,
              item.schedule.timezone
            )
            if (
              schedule.recurrence_type === item.schedule.recurrence_type &&
              schedule.rule &&
              item.schedule.rule
            )
              schedule.rule.interval = item.schedule.rule.interval
            if (schedule.recurrence_type !== 'none' && item.schedule.ends_on)
              schedule.ends_on = item.schedule.ends_on
            onChange({ schedule })
          }}
        />
        {item.schedule.recurrence_type !== 'none' ? (
          <div className="plan-schedule-extra">
            <label className="plan-field">
              <span>重复间隔</span>
              <Input
                aria-label={`任务 ${index + 1} 重复间隔`}
                type="number"
                min={1}
                max={100}
                value={item.schedule.rule?.interval || 1}
                onChange={(event) =>
                  onChange({
                    schedule: {
                      ...item.schedule,
                      rule: {
                        ...item.schedule.rule,
                        interval: Number(event.target.value),
                      },
                    },
                  })
                }
              />
            </label>
            <label className="plan-field">
              <span>重复结束日期（可选）</span>
              <Input
                aria-label={`任务 ${index + 1} 重复结束日期`}
                type="date"
                value={item.schedule.ends_on || ''}
                onChange={(event) =>
                  onChange({
                    schedule: {
                      ...item.schedule,
                      ends_on: event.target.value || undefined,
                    },
                  })
                }
              />
            </label>
          </div>
        ) : null}
        {item.schedule.timing_type === 'unscheduled' ? (
          <p className="text-sm text-muted-foreground">
            未安排具体日期；暂不启用重复执行。
          </p>
        ) : null}
        <label className="plan-field">
          <span>截止时刻（含时区，可选）</span>
          <Input
            aria-label={`任务 ${index + 1} 截止时刻`}
            placeholder="2026-11-27T18:00:00+08:00"
            value={item.due_at || ''}
            onChange={(event) =>
              onChange({ due_at: event.target.value || undefined })
            }
          />
        </label>
      </div>
    </fieldset>
  )
}
