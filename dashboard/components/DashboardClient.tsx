'use client';

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useTransition,
  type ChangeEvent,
  type FormEvent,
} from 'react';
import { AnimatePresence, motion } from 'framer-motion';
import { useRouter, useSearchParams } from 'next/navigation';
import {
  createEnvironmentAction,
  createEnvironmentVersionAction,
  createProjectAction,
  createTeamAction,
  deleteDeploymentAction,
  triggerDeploymentAction,
} from '@/lib/actions/dashboard';
import { logout } from '@/lib/actions/auth';
import {
  LOGS_PAGE_SIZE,
  RUNTIME_EVENTS_PAGE_SIZE,
  RUNTIME_METRIC_BUCKET_SECONDS,
  RUNTIME_METRICS_LIMIT,
} from '@/lib/dashboard';
import type {
  Deployment,
  EnvironmentAudit,
  EnvironmentDetails,
  EnvironmentVersion,
  Project,
  ProjectLog,
  RuntimeEvent,
  RuntimeMetricRollup,
  Team,
} from '@/types';

interface DashboardClientProps {
  userEmail: string;
  teams: Team[];
  activeTeamId: string | null;
  projects: Project[];
  activeProjectId: string | null;
  project: Project | null;
  environments: EnvironmentDetails[];
  activeEnvironmentId: string | null;
  environmentVersions: EnvironmentVersion[];
  environmentAudits: EnvironmentAudit[];
  deployments: Deployment[];
  logs: ProjectLog[];
  logsHasMore: boolean;
  deploymentsHasMore: boolean;
  runtimeRollups: RuntimeMetricRollup[];
  runtimeEvents: RuntimeEvent[];
  runtimeEventsHasMore: boolean;
}

interface DashboardState {
  userEmail: string;
  teams: Team[];
  activeTeamId: string | null;
  projects: Project[];
  activeProjectId: string | null;
  project: Project | null;
  environments: EnvironmentDetails[];
  activeEnvironmentId: string | null;
  environmentVersions: EnvironmentVersion[];
  environmentAudits: EnvironmentAudit[];
  deployments: Deployment[];
  logs: ProjectLog[];
  logsHasMore: boolean;
  deploymentsHasMore: boolean;
  runtimeRollups: RuntimeMetricRollup[];
  runtimeEvents: RuntimeEvent[];
  runtimeEventsHasMore: boolean;
}

type ToastVariant = 'success' | 'error';

interface ToastState {
  id: number;
  message: string;
  variant: ToastVariant;
}

interface StreamLogPayload {
  project_id: string;
  source: string;
  level: string;
  message: string;
  metadata: unknown;
  created_at: string;
  id?: number;
}

interface RuntimeStreamPayload {
  id: number;
  project_id: string;
  source: string;
  event_type: string;
  level: string;
  message: string;
  method: string;
  path: string;
  status_code: number | null;
  latency_ms: number | null;
  bytes_in: number | null;
  bytes_out: number | null;
  metadata: unknown;
  occurred_at: string;
  ingested_at: string;
}

type LogStreamStatus = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error';

interface ReconnectState {
  attempts: number;
  delay: number;
  timeoutId: number | null;
}

class UnauthorizedError extends Error {
  constructor() {
    super('unauthorized');
    this.name = 'UnauthorizedError';
  }
}

async function fetchDashboardState(teamId: string | null, projectId: string | null, environmentId?: string | null): Promise<DashboardState> {
  const params = new URLSearchParams();
  if (teamId) {
    params.set('team', teamId);
  }
  if (projectId) {
    params.set('project', projectId);
  }
  if (environmentId) {
    params.set('environment', environmentId);
  }
  const response = await fetch(`/api/dashboard/state${params.size ? `?${params.toString()}` : ''}`, {
    method: 'GET',
    credentials: 'include',
    cache: 'no-store',
  });

  if (response.status === 401) {
    throw new UnauthorizedError();
  }

  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string } | null;
    const message = body?.error ?? 'Failed to load dashboard state.';
    throw new Error(message);
  }

  return (await response.json()) as DashboardState;
}

function parsePositiveInteger(value: string): number | undefined {
  const trimmed = value.trim();
  if (!trimmed) {
    return undefined;
  }
  const parsed = Number(trimmed);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return undefined;
  }
  return Math.floor(parsed);
}

function formatId(value: string | null | undefined, maxLength = 6): string {
  if (!value) {
    return '—';
  }
  return String(value).slice(0, maxLength);
}

const timestampFormatter = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: 'short',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
});

function formatTimestamp(value: string | null | undefined): string {
  if (!value) {
    return '—';
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return '—';
  }
  return timestampFormatter.format(parsed);
}

function buildSparklinePath(values: number[], width: number, height: number): string {
  if (!Array.isArray(values) || values.length === 0) {
    return '';
  }
  const max = Math.max(...values);
  const min = Math.min(...values);
  const range = max - min || 1;
  if (values.length === 1) {
    const singleY = height - ((values[0] - min) / range) * height;
    return `M0 ${singleY.toFixed(2)} L${width.toFixed(2)} ${singleY.toFixed(2)}`;
  }
  return values
    .map((value, index) => {
      const x = (index / (values.length - 1)) * width;
      const y = height - ((value - min) / range) * height;
      const command = index === 0 ? 'M' : 'L';
      return `${command}${x.toFixed(2)} ${y.toFixed(2)}`;
    })
    .join(' ');
}

function formatLatencyValue(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
    return '—';
  }
  if (value >= 100) {
    return `${Math.round(value)} ms`;
  }
  if (value >= 10) {
    return `${value.toFixed(1)} ms`;
  }
  return `${value.toFixed(2)} ms`;
}

function formatRatePerSecond(count: number, bucketSpanSeconds: number): string {
  if (!Number.isFinite(count) || count < 0 || bucketSpanSeconds <= 0) {
    return '—';
  }
  const perSecond = count / bucketSpanSeconds;
  if (perSecond >= 1) {
    return `${perSecond.toFixed(1)}/s`;
  }
  const perMinute = perSecond * 60;
  return `${perMinute.toFixed(1)}/min`;
}

function formatPercent(value: number): string {
  if (!Number.isFinite(value)) {
    return '—';
  }
  if (value >= 1) {
    return `${value.toFixed(1)}%`;
  }
  return `${value.toFixed(2)}%`;
}

function streamStatusBadgeClass(status: LogStreamStatus): string {
  const base = 'badge text-[11px] uppercase tracking-wide';
  switch (status) {
    case 'connected':
      return `${base} border-emerald-500/60 bg-emerald-500/15 text-emerald-200`;
    case 'connecting':
      return `${base} border-cyan-500/60 bg-cyan-500/15 text-cyan-200`;
    case 'reconnecting':
      return `${base} border-amber-500/60 bg-amber-500/15 text-amber-200`;
    case 'error':
      return `${base} border-rose-500/60 bg-rose-500/15 text-rose-200`;
    default:
      return `${base} border-slate-500/40 bg-slate-500/10 text-slate-200`;
  }
}

const STREAM_RETRY_MIN_DELAY_MS = 2000;
const STREAM_RETRY_MAX_DELAY_MS = 30000;
const STREAM_RETRY_ERROR_THRESHOLD = 5;
const DEFAULT_BUILD_COMMAND = 'docker build -f Dockerfile .';
const DEFAULT_RUN_COMMAND = 'docker compose up --build';

function normalizeStreamMetadata(metadata: unknown): Record<string, unknown> | null {
  if (!metadata) {
    return null;
  }
  if (typeof metadata === 'object' && metadata !== null) {
    return metadata as Record<string, unknown>;
  }
  if (typeof metadata === 'string') {
    try {
      const parsed = JSON.parse(metadata) as unknown;
      if (typeof parsed === 'object' && parsed !== null) {
        return parsed as Record<string, unknown>;
      }
    } catch {
      return null;
    }
  }
  return null;
}

function deploymentBadgeClass(status: string): string {
  const base = 'badge inline-flex items-center rounded-lg border px-2 py-1 text-[11px] font-semibold uppercase tracking-wide';
  const normalized = status ? status.toLowerCase() : '';
  switch (normalized) {
    case 'success':
    case 'completed':
    case 'ready':
      return `${base} border-emerald-500/50 bg-emerald-500/15 text-emerald-200`;
    case 'error':
    case 'failed':
    case 'canceled':
      return `${base} border-rose-500/50 bg-rose-500/15 text-rose-200`;
    case 'building':
    case 'pending':
    case 'queued':
      return `${base} border-amber-500/40 bg-amber-500/15 text-amber-200`;
    default:
      return `${base} border-slate-500/40 bg-slate-500/10 text-slate-200`;
  }
}

export function DashboardClient(props: DashboardClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();

  const [navigationPending, startNavigation] = useTransition();
  const [refreshTransitionPending, startRefreshTransition] = useTransition();
  const [logoutPending, startLogout] = useTransition();
  const [teamPending, startTeamAction] = useTransition();
  const [projectPending, startProjectAction] = useTransition();
  const [environmentPending, startEnvironmentAction] = useTransition();
  const [versionPending, startVersionAction] = useTransition();
  const [deployPending, startDeployAction] = useTransition();
  const [deletePending, startDeleteAction] = useTransition();
  const runtimeBucketOptions = [60, 300, 900];
  const runtimeLevelOptions = ['debug', 'info', 'warn', 'error'] as const;
  const environmentTypeOptions = ['production', 'staging', 'development', 'preview', 'custom'] as const;

  const [state, setState] = useState<DashboardState>(() => ({
    userEmail: props.userEmail,
    teams: props.teams,
    activeTeamId: props.activeTeamId,
    projects: props.projects,
    activeProjectId: props.activeProjectId,
    project: props.project,
    environments: props.environments,
    activeEnvironmentId: props.activeEnvironmentId,
    environmentVersions: props.environmentVersions,
    environmentAudits: props.environmentAudits,
    deployments: props.deployments,
    logs: props.logs,
    logsHasMore: props.logsHasMore,
    deploymentsHasMore: props.deploymentsHasMore,
    runtimeRollups: props.runtimeRollups,
    runtimeEvents: props.runtimeEvents,
    runtimeEventsHasMore: props.runtimeEventsHasMore,
  }));
  const [dataPending, setDataPending] = useState(false);
  const [toast, setToast] = useState<ToastState | null>(null);
  const [teamFormOpen, setTeamFormOpen] = useState(false);
  const [teamName, setTeamName] = useState('');
  const [maxProjects, setMaxProjects] = useState('');
  const [maxContainers, setMaxContainers] = useState('');
  const [storageLimit, setStorageLimit] = useState('');
  const [teamFormError, setTeamFormError] = useState<string | null>(null);
  const [projectFormOpen, setProjectFormOpen] = useState(false);
  const [projectName, setProjectName] = useState('');
  const [projectRepo, setProjectRepo] = useState('');
  const [projectType, setProjectType] = useState<'frontend' | 'backend'>('frontend');
  const [projectBuildCommand, setProjectBuildCommand] = useState(DEFAULT_BUILD_COMMAND);
  const [projectRunCommand, setProjectRunCommand] = useState(DEFAULT_RUN_COMMAND);
  const [projectFormError, setProjectFormError] = useState<string | null>(null);
  const [environmentFormOpen, setEnvironmentFormOpen] = useState(false);
  const [environmentName, setEnvironmentName] = useState('');
  const [environmentSlug, setEnvironmentSlug] = useState('');
  const [environmentType, setEnvironmentType] = useState<'production' | 'staging' | 'development' | 'preview' | 'custom'>('development');
  const [environmentProtected, setEnvironmentProtected] = useState(false);
  const [environmentPosition, setEnvironmentPosition] = useState('');
  const [environmentFormError, setEnvironmentFormError] = useState<string | null>(null);
  const [versionEditor, setVersionEditor] = useState('');
  const [versionDescription, setVersionDescription] = useState('');
  const [versionError, setVersionError] = useState<string | null>(null);
  const [deployCommit, setDeployCommit] = useState('');
  const [deployError, setDeployError] = useState<string | null>(null);
  const [deletingDeploymentId, setDeletingDeploymentId] = useState<string | null>(null);
  const [logsLoading, setLogsLoading] = useState(false);
  const [logsError, setLogsError] = useState<string | null>(null);
  const [logStreamStatus, setLogStreamStatus] = useState<LogStreamStatus>('idle');
  const [insightTab, setInsightTab] = useState<'runtime' | 'build'>('runtime');
  const [runtimeSearchTerm, setRuntimeSearchTerm] = useState('');
  const [runtimeLevelFilter, setRuntimeLevelFilter] = useState<'all' | 'debug' | 'info' | 'warn' | 'error'>('all');
  const [runtimeMethodFilter, setRuntimeMethodFilter] = useState<'all' | string>('all');
  const [runtimeSourceFilter, setRuntimeSourceFilter] = useState<'all' | string>('all');
  const [runtimeEventTypeFilter, setRuntimeEventTypeFilter] = useState<'all' | string>('all');
  const [runtimeEventsLoading, setRuntimeEventsLoading] = useState(false);
  const [runtimeEventsError, setRuntimeEventsError] = useState<string | null>(null);
  const [runtimeStreamStatus, setRuntimeStreamStatus] = useState<LogStreamStatus>('idle');
  const [runtimeMetricsLoading, setRuntimeMetricsLoading] = useState(false);
  const [runtimeMetricsError, setRuntimeMetricsError] = useState<string | null>(null);
  const [runtimeBucketSpan, setRuntimeBucketSpan] = useState(RUNTIME_METRIC_BUCKET_SECONDS);

  const logStreamRef = useRef<EventSource | null>(null);
  const streamSequenceRef = useRef(0);
  const reconnectStateRef = useRef<ReconnectState>({
    attempts: 0,
    delay: STREAM_RETRY_MIN_DELAY_MS,
    timeoutId: null,
  });
  const prevStreamStatusRef = useRef<LogStreamStatus>('idle');
  const runtimeStreamRef = useRef<EventSource | null>(null);
  const runtimeStreamSequenceRef = useRef(0);
  const runtimeReconnectStateRef = useRef<ReconnectState>({
    attempts: 0,
    delay: STREAM_RETRY_MIN_DELAY_MS,
    timeoutId: null,
  });
  const prevRuntimeStreamStatusRef = useRef<LogStreamStatus>('idle');
  const runtimeMetricsRefreshTimerRef = useRef<number | null>(null);
  const runtimeBucketSpanRef = useRef(runtimeBucketSpan);
  const runtimeEventTypeFilterRef = useRef(runtimeEventTypeFilter);

  const loadRuntimeMetrics = useCallback(
    async (projectId: string, bucketSpanSeconds: number, eventType: string | 'all') => {
      if (!projectId) {
        return;
      }

      setRuntimeMetricsError(null);
      setRuntimeMetricsLoading(true);

      try {
        const params = new URLSearchParams({
          project: projectId,
          bucketSpan: String(bucketSpanSeconds),
          limit: String(RUNTIME_METRICS_LIMIT),
        });
        if (eventType && eventType !== 'all') {
          params.set('eventType', eventType);
        }

        const response = await fetch(`/api/dashboard/runtime/metrics?${params.toString()}`, {
          method: 'GET',
          credentials: 'include',
          cache: 'no-store',
        });

        if (response.status === 401) {
          router.refresh();
          return;
        }

        if (!response.ok) {
          const body = (await response.json().catch(() => null)) as { error?: string } | null;
          const message = body?.error ?? 'Unable to refresh runtime metrics.';
          setRuntimeMetricsError(message);
          return;
        }

        const payload = (await response.json()) as { rollups: RuntimeMetricRollup[] | undefined };
        const rollups = Array.isArray(payload.rollups) ? payload.rollups : [];
        setState((prev) => {
          if (prev.activeProjectId !== projectId) {
            return prev;
          }
          return {
            ...prev,
            runtimeRollups: rollups.slice(0, RUNTIME_METRICS_LIMIT),
          };
        });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unable to refresh runtime metrics.';
        setRuntimeMetricsError(message);
      } finally {
        setRuntimeMetricsLoading(false);
      }
    },
    [router],
  );

  const queueRuntimeMetricsRefresh = useCallback((projectId: string) => {
    if (typeof window === 'undefined') {
      return;
    }
    if (!projectId) {
      return;
    }
    if (runtimeMetricsRefreshTimerRef.current !== null) {
      return;
    }

    runtimeMetricsRefreshTimerRef.current = window.setTimeout(() => {
      runtimeMetricsRefreshTimerRef.current = null;
      if (!projectId) {
        return;
      }
      const bucketSpan = runtimeBucketSpanRef.current;
      const eventType = runtimeEventTypeFilterRef.current;
      void loadRuntimeMetrics(projectId, bucketSpan, eventType);
    }, 1500);
  }, [loadRuntimeMetrics]);

  useEffect(() => {
    setState({
      userEmail: props.userEmail,
      teams: props.teams,
      activeTeamId: props.activeTeamId,
      projects: props.projects,
      activeProjectId: props.activeProjectId,
      project: props.project,
      environments: props.environments,
      activeEnvironmentId: props.activeEnvironmentId,
      environmentVersions: props.environmentVersions,
      environmentAudits: props.environmentAudits,
      deployments: props.deployments,
      logs: props.logs,
      logsHasMore: props.logsHasMore,
      deploymentsHasMore: props.deploymentsHasMore,
      runtimeRollups: props.runtimeRollups,
      runtimeEvents: props.runtimeEvents,
      runtimeEventsHasMore: props.runtimeEventsHasMore,
    });
  }, [
    props.userEmail,
    props.teams,
    props.activeTeamId,
    props.projects,
    props.activeProjectId,
    props.project,
    props.environments,
    props.activeEnvironmentId,
    props.environmentVersions,
    props.environmentAudits,
    props.deployments,
    props.logs,
    props.logsHasMore,
    props.deploymentsHasMore,
    props.runtimeRollups,
    props.runtimeEvents,
    props.runtimeEventsHasMore,
  ]);

  useEffect(() => {
    runtimeBucketSpanRef.current = runtimeBucketSpan;
  }, [runtimeBucketSpan]);

  useEffect(() => {
    runtimeEventTypeFilterRef.current = runtimeEventTypeFilter;
  }, [runtimeEventTypeFilter]);

  useEffect(() => {
    if (!toast) {
      return;
    }
    const timeoutId = window.setTimeout(() => setToast(null), 4000);
    return () => window.clearTimeout(timeoutId);
  }, [toast]);

  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }

    const reconnectState = reconnectStateRef.current;
    let cancelled = false;

    const clearTimer = () => {
      if (reconnectState.timeoutId !== null) {
        window.clearTimeout(reconnectState.timeoutId);
        reconnectState.timeoutId = null;
      }
    };

    const closeSource = () => {
      if (logStreamRef.current) {
        logStreamRef.current.close();
        logStreamRef.current = null;
      }
    };

    if (!state.activeProjectId) {
      clearTimer();
      closeSource();
      setLogStreamStatus('idle');
      return () => {
        cancelled = true;
      };
    }

    const projectId = state.activeProjectId;
    const streamUrl = `/api/dashboard/logs/stream?project=${encodeURIComponent(projectId)}`;

    reconnectState.attempts = 0;
    reconnectState.delay = STREAM_RETRY_MIN_DELAY_MS;
    clearTimer();
    closeSource();
    setLogsError(null);
    setLogStreamStatus('connecting');

    const connect = () => {
      if (cancelled) {
        return;
      }

      clearTimer();
      const source = new EventSource(streamUrl, { withCredentials: true });
      logStreamRef.current = source;
      streamSequenceRef.current = 0;

      source.onopen = () => {
        if (cancelled) {
          return;
        }
        reconnectState.attempts = 0;
        reconnectState.delay = STREAM_RETRY_MIN_DELAY_MS;
        setLogsError(null);
        setLogStreamStatus('connected');
      };

      source.onmessage = (event: MessageEvent<string>) => {
        try {
          const payload = JSON.parse(event.data) as StreamLogPayload;
          if (!payload || payload.project_id !== projectId) {
            return;
          }

          const metadata = normalizeStreamMetadata(payload.metadata);
          const nextId =
            typeof payload.id === 'number'
              ? payload.id
              : -Math.abs(Date.now() + ++streamSequenceRef.current);
          const entry: ProjectLog = {
            id: nextId,
            project_id: payload.project_id,
            source: payload.source,
            level: payload.level,
            message: payload.message,
            metadata,
            created_at: payload.created_at,
          };

          setState((prev) => {
            if (prev.activeProjectId !== projectId) {
              return prev;
            }
            const duplicate = prev.logs.some(
              (log) =>
                log.created_at === entry.created_at &&
                log.message === entry.message &&
                log.source === entry.source,
            );
            if (duplicate) {
              return prev;
            }
            const nextLogs = [entry, ...prev.logs];
            const maxEntries = LOGS_PAGE_SIZE * 4;
            if (nextLogs.length > maxEntries) {
              nextLogs.length = maxEntries;
            }
            return {
              ...prev,
              logs: nextLogs,
            };
          });
        } catch (error) {
          console.error('Failed to parse log stream payload', error);
        }
      };

      source.onerror = () => {
        if (cancelled) {
          return;
        }
        source.close();
        if (logStreamRef.current === source) {
          logStreamRef.current = null;
        }
        reconnectState.attempts += 1;
        const exceeded = reconnectState.attempts >= STREAM_RETRY_ERROR_THRESHOLD;
        setLogStreamStatus(exceeded ? 'error' : 'reconnecting');
        setLogsError((prev) => prev ?? 'Live log stream disrupted. Retrying…');
        if (reconnectState.timeoutId === null) {
          const jitter = reconnectState.delay + Math.random() * (reconnectState.delay * 0.25);
          reconnectState.timeoutId = window.setTimeout(() => {
            reconnectState.timeoutId = null;
            reconnectState.delay = Math.min(reconnectState.delay * 2, STREAM_RETRY_MAX_DELAY_MS);
            connect();
          }, jitter);
        }
      };
    };

    connect();

    return () => {
      cancelled = true;
      clearTimer();
      closeSource();
    };
  }, [state.activeProjectId]);

  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }

    const reconnectState = runtimeReconnectStateRef.current;
    let cancelled = false;

    const clearTimer = () => {
      if (reconnectState.timeoutId !== null) {
        window.clearTimeout(reconnectState.timeoutId);
        reconnectState.timeoutId = null;
      }
    };

    const closeSource = () => {
      if (runtimeStreamRef.current) {
        runtimeStreamRef.current.close();
        runtimeStreamRef.current = null;
      }
    };

    if (!state.activeProjectId) {
      clearTimer();
      closeSource();
      setRuntimeStreamStatus('idle');
      setRuntimeEventsError(null);
      return () => {
        cancelled = true;
      };
    }

    const projectId = state.activeProjectId;
    const streamUrl = `/api/dashboard/runtime/stream?project=${encodeURIComponent(projectId)}`;

    reconnectState.attempts = 0;
    reconnectState.delay = STREAM_RETRY_MIN_DELAY_MS;
    clearTimer();
    closeSource();
    setRuntimeEventsError(null);
    setRuntimeStreamStatus('connecting');

    const connect = () => {
      if (cancelled) {
        return;
      }

      clearTimer();
      const source = new EventSource(streamUrl, { withCredentials: true });
      runtimeStreamRef.current = source;
      runtimeStreamSequenceRef.current = 0;

      source.onopen = () => {
        if (cancelled) {
          return;
        }
        reconnectState.attempts = 0;
        reconnectState.delay = STREAM_RETRY_MIN_DELAY_MS;
        setRuntimeEventsError(null);
        setRuntimeStreamStatus('connected');
      };

      source.onmessage = (event: MessageEvent<string>) => {
        try {
          const payload = JSON.parse(event.data) as RuntimeStreamPayload;
          if (!payload || payload.project_id !== projectId) {
            return;
          }
          const metadata = normalizeStreamMetadata(payload.metadata);
          const nextId =
            typeof payload.id === 'number'
              ? payload.id
              : -Math.abs(Date.now() + ++runtimeStreamSequenceRef.current);
          const entry: RuntimeEvent = {
            id: nextId,
            project_id: payload.project_id,
            source: payload.source,
            event_type: payload.event_type,
            level: payload.level || 'info',
            message: payload.message,
            method: payload.method,
            path: payload.path,
            status_code: payload.status_code,
            latency_ms: payload.latency_ms,
            bytes_in: payload.bytes_in,
            bytes_out: payload.bytes_out,
            metadata,
            occurred_at: payload.occurred_at,
            ingested_at: payload.ingested_at,
          };

          setState((prev) => {
            if (prev.activeProjectId !== projectId) {
              return prev;
            }
            const duplicate = prev.runtimeEvents.some((event) => event.id === entry.id);
            if (duplicate) {
              return prev;
            }
            const nextEvents = [entry, ...prev.runtimeEvents];
            const maxEntries = RUNTIME_EVENTS_PAGE_SIZE * 4;
            if (nextEvents.length > maxEntries) {
              nextEvents.length = maxEntries;
            }
            return {
              ...prev,
              runtimeEvents: nextEvents,
            };
          });

          queueRuntimeMetricsRefresh(projectId);
        } catch (error) {
          console.error('Failed to parse runtime event stream payload', error);
        }
      };

      source.onerror = () => {
        if (cancelled) {
          return;
        }
        source.close();
        if (runtimeStreamRef.current === source) {
          runtimeStreamRef.current = null;
        }
        reconnectState.attempts += 1;
        const exceeded = reconnectState.attempts >= STREAM_RETRY_ERROR_THRESHOLD;
        setRuntimeStreamStatus(exceeded ? 'error' : 'reconnecting');
        setRuntimeEventsError((prev) => prev ?? 'Live runtime stream disrupted. Retrying…');
        if (reconnectState.timeoutId === null) {
          const jitter = reconnectState.delay + Math.random() * (reconnectState.delay * 0.25);
          reconnectState.timeoutId = window.setTimeout(() => {
            reconnectState.timeoutId = null;
            reconnectState.delay = Math.min(reconnectState.delay * 2, STREAM_RETRY_MAX_DELAY_MS);
            connect();
          }, jitter);
        }
      };
    };

    connect();

    return () => {
      cancelled = true;
      clearTimer();
      closeSource();
    };
  }, [state.activeProjectId, queueRuntimeMetricsRefresh]);

  useEffect(() => {
    const previous = prevStreamStatusRef.current;
    if (previous !== logStreamStatus) {
      if (logStreamStatus === 'connected' && (previous === 'reconnecting' || previous === 'error')) {
        showToast('Live log stream reconnected.', 'success');
      } else if (logStreamStatus === 'reconnecting') {
        showToast('Live log stream interrupted. Retrying…', 'error');
      } else if (logStreamStatus === 'error') {
        showToast('Live log stream unavailable. Attempts will continue in the background.', 'error');
      }
      prevStreamStatusRef.current = logStreamStatus;
    }
  }, [logStreamStatus]);

  useEffect(() => {
    const previous = prevRuntimeStreamStatusRef.current;
    if (previous !== runtimeStreamStatus) {
      if (runtimeStreamStatus === 'connected' && (previous === 'reconnecting' || previous === 'error')) {
        showToast('Runtime event stream reconnected.', 'success');
      } else if (runtimeStreamStatus === 'reconnecting') {
        showToast('Runtime event stream interrupted. Retrying…', 'error');
      } else if (runtimeStreamStatus === 'error') {
        showToast('Runtime event stream unavailable. Attempts will continue in the background.', 'error');
      }
      prevRuntimeStreamStatusRef.current = runtimeStreamStatus;
    }
  }, [runtimeStreamStatus]);

  useEffect(() => {
    return () => {
      if (typeof window !== 'undefined' && runtimeMetricsRefreshTimerRef.current !== null) {
        window.clearTimeout(runtimeMetricsRefreshTimerRef.current);
        runtimeMetricsRefreshTimerRef.current = null;
      }
    };
  }, []);

  const selectedTeam = useMemo(() => {
    if (!state.activeTeamId) {
      return null;
    }
    return state.teams.find((team) => team.id === state.activeTeamId) ?? null;
  }, [state.activeTeamId, state.teams]);

  const environmentsSorted = useMemo(() => {
    return [...state.environments].sort((a, b) => a.environment.position - b.environment.position);
  }, [state.environments]);

  const activeEnvironment = useMemo(() => {
    if (!environmentsSorted.length) {
      return null;
    }
    if (state.activeEnvironmentId) {
      return (
        environmentsSorted.find((candidate) => candidate.environment.id === state.activeEnvironmentId) ??
        environmentsSorted[0]
      );
    }
    return environmentsSorted[0];
  }, [environmentsSorted, state.activeEnvironmentId]);

  const latestEnvironmentVersion = useMemo(() => {
    return activeEnvironment?.latest_version ?? null;
  }, [activeEnvironment]);

  const latestEnvironmentVariables = useMemo(() => {
    return latestEnvironmentVersion?.variables ?? [];
  }, [latestEnvironmentVersion]);

  useEffect(() => {
    if (!activeEnvironment || !latestEnvironmentVersion) {
      setVersionEditor('');
      return;
    }
    if (!latestEnvironmentVariables.length) {
      setVersionEditor('');
      return;
    }
    const serialized = latestEnvironmentVariables.map((variable) => `${variable.key}=${variable.value}`).join('\n');
    setVersionEditor(serialized);
  }, [activeEnvironment, latestEnvironmentVersion, latestEnvironmentVariables]);

  const environmentVersionsSorted = useMemo(() => {
    return [...state.environmentVersions].sort((a, b) => b.version - a.version);
  }, [state.environmentVersions]);

  const runtimeEventTypes = useMemo(() => {
    const values = new Set<string>();
    state.runtimeEvents.forEach((event) => {
      if (event.event_type) {
        values.add(event.event_type);
      }
    });
    return Array.from(values).sort((a, b) => a.localeCompare(b));
  }, [state.runtimeEvents]);

  const runtimeMethodOptions = useMemo(() => {
    const values = new Set<string>();
    state.runtimeEvents.forEach((event) => {
      const method = (event.method || '').toUpperCase();
      if (method) {
        values.add(method);
      }
    });
    return Array.from(values).sort((a, b) => a.localeCompare(b));
  }, [state.runtimeEvents]);

  const runtimeSourceOptions = useMemo(() => {
    const values = new Set<string>();
    state.runtimeEvents.forEach((event) => {
      if (event.source) {
        values.add(event.source);
      }
    });
    return Array.from(values).sort((a, b) => a.localeCompare(b));
  }, [state.runtimeEvents]);

  const runtimeRollupsSorted = useMemo(() => {
    return [...state.runtimeRollups].sort((a, b) => {
      const aTime = new Date(a.bucket_start).getTime();
      const bTime = new Date(b.bucket_start).getTime();
      return aTime - bTime;
    });
  }, [state.runtimeRollups]);

  const runtimeLatencySeries = useMemo(() => {
    if (runtimeRollupsSorted.length === 0) {
      return [] as number[];
    }
    return runtimeRollupsSorted.map((rollup) => {
      const candidates = [rollup.p95_ms, rollup.p90_ms, rollup.p50_ms];
      const value = candidates.find((candidate) => typeof candidate === 'number' && Number.isFinite(candidate));
      return typeof value === 'number' ? value : 0;
    });
  }, [runtimeRollupsSorted]);

  const runtimeThroughputSeries = useMemo(() => runtimeRollupsSorted.map((rollup) => rollup.count), [runtimeRollupsSorted]);

  const latencySparklinePath = useMemo(
    () => buildSparklinePath(runtimeLatencySeries, 160, 48),
    [runtimeLatencySeries],
  );

  const throughputSparklinePath = useMemo(
    () => buildSparklinePath(runtimeThroughputSeries, 160, 48),
    [runtimeThroughputSeries],
  );

  const runtimeSummary = useMemo(() => {
    if (runtimeRollupsSorted.length === 0) {
      return null;
    }
    let totalCount = 0;
    let totalErrors = 0;
    runtimeRollupsSorted.forEach((rollup) => {
      totalCount += rollup.count;
      totalErrors += rollup.error_count;
    });
    const latest = runtimeRollupsSorted[runtimeRollupsSorted.length - 1];
    const errorRate = totalCount > 0 ? (totalErrors / totalCount) * 100 : 0;
    return {
      totalCount,
      totalErrors,
      errorRate,
      latest,
    };
  }, [runtimeRollupsSorted]);

  const filteredRuntimeEvents = useMemo(() => {
    const search = runtimeSearchTerm.trim().toLowerCase();
    return state.runtimeEvents.filter((event) => {
      if (runtimeEventTypeFilter !== 'all' && event.event_type !== runtimeEventTypeFilter) {
        return false;
      }
      if (runtimeLevelFilter !== 'all' && (event.level || '').toLowerCase() !== runtimeLevelFilter) {
        return false;
      }
      if (runtimeMethodFilter !== 'all' && (event.method || '').toUpperCase() !== runtimeMethodFilter) {
        return false;
      }
      if (runtimeSourceFilter !== 'all' && event.source !== runtimeSourceFilter) {
        return false;
      }
      if (search) {
        const metadataText = event.metadata ? JSON.stringify(event.metadata).toLowerCase() : '';
        const haystack = `${event.message ?? ''} ${event.path ?? ''} ${metadataText}`.toLowerCase();
        if (!haystack.includes(search)) {
          return false;
        }
      }
      return true;
    });
  }, [
    state.runtimeEvents,
    runtimeEventTypeFilter,
    runtimeLevelFilter,
    runtimeMethodFilter,
    runtimeSourceFilter,
    runtimeSearchTerm,
  ]);

  function showToast(message: string, variant: ToastVariant) {
    setToast({ id: Date.now(), message, variant });
  }

  const handleRuntimeBucketSpanSelect = useCallback(
    (value: number) => {
      setRuntimeBucketSpan(value);
      if (!state.activeProjectId) {
        return;
      }
      void loadRuntimeMetrics(state.activeProjectId, value, runtimeEventTypeFilter);
    },
    [state.activeProjectId, runtimeEventTypeFilter, loadRuntimeMetrics],
  );

  const handleRuntimeEventTypeFilterSelect = useCallback(
    (value: string | 'all') => {
      setRuntimeEventTypeFilter(value);
      if (!state.activeProjectId) {
        return;
      }
      void loadRuntimeMetrics(state.activeProjectId, runtimeBucketSpan, value);
    },
    [state.activeProjectId, runtimeBucketSpan, loadRuntimeMetrics],
  );

  const handleManualRuntimeMetricsRefresh = useCallback(() => {
    if (!state.activeProjectId) {
      return;
    }
    void loadRuntimeMetrics(state.activeProjectId, runtimeBucketSpan, runtimeEventTypeFilter);
  }, [state.activeProjectId, runtimeBucketSpan, runtimeEventTypeFilter, loadRuntimeMetrics]);

  async function reloadState(
    nextTeamId: string | null,
    nextProjectId: string | null,
    successMessage?: string,
    nextEnvironmentId?: string | null,
  ) {
    setDataPending(true);
    try {
      const nextState = await fetchDashboardState(nextTeamId, nextProjectId, nextEnvironmentId);
      setState(nextState);
      setRuntimeMetricsError(null);
      setRuntimeEventsError(null);
      if (successMessage) {
        showToast(successMessage, 'success');
      }
    } catch (error) {
      if (error instanceof UnauthorizedError) {
        router.refresh();
        return;
      }
      const message = error instanceof Error ? error.message : 'Unable to update dashboard state.';
      showToast(message, 'error');
    } finally {
      setDataPending(false);
    }
  }

  function updateSearchParams(next: Record<string, string | null>) {
    const current = new URLSearchParams(searchParams.toString());
    Object.entries(next).forEach(([key, value]) => {
      if (value === null || value === '') {
        current.delete(key);
      } else {
        current.set(key, value);
      }
    });
    const query = current.toString();
    const href = query ? `/?${query}` : '/';
    router.push(href);
  }

  function handleSelectTeam(teamId: string) {
    if (teamId === state.activeTeamId) {
      return;
    }
    setState((prev) => ({
      ...prev,
      activeTeamId: teamId,
      activeProjectId: null,
      project: null,
      environments: [],
      activeEnvironmentId: null,
      environmentVersions: [],
      environmentAudits: [],
      deployments: [],
      logs: [],
      logsHasMore: false,
      deploymentsHasMore: false,
      runtimeRollups: [],
      runtimeEvents: [],
      runtimeEventsHasMore: false,
    }));
    setRuntimeEventsError(null);
    setRuntimeMetricsError(null);
    setRuntimeSearchTerm('');
    setRuntimeLevelFilter('all');
    setRuntimeMethodFilter('all');
    setRuntimeSourceFilter('all');
    setRuntimeEventTypeFilter('all');
    setInsightTab('runtime');
    if (typeof window !== 'undefined' && runtimeMetricsRefreshTimerRef.current !== null) {
      window.clearTimeout(runtimeMetricsRefreshTimerRef.current);
      runtimeMetricsRefreshTimerRef.current = null;
    }
    startNavigation(() => {
      updateSearchParams({ team: teamId, project: null, environment: null });
    });
    void reloadState(teamId, null, undefined, null);
  }

  function handleSelectProject(projectId: string) {
    if (projectId === state.activeProjectId) {
      return;
    }
    startNavigation(() => {
      updateSearchParams({ team: state.activeTeamId ?? null, project: projectId, environment: null });
    });
    setState((prev) => ({
      ...prev,
      activeProjectId: projectId,
      project: prev.projects.find((project) => project.id === projectId) ?? prev.project,
      environments: [],
      activeEnvironmentId: null,
      environmentVersions: [],
      environmentAudits: [],
      deployments: [],
      logs: [],
      logsHasMore: false,
      deploymentsHasMore: false,
      runtimeRollups: [],
      runtimeEvents: [],
      runtimeEventsHasMore: false,
    }));
    setRuntimeEventsError(null);
    setRuntimeMetricsError(null);
    setRuntimeSearchTerm('');
    setRuntimeLevelFilter('all');
    setRuntimeMethodFilter('all');
    setRuntimeSourceFilter('all');
    setRuntimeEventTypeFilter('all');
    setInsightTab('runtime');
    if (typeof window !== 'undefined' && runtimeMetricsRefreshTimerRef.current !== null) {
      window.clearTimeout(runtimeMetricsRefreshTimerRef.current);
      runtimeMetricsRefreshTimerRef.current = null;
    }
    void reloadState(state.activeTeamId, projectId, undefined, null);
  }

  function handleRefresh() {
    startRefreshTransition(() => {
      void reloadState(state.activeTeamId, state.activeProjectId, 'Dashboard data refreshed.', state.activeEnvironmentId);
    });
  }

  function handleLogout() {
    startLogout(async () => {
      await logout();
      router.refresh();
    });
  }

  function handleCreateTeam(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nameTrimmed = teamName.trim();
    if (!nameTrimmed) {
      setTeamFormError('Team name is required.');
      return;
    }
    setTeamFormError(null);
    const limits = {
      maxProjects: parsePositiveInteger(maxProjects),
      maxContainers: parsePositiveInteger(maxContainers),
      storageLimitMb: parsePositiveInteger(storageLimit),
    };
    startTeamAction(async () => {
      const result = await createTeamAction({
        name: nameTrimmed,
        maxProjects: limits.maxProjects,
        maxContainers: limits.maxContainers,
        storageLimitMb: limits.storageLimitMb,
      });
      if (!result.success) {
        setTeamFormError(result.error ?? 'Failed to create team.');
        return;
      }
      setTeamName('');
      setMaxProjects('');
      setMaxContainers('');
      setStorageLimit('');
      setTeamFormOpen(false);
      await reloadState(null, null, 'Team created.');
    });
  }

  function handleCreateProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!state.activeTeamId) {
      setProjectFormError('Select a team before creating a project.');
      return;
    }
    const nameTrimmed = projectName.trim();
    const repoTrimmed = projectRepo.trim();
    const buildTrimmed = projectBuildCommand.trim();
    const runTrimmed = projectRunCommand.trim();
    if (!nameTrimmed) {
      setProjectFormError('Project name is required.');
      return;
    }
    if (!repoTrimmed) {
      setProjectFormError('Repository URL is required.');
      return;
    }
    if (!buildTrimmed || !runTrimmed) {
      setProjectFormError('Build and run commands are required.');
      return;
    }
    setProjectFormError(null);
    startProjectAction(async () => {
      const result = await createProjectAction({
        teamId: state.activeTeamId!,
        name: nameTrimmed,
        repoUrl: repoTrimmed,
        type: projectType,
        buildCommand: buildTrimmed,
        runCommand: runTrimmed,
      });
      if (!result.success) {
        setProjectFormError(result.error ?? 'Failed to create project.');
        return;
      }
      setProjectName('');
      setProjectRepo('');
      setProjectBuildCommand(DEFAULT_BUILD_COMMAND);
      setProjectRunCommand(DEFAULT_RUN_COMMAND);
      setProjectFormOpen(false);
      await reloadState(state.activeTeamId, null, 'Project provisioned.');
    });
  }

  function handleCreateEnvironment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!state.activeProjectId) {
      setEnvironmentFormError('Select a project first.');
      return;
    }

    const nameTrimmed = environmentName.trim();
    if (!nameTrimmed) {
      setEnvironmentFormError('Environment name is required.');
      return;
    }

    const slugTrimmed = environmentSlug.trim();
    const positionTrimmed = environmentPosition.trim();
    let positionValue: number | undefined;
    if (positionTrimmed) {
      const parsed = Number(positionTrimmed);
      if (!Number.isFinite(parsed) || parsed <= 0) {
        setEnvironmentFormError('Position must be a positive number.');
        return;
      }
      positionValue = Math.floor(parsed);
    }

    setEnvironmentFormError(null);
    startEnvironmentAction(async () => {
      const result = await createEnvironmentAction({
        projectId: state.activeProjectId!,
        name: nameTrimmed,
        slug: slugTrimmed || undefined,
        type: environmentType,
        protected: environmentProtected,
        position: positionValue,
      });
      if (!result.success) {
        setEnvironmentFormError(result.error ?? 'Failed to create environment.');
        return;
      }
      setEnvironmentName('');
      setEnvironmentSlug('');
      setEnvironmentType('development');
      setEnvironmentProtected(false);
      setEnvironmentPosition('');
      setEnvironmentFormOpen(false);
      await reloadState(
        state.activeTeamId,
        state.activeProjectId,
        'Environment created.',
        result.environmentId ?? null,
      );
    });
  }

  function handleSelectEnvironment(environmentId: string) {
    if (!environmentId || environmentId === state.activeEnvironmentId) {
      return;
    }
    setState((prev) => ({
      ...prev,
      activeEnvironmentId: environmentId,
      environmentVersions: [],
      environmentAudits: [],
    }));
    setVersionError(null);
    setVersionDescription('');
    startNavigation(() => {
      updateSearchParams({
        team: state.activeTeamId ?? null,
        project: state.activeProjectId ?? null,
        environment: environmentId,
      });
    });
    void reloadState(state.activeTeamId, state.activeProjectId, undefined, environmentId);
  }

  function handlePublishEnvironmentVersion(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!state.activeProjectId || !state.activeEnvironmentId) {
      setVersionError('Select an environment to publish a version.');
      return;
    }

    const summary = versionDescription.trim();
    const lines = versionEditor.split('\n');
    const variables: { key: string; value: string }[] = [];
    const seen = new Set<string>();

    for (const rawLine of lines) {
      const line = rawLine.trim();
      if (!line) {
        continue;
      }
      const [keyPart, ...rest] = line.split('=');
      const key = keyPart.trim().toUpperCase();
      const value = rest.join('=').trim();
      if (!key) {
        setVersionError('Variable keys are required.');
        return;
      }
      if (seen.has(key)) {
        setVersionError(`Duplicate variable detected: ${key}`);
        return;
      }
      seen.add(key);
      variables.push({ key, value });
    }

    if (variables.length === 0) {
      setVersionError('Provide at least one variable.');
      return;
    }

    setVersionError(null);
    startVersionAction(async () => {
      const result = await createEnvironmentVersionAction({
        projectId: state.activeProjectId!,
        environmentId: state.activeEnvironmentId!,
        description: summary || undefined,
        variables,
      });
      if (!result.success) {
        setVersionError(result.error ?? 'Failed to publish version.');
        return;
      }
      setVersionDescription('');
      setVersionEditor('');
      await reloadState(
        state.activeTeamId,
        state.activeProjectId,
        'Environment version published.',
        state.activeEnvironmentId,
      );
    });
  }

  function handleTriggerDeployment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!state.activeProjectId) {
      setDeployError('Select a project first.');
      return;
    }
    setDeployError(null);
    startDeployAction(async () => {
      const result = await triggerDeploymentAction({
        projectId: state.activeProjectId!,
        commit: deployCommit.trim() || undefined,
      });
      if (!result.success) {
        setDeployError(result.error ?? 'Failed to trigger deployment.');
        return;
      }
      setDeployCommit('');
      await reloadState(state.activeTeamId, state.activeProjectId, 'Deployment triggered.');
    });
  }

  function handleDeleteDeployment(deploymentId: string) {
    if (!deploymentId) {
      return;
    }
    setDeletingDeploymentId(deploymentId);
    startDeleteAction(async () => {
      const result = await deleteDeploymentAction({ deploymentId });
      if (!result.success) {
        showToast(result.error ?? 'Failed to delete deployment.', 'error');
        setDeletingDeploymentId(null);
        return;
      }
      await reloadState(state.activeTeamId, state.activeProjectId, 'Deployment deleted.');
      setDeletingDeploymentId(null);
    });
  }

  async function handleLoadMoreLogs() {
    if (!state.activeProjectId || logsLoading || !state.logsHasMore) {
      return;
    }
    setLogsError(null);
    setLogsLoading(true);
    try {
      const params = new URLSearchParams({
        project: state.activeProjectId,
        offset: String(state.logs.length),
        limit: String(LOGS_PAGE_SIZE),
      });
      const response = await fetch(`/api/dashboard/logs?${params.toString()}`, {
        method: 'GET',
        credentials: 'include',
        cache: 'no-store',
      });
      if (response.status === 401) {
        router.refresh();
        return;
      }
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as { error?: string } | null;
        const message = body?.error ?? 'Unable to load more logs.';
        setLogsError(message);
        return;
      }
      const payload = (await response.json()) as { entries: ProjectLog[]; hasMore: boolean };
      setState((prev) => {
        const existingIds = new Set(prev.logs.map((log) => log.id));
        const newEntries = payload.entries.filter((log) => !existingIds.has(log.id));
        return {
          ...prev,
          logs: [...prev.logs, ...newEntries],
          logsHasMore: payload.hasMore,
        };
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unable to load more logs.';
      setLogsError(message);
    } finally {
      setLogsLoading(false);
    }
  }

  async function handleLoadMoreRuntimeEvents() {
    const activeProjectId = state.activeProjectId;
    if (!activeProjectId || runtimeEventsLoading || !state.runtimeEventsHasMore) {
      return;
    }
    setRuntimeEventsError(null);
    setRuntimeEventsLoading(true);
    try {
      const params = new URLSearchParams({
        project: activeProjectId,
        offset: String(state.runtimeEvents.length),
        limit: String(RUNTIME_EVENTS_PAGE_SIZE),
      });
      if (runtimeEventTypeFilter !== 'all') {
        params.set('eventType', runtimeEventTypeFilter);
      }
      const response = await fetch(`/api/dashboard/runtime/events?${params.toString()}`, {
        method: 'GET',
        credentials: 'include',
        cache: 'no-store',
      });
      if (response.status === 401) {
        router.refresh();
        return;
      }
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as { error?: string } | null;
        const message = body?.error ?? 'Unable to load more runtime events.';
        setRuntimeEventsError(message);
        return;
      }
      const payload = (await response.json()) as { entries: RuntimeEvent[]; hasMore: boolean };
      setState((prev) => {
        if (prev.activeProjectId !== activeProjectId) {
          return prev;
        }
        const existingIds = new Set(prev.runtimeEvents.map((event) => event.id));
        const additions = payload.entries.filter((event) => !existingIds.has(event.id));
        return {
          ...prev,
          runtimeEvents: [...prev.runtimeEvents, ...additions],
          runtimeEventsHasMore: payload.hasMore,
        };
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unable to load more runtime events.';
      setRuntimeEventsError(message);
    } finally {
      setRuntimeEventsLoading(false);
    }
  }

  const disableInputs =
    navigationPending ||
    refreshTransitionPending ||
    logoutPending ||
    teamPending ||
    projectPending ||
  environmentPending ||
  versionPending ||
    deployPending ||
    dataPending;

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-8">
      {toast ? (
        <div
          key={toast.id}
          className={`fixed left-1/2 top-6 z-50 w-11/12 max-w-md -translate-x-1/2 rounded-2xl border px-4 py-3 text-sm shadow-lg ${{
            success: 'border-emerald-400/60 bg-emerald-500/15 text-emerald-100',
            error: 'border-rose-400/60 bg-rose-500/15 text-rose-100',
          }[toast.variant]}`}
        >
          {toast.message}
        </div>
      ) : null}

      <header className="glass-card flex flex-col gap-6 px-6 py-6 md:flex-row md:items-center md:justify-between">
        <div className="space-y-1">
          <div className="badge">authenticated</div>
          <h1 className="text-2xl font-semibold text-white">Hello, {state.userEmail}</h1>
          {selectedTeam ? (
            <p className="muted">Currently viewing team: {selectedTeam.name}</p>
          ) : (
            <p className="muted">Select or create a team to get started.</p>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={handleRefresh}
            disabled={refreshTransitionPending || dataPending}
            className="rounded-xl border border-cyan-500/50 px-4 py-2 text-sm font-semibold text-cyan-200 transition hover:border-cyan-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
          >
            {refreshTransitionPending || dataPending ? 'Refreshing…' : 'Refresh view'}
          </button>
          <button
            type="button"
            onClick={handleLogout}
            disabled={logoutPending}
            className="rounded-xl border border-rose-500/50 px-4 py-2 text-sm font-semibold text-rose-200 transition hover:border-rose-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
          >
            {logoutPending ? 'Signing out…' : 'Sign out'}
          </button>
        </div>
      </header>

      <section className="grid gap-6 lg:grid-cols-12">
        <article className="glass-card flex h-full flex-col gap-6 px-6 py-6 lg:col-span-4">
          <header className="space-y-1">
            <h2 className="section-heading">Teams</h2>
            <p className="muted">Switch contexts and manage quotas per organization.</p>
          </header>
          {navigationPending ? <p className="muted">Loading teams…</p> : null}
          {state.teams.length === 0 ? <p className="muted">No teams yet. Create one below to begin.</p> : null}
          <ul className="space-y-3">
            {state.teams.map((team) => {
              const active = team.id === state.activeTeamId;
              return (
                <li key={team.id}>
                  <button
                    type="button"
                    onClick={() => handleSelectTeam(team.id)}
                    disabled={disableInputs}
                    className={`w-full rounded-2xl border px-4 py-3 text-left transition ${
                      active
                        ? 'border-cyan-400/80 bg-cyan-400/15 text-white'
                        : 'border-white/10 bg-slate-900/40 text-slate-200 hover:border-cyan-300/40 hover:bg-cyan-400/10'
                    }`}
                  >
                    <div className="flex items-center justify-between gap-3 text-sm font-semibold">
                      <span>{team.name}</span>
                      <span className="badge">ID {formatId(team.id)}</span>
                    </div>
                    <dl className="mt-3 grid grid-cols-3 gap-3 text-xs text-slate-300">
                      <div>
                        <dt className="uppercase tracking-wide text-slate-500">Projects</dt>
                        <dd>{team.max_projects}</dd>
                      </div>
                      <div>
                        <dt className="uppercase tracking-wide text-slate-500">Containers</dt>
                        <dd>{team.max_containers}</dd>
                      </div>
                      <div>
                        <dt className="uppercase tracking-wide text-slate-500">Storage</dt>
                        <dd>{team.storage_limit_mb} MB</dd>
                      </div>
                    </dl>
                  </button>
                </li>
              );
            })}
          </ul>
          <div>
            <button
              type="button"
              onClick={() => setTeamFormOpen((open) => !open)}
              disabled={disableInputs}
              className="w-full rounded-2xl border border-cyan-500/50 px-4 py-3 text-sm font-semibold text-cyan-200 transition hover:border-cyan-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
            >
              {teamFormOpen ? 'Close team creator' : 'Create a new team'}
            </button>
            {teamFormOpen ? (
              <form className="mt-4 space-y-3" onSubmit={handleCreateTeam}>
                <label className="block text-sm text-slate-200">
                  Team name
                  <input
                    className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                    value={teamName}
                    onChange={(event: ChangeEvent<HTMLInputElement>) => setTeamName(event.target.value)}
                    placeholder="Acme Cloud"
                    required
                    disabled={disableInputs}
                  />
                </label>
                <div className="grid gap-3 md:grid-cols-3">
                  <label className="block text-sm text-slate-200">
                    Max projects
                    <input
                      className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                      value={maxProjects}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setMaxProjects(event.target.value)}
                      placeholder="5"
                      inputMode="numeric"
                      disabled={disableInputs}
                    />
                  </label>
                  <label className="block text-sm text-slate-200">
                    Max containers
                    <input
                      className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                      value={maxContainers}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setMaxContainers(event.target.value)}
                      placeholder="10"
                      inputMode="numeric"
                      disabled={disableInputs}
                    />
                  </label>
                  <label className="block text-sm text-slate-200">
                    Storage (MB)
                    <input
                      className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                      value={storageLimit}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setStorageLimit(event.target.value)}
                      placeholder="10240"
                      inputMode="numeric"
                      disabled={disableInputs}
                    />
                  </label>
                </div>
                {teamFormError ? (
                  <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
                    {teamFormError}
                  </p>
                ) : null}
                <button
                  type="submit"
                  disabled={teamPending || disableInputs}
                  className="flex w-full items-center justify-center gap-2 rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-cyan-400 focus:outline-none focus:ring-4 focus:ring-cyan-500/40 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {teamPending ? 'Creating…' : 'Save team'}
                </button>
              </form>
            ) : null}
          </div>
        </article>

        <article className="glass-card flex h-full flex-col gap-6 px-6 py-6 lg:col-span-4">
          <header className="space-y-1">
            <h2 className="section-heading">Projects</h2>
            <p className="muted">Deployable units attached to the selected team.</p>
          </header>
          {!state.activeTeamId ? <p className="muted">Select a team to inspect its projects.</p> : null}
          {state.activeTeamId && state.projects.length === 0 ? (
            <p className="muted">No projects yet. Provision one below to kick off deployments.</p>
          ) : null}
          <ul className="space-y-3">
            {state.projects.map((project) => {
              const active = project.id === state.activeProjectId;
              return (
                <li key={project.id}>
                  <button
                    type="button"
                    onClick={() => handleSelectProject(project.id)}
                    disabled={disableInputs}
                    className={`w-full rounded-2xl border px-4 py-3 text-left transition ${
                      active
                        ? 'border-emerald-400/80 bg-emerald-400/15 text-white'
                        : 'border-white/10 bg-slate-900/40 text-slate-200 hover:border-emerald-300/40 hover:bg-emerald-400/10'
                    }`}
                  >
                    <div className="flex items-center justify-between text-sm font-semibold">
                      <span>{project.name}</span>
                      <span className="badge capitalize">{project.type}</span>
                    </div>
                    <p className="mt-2 text-xs text-slate-300">Repo: {project.repo_url || 'Not linked'}</p>
                    <p className="mt-1 text-[11px] uppercase tracking-wide text-slate-500">ID {formatId(project.id, 10)}</p>
                  </button>
                </li>
              );
            })}
          </ul>
          {state.activeTeamId ? (
            <div>
              <button
                type="button"
                onClick={() => setProjectFormOpen((open) => !open)}
                disabled={disableInputs}
                className="w-full rounded-2xl border border-emerald-500/50 px-4 py-3 text-sm font-semibold text-emerald-200 transition hover:border-emerald-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
              >
                {projectFormOpen ? 'Close project creator' : 'Create a new project'}
              </button>
              {projectFormOpen ? (
                <form className="mt-4 space-y-3" onSubmit={handleCreateProject}>
                  <label className="block text-sm text-slate-200">
                    Project name
                    <input
                      className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                      value={projectName}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setProjectName(event.target.value)}
                      placeholder="marketing-site"
                      required
                      disabled={disableInputs}
                    />
                  </label>
                  <label className="block text-sm text-slate-200">
                    Repository URL
                    <input
                      className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                      value={projectRepo}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setProjectRepo(event.target.value)}
                      placeholder="https://github.com/acme/marketing"
                      required
                      disabled={disableInputs}
                    />
                  </label>
                  <div className="grid gap-3 md:grid-cols-2">
                    <label className="block text-sm text-slate-200">
                      Project type
                      <select
                        className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                        value={projectType}
                        onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                          setProjectType(event.target.value as 'frontend' | 'backend')
                        }
                        disabled={disableInputs}
                      >
                        <option value="frontend">Frontend</option>
                        <option value="backend">Backend</option>
                      </select>
                    </label>
                    <label className="block text-sm text-slate-200">
                      Build command
                      <input
                        className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                        value={projectBuildCommand}
                        onChange={(event: ChangeEvent<HTMLInputElement>) => setProjectBuildCommand(event.target.value)}
                        placeholder={DEFAULT_BUILD_COMMAND}
                        required
                        disabled={disableInputs}
                      />
                    </label>
                  </div>
                  <label className="block text-sm text-slate-200">
                    Run command
                    <input
                      className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                      value={projectRunCommand}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setProjectRunCommand(event.target.value)}
                      placeholder={DEFAULT_RUN_COMMAND}
                      required
                      disabled={disableInputs}
                    />
                  </label>
                  {projectFormError ? (
                    <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
                      {projectFormError}
                    </p>
                  ) : null}
                  <button
                    type="submit"
                    disabled={projectPending || disableInputs}
                    className="flex w-full items-center justify-center gap-2 rounded-xl bg-emerald-400 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-emerald-300 focus:outline-none focus:ring-4 focus:ring-emerald-400/40 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {projectPending ? 'Creating…' : 'Provision project'}
                  </button>
                </form>
              ) : null}
            </div>
          ) : null}
        </article>

        <article className="glass-card flex h-full flex-col gap-6 px-6 py-6 lg:col-span-4">
          <header className="space-y-1">
            <h2 className="section-heading">Project detail</h2>
            <p className="muted">Inspect configuration and manage runtime secrets.</p>
          </header>
          {!state.activeProjectId ? <p className="muted">Select a project to load its configuration.</p> : null}
          {state.project ? (
            <div className="space-y-4">
              <div className="rounded-2xl border border-white/10 bg-slate-900/60 p-4">
                <div className="flex items-center justify-between">
                  <h3 className="text-lg font-semibold text-white">{state.project.name}</h3>
                  <span className="badge capitalize">{state.project.type}</span>
                </div>
                <dl className="mt-4 space-y-2 text-sm text-slate-300">
                  <div>
                    <dt className="text-xs uppercase tracking-wide text-slate-500">Repository</dt>
                    <dd>
                      {state.project.repo_url ? (
                        <a
                          href={state.project.repo_url}
                          target="_blank"
                          rel="noreferrer"
                          className="text-cyan-300 underline-offset-2 hover:underline"
                        >
                          {state.project.repo_url}
                        </a>
                      ) : (
                        'Not linked'
                      )}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs uppercase tracking-wide text-slate-500">Build command</dt>
                    <dd className="font-mono text-xs text-slate-200">{state.project.build_command}</dd>
                  </div>
                  <div>
                    <dt className="text-xs uppercase tracking-wide text-slate-500">Run command</dt>
                    <dd className="font-mono text-xs text-slate-200">{state.project.run_command}</dd>
                  </div>
                </dl>
              </div>
              <section className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-semibold text-white">Environments</h3>
                    <p className="text-xs text-slate-400">Versioned secrets are promoted safely across environments.</p>
                  </div>
                  <button
                    type="button"
                    onClick={() => setEnvironmentFormOpen((open) => !open)}
                    disabled={disableInputs || !state.activeProjectId}
                    className="rounded-2xl border border-emerald-500/50 px-3 py-2 text-xs font-semibold text-emerald-200 transition hover:border-emerald-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {environmentFormOpen ? 'Close environment creator' : 'Create environment'}
                  </button>
                </div>
                {environmentsSorted.length > 0 ? (
                  <div className="space-y-4">
                    <div className="flex flex-wrap gap-2">
                      {environmentsSorted.map((entry) => {
                        const env = entry.environment;
                        const selected = activeEnvironment?.environment.id === env.id;
                        return (
                          <button
                            key={env.id}
                            type="button"
                            onClick={() => handleSelectEnvironment(env.id)}
                            disabled={disableInputs}
                            className={`rounded-2xl border px-4 py-2 text-left text-sm transition ${
                              selected
                                ? 'border-emerald-400/80 bg-emerald-400/15 text-white'
                                : 'border-white/10 bg-slate-900/40 text-slate-200 hover:border-emerald-300/40 hover:bg-emerald-400/10'
                            }`}
                          >
                            <div className="flex flex-wrap items-center gap-2 font-semibold">
                              <span>{env.name}</span>
                              <span className="badge capitalize">{env.environment_type}</span>
                              {env.protected ? (
                                <span className="badge border-amber-400/60 bg-amber-500/15 text-amber-200">Protected</span>
                              ) : null}
                            </div>
                            <p className="mt-1 text-xs text-slate-400">Slug: {env.slug}</p>
                          </button>
                        );
                      })}
                    </div>
                    {activeEnvironment ? (
                      <div className="space-y-4 rounded-2xl border border-white/10 bg-slate-900/70 p-4">
                        <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                          <div>
                            <h4 className="text-base font-semibold text-white">{activeEnvironment.environment.name}</h4>
                            <p className="text-xs text-slate-400">ID {formatId(activeEnvironment.environment.id, 8)}</p>
                          </div>
                          <dl className="grid gap-3 text-xs text-slate-300 sm:grid-cols-2">
                            <div>
                              <dt className="uppercase tracking-wide text-slate-500">Position</dt>
                              <dd>{activeEnvironment.environment.position}</dd>
                            </div>
                            <div>
                              <dt className="uppercase tracking-wide text-slate-500">Created</dt>
                              <dd>{formatTimestamp(activeEnvironment.environment.created_at)}</dd>
                            </div>
                            <div>
                              <dt className="uppercase tracking-wide text-slate-500">Updated</dt>
                              <dd>{formatTimestamp(activeEnvironment.environment.updated_at)}</dd>
                            </div>
                            <div>
                              <dt className="uppercase tracking-wide text-slate-500">Latest version</dt>
                              <dd>
                                {latestEnvironmentVersion ? `v${latestEnvironmentVersion.version.version}` : 'none'}
                              </dd>
                            </div>
                          </dl>
                        </div>
                        {latestEnvironmentVersion?.version.description ? (
                          <p className="text-xs text-slate-300">
                            Summary: {latestEnvironmentVersion.version.description}
                          </p>
                        ) : null}
                        <div className="space-y-2">
                          <div className="flex items-center justify-between">
                            <h4 className="text-sm font-semibold text-white">Latest variables</h4>
                            <span className="badge">{latestEnvironmentVariables.length} vars</span>
                          </div>
                          {latestEnvironmentVariables.length > 0 ? (
                            <ul className="space-y-2 text-sm text-slate-200">
                              {latestEnvironmentVariables.map((variable) => (
                                <li
                                  key={variable.key}
                                  className="flex items-center justify-between rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2"
                                >
                                  <span className="font-semibold">{variable.key}</span>
                                  <span className="font-mono text-xs text-slate-400">{variable.value}</span>
                                </li>
                              ))}
                            </ul>
                          ) : (
                            <p className="muted">No variables stored in the latest version.</p>
                          )}
                        </div>
                        <form className="space-y-3" onSubmit={handlePublishEnvironmentVersion}>
                          <div className="grid gap-3 md:grid-cols-2">
                            <label className="block text-sm text-slate-200">
                              Version description
                              <input
                                className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                                value={versionDescription}
                                onChange={(event: ChangeEvent<HTMLInputElement>) => setVersionDescription(event.target.value)}
                                placeholder="Notes for this version (optional)"
                                disabled={disableInputs}
                              />
                            </label>
                            <label className="block text-sm text-slate-200">
                              Actor
                              <input
                                className="mt-1 w-full cursor-not-allowed rounded-xl border border-white/10 bg-slate-900/50 px-3 py-2 text-sm text-slate-400"
                                value={state.userEmail}
                                readOnly
                                disabled
                              />
                            </label>
                          </div>
                          <label className="block text-sm text-slate-200">
                            Variables (KEY=VALUE per line)
                            <textarea
                              className="mt-1 h-32 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 font-mono text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                              value={versionEditor}
                              onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setVersionEditor(event.target.value)}
                              placeholder="API_URL=https://api.example.com"
                              disabled={disableInputs}
                            />
                          </label>
                          {versionError ? (
                            <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
                              {versionError}
                            </p>
                          ) : null}
                          <button
                            type="submit"
                            disabled={versionPending || disableInputs}
                            className="flex w-full items-center justify-center gap-2 rounded-xl bg-emerald-400 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-emerald-300 focus:outline-none focus:ring-4 focus:ring-emerald-400/40 disabled:cursor-not-allowed disabled:opacity-60"
                          >
                            {versionPending ? 'Publishing…' : 'Publish new version'}
                          </button>
                        </form>
                        <div className="grid gap-4 lg:grid-cols-2">
                          <div className="space-y-2">
                            <div className="flex items-center justify-between">
                              <h4 className="text-sm font-semibold text-white">Version history</h4>
                              <span className="badge">{environmentVersionsSorted.length}</span>
                            </div>
                            {environmentVersionsSorted.length > 0 ? (
                              <ul className="space-y-2 text-xs text-slate-300">
                                {environmentVersionsSorted.map((version) => (
                                  <li key={version.id} className="rounded-xl border border-white/10 bg-slate-900/80 p-3">
                                    <div className="flex items-center justify-between text-sm font-semibold text-white">
                                      <span>v{version.version}</span>
                                      <span>{formatTimestamp(version.created_at)}</span>
                                    </div>
                                    {version.description ? (
                                      <p className="mt-1 text-xs text-slate-300">{version.description}</p>
                                    ) : null}
                                    <p className="mt-1 text-[11px] uppercase tracking-wide text-slate-500">
                                      {version.created_by ? `Published by ${version.created_by}` : 'Published via dashboard'}
                                    </p>
                                  </li>
                                ))}
                              </ul>
                            ) : (
                              <p className="muted">No historical versions yet.</p>
                            )}
                          </div>
                          <div className="space-y-2">
                            <div className="flex items-center justify-between">
                              <h4 className="text-sm font-semibold text-white">Audit trail</h4>
                              <span className="badge">{state.environmentAudits.length}</span>
                            </div>
                            {state.environmentAudits.length > 0 ? (
                              <ul className="space-y-2 text-xs text-slate-300">
                                {state.environmentAudits.map((audit) => (
                                  <li key={audit.id} className="rounded-xl border border-white/10 bg-slate-900/80 p-3">
                                    <div className="flex items-center justify-between text-sm font-semibold text-white">
                                      <span>{audit.action}</span>
                                      <span>{formatTimestamp(audit.created_at)}</span>
                                    </div>
                                    {audit.actor_id ? (
                                      <p className="mt-1 text-[11px] uppercase tracking-wide text-slate-500">Actor: {audit.actor_id}</p>
                                    ) : null}
                                    {audit.metadata ? (
                                      <pre className="mt-2 max-h-32 overflow-x-auto overflow-y-auto rounded-lg bg-black/30 p-2 text-[11px] text-slate-200">
                                        {JSON.stringify(audit.metadata, null, 2)}
                                      </pre>
                                    ) : null}
                                  </li>
                                ))}
                              </ul>
                            ) : (
                              <p className="muted">No audit entries yet.</p>
                            )}
                          </div>
                        </div>
                      </div>
                    ) : (
                      <p className="muted">Select an environment to inspect details.</p>
                    )}
                  </div>
                ) : (
                  <p className="muted">No environments defined yet. Create one to start managing secrets.</p>
                )}
                {environmentFormOpen ? (
                  <form
                    className="space-y-3 rounded-2xl border border-emerald-500/30 bg-emerald-500/10 p-4"
                    onSubmit={handleCreateEnvironment}
                  >
                    <div className="grid gap-3 md:grid-cols-2">
                      <label className="block text-sm text-slate-200">
                        Name
                        <input
                          className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                          value={environmentName}
                          onChange={(event: ChangeEvent<HTMLInputElement>) => setEnvironmentName(event.target.value)}
                          placeholder="Production"
                          required
                          disabled={disableInputs}
                        />
                      </label>
                      <label className="block text-sm text-slate-200">
                        Slug
                        <input
                          className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                          value={environmentSlug}
                          onChange={(event: ChangeEvent<HTMLInputElement>) => setEnvironmentSlug(event.target.value)}
                          placeholder="production"
                          disabled={disableInputs}
                        />
                      </label>
                    </div>
                    <div className="grid gap-3 md:grid-cols-3">
                      <label className="block text-sm text-slate-200">
                        Type
                        <select
                          className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                          value={environmentType}
                          onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                            setEnvironmentType(event.target.value as (typeof environmentTypeOptions)[number])
                          }
                          disabled={disableInputs}
                        >
                          {environmentTypeOptions.map((option) => (
                            <option key={option} value={option}>
                              {option}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="block text-sm text-slate-200">
                        Position
                        <input
                          className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                          value={environmentPosition}
                          onChange={(event: ChangeEvent<HTMLInputElement>) => setEnvironmentPosition(event.target.value)}
                          placeholder="1"
                          inputMode="numeric"
                          disabled={disableInputs}
                        />
                      </label>
                      <label className="flex items-center gap-3 text-sm text-slate-200">
                        <input
                          type="checkbox"
                          className="h-4 w-4 rounded border-white/10 bg-slate-900 text-emerald-400 focus:ring-emerald-500/40"
                          checked={environmentProtected}
                          onChange={(event: ChangeEvent<HTMLInputElement>) => setEnvironmentProtected(event.target.checked)}
                          disabled={disableInputs}
                        />
                        Protected
                      </label>
                    </div>
                    {environmentFormError ? (
                      <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
                        {environmentFormError}
                      </p>
                    ) : null}
                    <button
                      type="submit"
                      disabled={environmentPending || disableInputs}
                      className="flex w-full items-center justify-center gap-2 rounded-xl bg-emerald-400 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-emerald-300 focus:outline-none focus:ring-4 focus:ring-emerald-400/40 disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      {environmentPending ? 'Creating…' : 'Save environment'}
                    </button>
                  </form>
                ) : null}
              </section>
              <section className="space-y-3">
                <div className="flex items-center justify-between">
                  <h3 className="text-sm font-semibold text-white">Deployments</h3>
                  <span className="badge">{state.deployments.length} recent</span>
                </div>
                {state.deploymentsHasMore ? (
                  <p className="text-xs text-slate-400">Showing the latest {state.deployments.length} deployments.</p>
                ) : null}
                <form
                  className="space-y-2 md:flex md:items-end md:gap-3 md:space-y-0"
                  onSubmit={handleTriggerDeployment}
                >
                  <label className="flex-1 text-sm text-slate-200">
                    Commit SHA (optional)
                    <input
                      className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                      value={deployCommit}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setDeployCommit(event.target.value)}
                      placeholder="main or commit SHA"
                      disabled={disableInputs}
                    />
                  </label>
                  <button
                    type="submit"
                    disabled={deployPending || disableInputs}
                    className="flex w-full items-center justify-center gap-2 rounded-xl bg-cyan-500 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-cyan-400 focus:outline-none focus:ring-4 focus:ring-cyan-500/40 disabled:cursor-not-allowed disabled:opacity-60 md:w-auto"
                  >
                    {deployPending ? 'Triggering…' : 'Trigger deployment'}
                  </button>
                </form>
                {deployError ? (
                  <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
                    {deployError}
                  </p>
                ) : null}
                {state.deployments.length > 0 ? (
                  <ul className="space-y-2">
                    {state.deployments.map((deployment) => {
                      const commitLabel = deployment.commit_sha ? deployment.commit_sha.slice(0, 8) : 'latest';
                      return (
                        <li
                          key={deployment.id}
                          className="space-y-2 rounded-xl border border-white/10 bg-slate-900/70 p-3 text-sm text-slate-200"
                        >
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="font-semibold text-white">{deployment.stage || 'pending'}</span>
                            <span className={deploymentBadgeClass(deployment.status)}>{deployment.status}</span>
                          </div>
                          <dl className="grid gap-2 text-[11px] uppercase tracking-wide text-slate-400 sm:grid-cols-2">
                            <div>
                              <dt>Commit</dt>
                              <dd className="font-mono text-xs text-slate-200">{commitLabel}</dd>
                            </div>
                            <div>
                              <dt>Started</dt>
                              <dd>{formatTimestamp(deployment.started_at)}</dd>
                            </div>
                            <div>
                              <dt>Completed</dt>
                              <dd>{formatTimestamp(deployment.completed_at)}</dd>
                            </div>
                            <div>
                              <dt>Updated</dt>
                              <dd>{formatTimestamp(deployment.updated_at)}</dd>
                            </div>
                          </dl>
                          {deployment.message ? (
                            <p className="text-xs text-slate-200">{deployment.message}</p>
                          ) : null}
                          {deployment.error ? (
                            <p className="text-xs text-rose-300">Error: {deployment.error}</p>
                          ) : null}
                          {deployment.url ? (
                            <a
                              href={deployment.url}
                              target="_blank"
                              rel="noreferrer"
                              className="inline-flex items-center text-xs font-semibold text-cyan-300 underline-offset-2 hover:underline"
                            >
                              Open preview
                            </a>
                          ) : null}
                          <div className="flex flex-wrap items-center gap-2 pt-1">
                            <span className="badge">ID {formatId(deployment.id, 8)}</span>
                            <button
                              type="button"
                              onClick={() => handleDeleteDeployment(deployment.id)}
                              disabled={deletePending && deletingDeploymentId === deployment.id}
                              className="rounded-lg border border-rose-500/50 px-2 py-1 text-[11px] font-semibold text-rose-200 transition hover:border-rose-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                            >
                              {deletePending && deletingDeploymentId === deployment.id ? 'Deleting…' : 'Delete'}
                            </button>
                          </div>
                        </li>
                      );
                    })}
                  </ul>
                ) : (
                  <p className="muted">No deployments recorded yet.</p>
                )}
              </section>
              <section className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-semibold text-white">Runtime insights</h3>
                    <p className="text-xs text-slate-400">
                      {runtimeSummary
                        ? `Tracking ${runtimeSummary.totalCount} events across ${runtimeBucketSpan}-second buckets.`
                        : 'No runtime metrics collected yet.'}
                    </p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2 text-xs text-slate-300">
                    <label className="flex items-center gap-1">
                      Bucket
                      <select
                        className="rounded-lg border border-white/10 bg-slate-900/80 px-2 py-1 text-xs text-slate-200 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                        value={runtimeBucketSpan}
                        onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                          handleRuntimeBucketSpanSelect(Number(event.target.value))
                        }
                      >
                        {runtimeBucketOptions.map((option) => (
                          <option key={option} value={option}>
                            {option >= 60 ? `${Math.round(option / 60)} min` : `${option} sec`}
                          </option>
                        ))}
                      </select>
                    </label>
                    <label className="flex items-center gap-1">
                      Type
                      <select
                        className="rounded-lg border border-white/10 bg-slate-900/80 px-2 py-1 text-xs text-slate-200 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                        value={runtimeEventTypeFilter}
                        onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                          handleRuntimeEventTypeFilterSelect(event.target.value as 'all' | string)
                        }
                      >
                        <option value="all">All</option>
                        {runtimeEventTypes.map((type) => (
                          <option key={type} value={type}>
                            {type}
                          </option>
                        ))}
                      </select>
                    </label>
                    <button
                      type="button"
                      onClick={handleManualRuntimeMetricsRefresh}
                      disabled={runtimeMetricsLoading || !state.activeProjectId}
                      className="rounded-lg border border-cyan-500/50 px-3 py-1 font-semibold text-cyan-200 transition hover:border-cyan-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      {runtimeMetricsLoading ? 'Refreshing…' : 'Refresh'}
                    </button>
                  </div>
                </div>
                {runtimeMetricsError ? (
                  <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-xs text-rose-200">
                    {runtimeMetricsError}
                  </p>
                ) : null}
                {runtimeRollupsSorted.length > 0 ? (
                  <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                    <div className="rounded-2xl border border-white/10 bg-slate-900/70 p-4">
                      <div className="flex items-center justify-between text-xs uppercase tracking-wide text-slate-400">
                        <span>Latency P95</span>
                        <span className="text-[11px] text-slate-500">
                          {runtimeMetricsLoading ? 'Updating…' : 'Live'}
                        </span>
                      </div>
                      <p className="mt-2 text-2xl font-semibold text-white">
                        {formatLatencyValue(runtimeSummary?.latest?.p95_ms ?? null)}
                      </p>
                      <p className="text-xs text-slate-400">
                        Bucket started {formatTimestamp(runtimeSummary?.latest?.bucket_start)}
                      </p>
                      <div className="mt-3 h-16 w-full">
                        <svg viewBox="0 0 160 48" className="h-full w-full text-cyan-300" preserveAspectRatio="none">
                          <path
                            d={latencySparklinePath || 'M0 48 L160 48'}
                            fill="none"
                            stroke="currentColor"
                            strokeWidth="2"
                            strokeLinecap="round"
                            opacity={0.8}
                          />
                        </svg>
                      </div>
                    </div>
                    <div className="rounded-2xl border border-white/10 bg-slate-900/70 p-4">
                      <div className="flex items-center justify-between text-xs uppercase tracking-wide text-slate-400">
                        <span>Throughput</span>
                        <span className="text-[11px] text-slate-500">
                          {runtimeSummary ? `${runtimeSummary.totalCount} total` : '—'}
                        </span>
                      </div>
                      <p className="mt-2 text-2xl font-semibold text-white">
                        {formatRatePerSecond(
                          runtimeSummary?.latest?.count ?? 0,
                          runtimeSummary?.latest?.bucket_span_seconds ?? runtimeBucketSpan,
                        )}
                      </p>
                      <p className="text-xs text-slate-400">
                        {runtimeSummary?.latest
                          ? `${runtimeSummary.latest.count} events in last bucket`
                          : 'No buckets recorded.'}
                      </p>
                      <div className="mt-3 h-16 w-full">
                        <svg viewBox="0 0 160 48" className="h-full w-full text-emerald-300" preserveAspectRatio="none">
                          <path
                            d={throughputSparklinePath || 'M0 48 L160 48'}
                            fill="none"
                            stroke="currentColor"
                            strokeWidth="2"
                            strokeLinecap="round"
                            opacity={0.8}
                          />
                        </svg>
                      </div>
                    </div>
                    <div className="rounded-2xl border border-white/10 bg-slate-900/70 p-4">
                      <div className="flex items-center justify-between text-xs uppercase tracking-wide text-slate-400">
                        <span>Error rate</span>
                        <span className="text-[11px] text-slate-500">
                          {runtimeSummary ? `${runtimeSummary.totalErrors} errors` : '—'}
                        </span>
                      </div>
                      <p className="mt-2 text-2xl font-semibold text-white">{formatPercent(runtimeSummary?.errorRate ?? 0)}</p>
                      <p className="text-xs text-slate-400">
                        {runtimeSummary?.latest
                          ? `${runtimeSummary.latest.error_count} errors last bucket`
                          : 'No error buckets recorded.'}
                      </p>
                      <div className="mt-3 flex items-center gap-2 text-[11px] text-slate-400">
                        <span className={streamStatusBadgeClass(runtimeStreamStatus)}>runtime {runtimeStreamStatus}</span>
                        <span className={streamStatusBadgeClass(logStreamStatus)}>builder {logStreamStatus}</span>
                      </div>
                    </div>
                  </div>
                ) : (
                  <div className="rounded-2xl border border-dashed border-white/15 bg-slate-900/40 p-6 text-sm text-slate-300">
                    {runtimeMetricsLoading
                      ? 'Collecting runtime metrics…'
                      : 'Runtime metrics will appear once telemetry events arrive.'}
                  </div>
                )}
              </section>
              <section className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-semibold text-white">Activity</h3>
                    <p className="text-xs text-slate-400">Inspect live runtime telemetry alongside build outputs.</p>
                  </div>
                  <div className="flex items-center gap-1 rounded-full border border-white/10 bg-slate-900/60 p-1">
                    <button
                      type="button"
                      onClick={() => setInsightTab('runtime')}
                      className={`rounded-full px-3 py-1 text-xs font-semibold transition ${
                        insightTab === 'runtime'
                          ? 'bg-cyan-500 text-slate-950 shadow'
                          : 'text-slate-300 hover:text-white'
                      }`}
                    >
                      Runtime
                    </button>
                    <button
                      type="button"
                      onClick={() => setInsightTab('build')}
                      className={`rounded-full px-3 py-1 text-xs font-semibold transition ${
                        insightTab === 'build'
                          ? 'bg-emerald-500 text-slate-950 shadow'
                          : 'text-slate-300 hover:text-white'
                      }`}
                    >
                      Build logs
                    </button>
                  </div>
                </div>
                <AnimatePresence mode="wait" initial={false}>
                  {insightTab === 'runtime' ? (
                    <motion.div
                      key="runtime"
                      initial={{ opacity: 0, y: 8 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, y: -8 }}
                      transition={{ duration: 0.2 }}
                      className="space-y-3"
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <div className="min-w-[200px] flex-1">
                          <input
                            className="w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                            placeholder="Search message, path, metadata…"
                            value={runtimeSearchTerm}
                            onChange={(event: ChangeEvent<HTMLInputElement>) => setRuntimeSearchTerm(event.target.value)}
                          />
                        </div>
                        <select
                          className="rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-xs text-slate-200 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                          value={runtimeLevelFilter}
                          onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                            setRuntimeLevelFilter(event.target.value as (typeof runtimeLevelOptions)[number] | 'all')
                          }
                        >
                          <option value="all">All levels</option>
                          {runtimeLevelOptions.map((level) => (
                            <option key={level} value={level}>
                              {level}
                            </option>
                          ))}
                        </select>
                        <select
                          className="rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-xs text-slate-200 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                          value={runtimeMethodFilter}
                          onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                            setRuntimeMethodFilter(event.target.value as 'all' | string)
                          }
                        >
                          <option value="all">All methods</option>
                          {runtimeMethodOptions.map((method) => (
                            <option key={method} value={method}>
                              {method}
                            </option>
                          ))}
                        </select>
                        <select
                          className="rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-xs text-slate-200 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                          value={runtimeSourceFilter}
                          onChange={(event: ChangeEvent<HTMLSelectElement>) =>
                            setRuntimeSourceFilter(event.target.value as 'all' | string)
                          }
                        >
                          <option value="all">All sources</option>
                          {runtimeSourceOptions.map((source) => (
                            <option key={source} value={source}>
                              {source}
                            </option>
                          ))}
                        </select>
                        <span className="text-xs text-slate-400">
                          {filteredRuntimeEvents.length} shown · {state.runtimeEvents.length} total
                        </span>
                      </div>
                      {filteredRuntimeEvents.length > 0 ? (
                        <ul className="space-y-2">
                          {filteredRuntimeEvents.map((event) => (
                            <li
                              key={`${event.id}-${event.ingested_at}`}
                              className="space-y-2 rounded-xl border border-white/10 bg-slate-900/70 p-3 text-sm text-slate-200"
                            >
                              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-slate-400">
                                <span className="font-semibold uppercase text-slate-200">{event.level || 'info'}</span>
                                <span>{formatTimestamp(event.occurred_at)}</span>
                              </div>
                              <div className="flex flex-wrap items-center gap-3 text-xs font-mono text-slate-300">
                                <span>{(event.method || '—').toUpperCase()}</span>
                                <span className="break-all text-slate-200">{event.path || '/'}</span>
                                <span>Status {event.status_code ?? '—'}</span>
                                <span>{formatLatencyValue(event.latency_ms)}</span>
                              </div>
                              <p className="text-sm text-slate-100">{event.message}</p>
                              <div className="flex flex-wrap items-center gap-3 text-[11px] uppercase tracking-wide text-slate-500">
                                <span>Source: {event.source || 'runtime'}</span>
                                <span>Type: {event.event_type || '—'}</span>
                                <span>ID {formatId(event.project_id, 10)}</span>
                              </div>
                              {event.metadata ? (
                                <pre className="whitespace-pre-wrap rounded-xl bg-slate-950/60 p-2 text-[11px] text-slate-300">
                                  {JSON.stringify(event.metadata, null, 2)}
                                </pre>
                              ) : null}
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <p className="muted">No runtime events match the current filters.</p>
                      )}
                      {runtimeEventsError ? (
                        <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-xs text-rose-200">
                          {runtimeEventsError}
                        </p>
                      ) : null}
                      {state.runtimeEventsHasMore ? (
                        <button
                          type="button"
                          onClick={handleLoadMoreRuntimeEvents}
                          disabled={runtimeEventsLoading}
                          className="w-full rounded-xl border border-cyan-500/50 px-4 py-2 text-sm font-semibold text-cyan-200 transition hover:border-cyan-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {runtimeEventsLoading ? 'Loading…' : `Load ${RUNTIME_EVENTS_PAGE_SIZE} more events`}
                        </button>
                      ) : state.runtimeEvents.length > 0 ? (
                        <p className="muted text-xs">Showing all available runtime events.</p>
                      ) : null}
                    </motion.div>
                  ) : (
                    <motion.div
                      key="build"
                      initial={{ opacity: 0, y: 8 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, y: -8 }}
                      transition={{ duration: 0.2 }}
                      className="space-y-3"
                    >
                      {state.logs.length > 0 ? (
                        <ul className="space-y-2">
                          {state.logs.map((log) => (
                            <li
                              key={`${log.id}-${log.created_at}`}
                              className="space-y-2 rounded-xl border border-white/10 bg-slate-900/70 p-3 text-sm text-slate-200"
                            >
                              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-slate-400">
                                <span className="font-semibold uppercase text-slate-200">{log.level}</span>
                                <span>{formatTimestamp(log.created_at)}</span>
                              </div>
                              <p className="text-sm text-slate-100">{log.message}</p>
                              <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] uppercase tracking-wide text-slate-500">
                                <span>Source: {log.source || 'n/a'}</span>
                                <span>ID {formatId(log.project_id, 10)}</span>
                              </div>
                              {log.metadata ? (
                                <pre className="whitespace-pre-wrap rounded-xl bg-slate-950/60 p-2 text-[11px] text-slate-300">
                                  {JSON.stringify(log.metadata, null, 2)}
                                </pre>
                              ) : null}
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <p className="muted">No build logs available for this project.</p>
                      )}
                      {logsError ? (
                        <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-xs text-rose-200">
                          {logsError}
                        </p>
                      ) : null}
                      {state.logsHasMore ? (
                        <button
                          type="button"
                          onClick={handleLoadMoreLogs}
                          disabled={logsLoading}
                          className="w-full rounded-xl border border-cyan-500/50 px-4 py-2 text-sm font-semibold text-cyan-200 transition hover:border-cyan-400 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                        >
                          {logsLoading ? 'Loading…' : `Load ${LOGS_PAGE_SIZE} more logs`}
                        </button>
                      ) : state.logs.length > 0 ? (
                        <p className="muted text-xs">Showing all available builder logs.</p>
                      ) : null}
                    </motion.div>
                  )}
                </AnimatePresence>
              </section>
            </div>
          ) : null}
        </article>
      </section>
    </div>
  );
}
