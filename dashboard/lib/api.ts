import type {
  CreateProjectInput,
  CreateTeamInput,
  Deployment,
  Project,
  ProjectEnvVar,
  ProjectLog,
  SessionPayload,
  RuntimeEvent,
  RuntimeMetricRollup,
  Team,
} from '@/types';

interface RawTeam {
  ID: string;
  Name: string;
  OwnerID: string;
  MaxProjects: number;
  MaxContainers: number;
  StorageLimitMB: number;
  CreatedAt: string;
}

interface RawProject {
  ID: string;
  TeamID: string;
  Name: string;
  RepoURL: string;
  Type: string;
  BuildCommand: string;
  RunCommand: string;
  CreatedAt: string;
}

interface RawEnvVar {
  ProjectID: string;
  Key: string;
  Value: string;
}

interface RawDeployment {
  ID: string;
  ProjectID: string;
  CommitSHA: string;
  Status: string;
  Stage: string;
  Message: string;
  URL: string | null;
  Error: string | null;
  Metadata: unknown;
  StartedAt: string;
  CompletedAt: string | null;
  UpdatedAt: string;
}

interface RawLogEntry {
  ID: number;
  ProjectID: string;
  Source: string;
  Level: string;
  Message: string;
  Metadata: unknown;
  CreatedAt: string;
}

interface RawRuntimeRollup {
  project_id: string;
  bucket_start: string;
  bucket_span_seconds: number;
  source: string;
  event_type: string;
  count: number;
  error_count: number;
  p50_ms: number | null;
  p90_ms: number | null;
  p95_ms: number | null;
  p99_ms: number | null;
  max_ms: number | null;
  avg_ms: number | null;
  updated_at: string;
}

interface RawRuntimeEvent {
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

function normalizeTeam(raw: RawTeam): Team {
  return {
    id: raw.ID,
    name: raw.Name,
    owner_id: raw.OwnerID,
    max_projects: raw.MaxProjects,
    max_containers: raw.MaxContainers,
    storage_limit_mb: raw.StorageLimitMB,
    created_at: raw.CreatedAt,
  };
}

function normalizeProject(raw: RawProject): Project {
  return {
    id: raw.ID,
    team_id: raw.TeamID,
    name: raw.Name,
    repo_url: raw.RepoURL,
    type: raw.Type,
    build_command: raw.BuildCommand,
    run_command: raw.RunCommand,
    created_at: raw.CreatedAt,
  };
}

function normalizeEnvVar(raw: RawEnvVar): ProjectEnvVar {
  return {
    key: raw.Key,
    value: raw.Value,
  };
}

function parseMetadata(raw: unknown): Record<string, unknown> | null {
  if (raw === null || raw === undefined) {
    return null;
  }
  if (typeof raw === 'string') {
    const trimmed = raw.trim();
    if (!trimmed) {
      return null;
    }
    try {
      const parsed = JSON.parse(trimmed) as unknown;
      return typeof parsed === 'object' && parsed !== null ? (parsed as Record<string, unknown>) : null;
    } catch {
      return null;
    }
  }
  if (typeof raw === 'object') {
    return raw as Record<string, unknown>;
  }
  return null;
}

function normalizeDeployment(raw: RawDeployment): Deployment {
  return {
    id: raw.ID,
    project_id: raw.ProjectID,
    commit_sha: raw.CommitSHA,
    status: raw.Status,
    stage: raw.Stage,
    message: raw.Message,
    url: raw.URL || null,
    error: raw.Error || null,
    metadata: parseMetadata(raw.Metadata),
    started_at: raw.StartedAt,
    completed_at: raw.CompletedAt,
    updated_at: raw.UpdatedAt,
  };
}

function normalizeLogEntry(raw: RawLogEntry): ProjectLog {
  return {
    id: raw.ID,
    project_id: raw.ProjectID,
    source: raw.Source,
    level: raw.Level,
    message: raw.Message,
    metadata: parseMetadata(raw.Metadata),
    created_at: raw.CreatedAt,
  };
}

function normalizeRuntimeRollup(raw: RawRuntimeRollup): RuntimeMetricRollup {
  return {
    project_id: raw.project_id,
    bucket_start: raw.bucket_start,
    bucket_span_seconds: raw.bucket_span_seconds,
    source: raw.source,
    event_type: raw.event_type,
    count: raw.count,
    error_count: raw.error_count,
    p50_ms: raw.p50_ms,
    p90_ms: raw.p90_ms,
    p95_ms: raw.p95_ms,
    p99_ms: raw.p99_ms,
    max_ms: raw.max_ms,
    avg_ms: raw.avg_ms,
    updated_at: raw.updated_at,
  };
}

function normalizeRuntimeEvent(raw: RawRuntimeEvent): RuntimeEvent {
  return {
    id: raw.id,
    project_id: raw.project_id,
    source: raw.source,
    event_type: raw.event_type,
    level: raw.level,
    message: raw.message,
    method: raw.method,
    path: raw.path,
    status_code: raw.status_code,
    latency_ms: raw.latency_ms,
    bytes_in: raw.bytes_in,
    bytes_out: raw.bytes_out,
    metadata: parseMetadata(raw.metadata),
    occurred_at: raw.occurred_at,
    ingested_at: raw.ingested_at,
  };
}

const globalProcess = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process;
export const API_BASE_URL = globalProcess?.env?.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:4000';

export class ApiError extends Error {
  public readonly status: number;
  public readonly payload: unknown;

  constructor(message: string, status: number, payload: unknown = null) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.payload = payload;
  }
}

function authHeaders(token: string): Record<string, string> {
  return {
    Authorization: `Bearer ${token}`,
  };
}

async function request<T>(path: string, init: RequestInit): Promise<T> {
  const url = new URL(path, API_BASE_URL);
  const response = await fetch(url, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init.headers ?? {}),
    },
    cache: 'no-store',
  });

  const text = await response.text();
  let payload: unknown = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = text;
    }
  }

  if (!response.ok) {
    const message =
      typeof (payload as { error?: string } | null)?.error === 'string'
        ? (payload as { error: string }).error
        : response.statusText;
    throw new ApiError(message || 'request failed', response.status, payload);
  }

  return payload as T;
}

export async function login(email: string, password: string): Promise<SessionPayload> {
  return request<SessionPayload>('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  });
}

export async function signup(email: string, password: string): Promise<SessionPayload> {
  return request<SessionPayload>('/auth/signup', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  });
}

export async function listTeams(accessToken: string): Promise<Team[]> {
  const teams = await request<RawTeam[]>('/teams', {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  return teams.map(normalizeTeam);
}

export async function listProjects(accessToken: string, teamId: string): Promise<Project[]> {
  const search = new URLSearchParams({ team_id: teamId });
  const projects = await request<RawProject[]>(`/projects?${search.toString()}`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  return projects.map(normalizeProject);
}

export async function createTeam(accessToken: string, input: CreateTeamInput): Promise<Team> {
  const team = await request<RawTeam>('/teams', {
    method: 'POST',
    headers: authHeaders(accessToken),
    body: JSON.stringify(input),
  });
  return normalizeTeam(team);
}

export async function createProject(accessToken: string, input: CreateProjectInput): Promise<Project> {
  const project = await request<RawProject>('/projects', {
    method: 'POST',
    headers: authHeaders(accessToken),
    body: JSON.stringify(input),
  });
  return normalizeProject(project);
}

export async function getProject(accessToken: string, projectId: string): Promise<Project> {
  const project = await request<RawProject>(`/projects/${encodeURIComponent(projectId)}`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  return normalizeProject(project);
}

export async function listEnvVars(accessToken: string, projectId: string): Promise<ProjectEnvVar[]> {
  const envVars = await request<RawEnvVar[] | null>(`/projects/${encodeURIComponent(projectId)}/env`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  if (!Array.isArray(envVars)) {
    return [];
  }
  return envVars.map(normalizeEnvVar);
}

export async function addEnvVar(
  accessToken: string,
  projectId: string,
  key: string,
  value: string,
): Promise<{ status: string }> {
  return request<{ status: string }>(`/projects/${encodeURIComponent(projectId)}/env`, {
    method: 'POST',
    headers: authHeaders(accessToken),
    body: JSON.stringify({ ProjectID: projectId, Key: key, Value: value }),
  });
}

export async function listDeployments(accessToken: string, projectId: string, limit = 10): Promise<Deployment[]> {
  const search = new URLSearchParams();
  if (limit > 0) {
    search.set('limit', String(limit));
  }
  const deployments = await request<RawDeployment[] | null>(
    `/deploy/${encodeURIComponent(projectId)}${search.size ? `?${search.toString()}` : ''}`,
    {
      method: 'GET',
      headers: authHeaders(accessToken),
    },
  );
  if (!Array.isArray(deployments)) {
    return [];
  }
  return deployments.map(normalizeDeployment);
}

export async function triggerDeployment(
  accessToken: string,
  projectId: string,
  commit?: string,
): Promise<Deployment> {
  const body: Record<string, string> = {};
  const trimmed = commit?.trim();
  if (trimmed) {
    body.commit = trimmed;
  }
  const deployment = await request<RawDeployment>(`/deploy/${encodeURIComponent(projectId)}`, {
    method: 'POST',
    headers: authHeaders(accessToken),
    body: JSON.stringify(body),
  });
  return normalizeDeployment(deployment);
}

export async function deleteDeployment(
  accessToken: string,
  deploymentId: string,
): Promise<{ status: string }> {
  return request<{ status: string }>(`/deployments/${encodeURIComponent(deploymentId)}`, {
    method: 'DELETE',
    headers: authHeaders(accessToken),
  });
}

export async function listLogs(accessToken: string, projectId: string, limit = 50, offset = 0): Promise<ProjectLog[]> {
  const search = new URLSearchParams();
  if (limit > 0) {
    search.set('limit', String(limit));
  }
  if (offset > 0) {
    search.set('offset', String(offset));
  }
  const logs = await request<RawLogEntry[] | null>(`/logs/${encodeURIComponent(projectId)}${search.size ? `?${search.toString()}` : ''}`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  if (!Array.isArray(logs)) {
    return [];
  }
  return logs.map(normalizeLogEntry);
}

export interface RuntimeRollupQuery {
  eventType?: string;
  source?: string;
  bucketSpanSeconds?: number;
  limit?: number;
}

export async function listRuntimeRollups(
  accessToken: string,
  projectId: string,
  query: RuntimeRollupQuery = {},
): Promise<RuntimeMetricRollup[]> {
  const search = new URLSearchParams();
  if (query.eventType) {
    search.set('event_type', query.eventType);
  }
  if (query.source) {
    search.set('source', query.source);
  }
  if (query.bucketSpanSeconds && query.bucketSpanSeconds > 0) {
    search.set('bucket_span', String(query.bucketSpanSeconds));
  }
  if (query.limit && query.limit > 0) {
    search.set('limit', String(query.limit));
  }
  search.set('project_id', projectId);
  const rollups = await request<RawRuntimeRollup[] | null>(`/runtime/metrics?${search.toString()}`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  if (!Array.isArray(rollups)) {
    return [];
  }
  return rollups.map(normalizeRuntimeRollup);
}

export interface RuntimeEventsQuery {
  eventType?: string;
  limit?: number;
  offset?: number;
}

export async function listRuntimeEvents(
  accessToken: string,
  projectId: string,
  query: RuntimeEventsQuery = {},
): Promise<RuntimeEvent[]> {
  const search = new URLSearchParams();
  if (query.eventType) {
    search.set('event_type', query.eventType);
  }
  if (typeof query.limit === 'number' && query.limit > 0) {
    search.set('limit', String(query.limit));
  }
  if (typeof query.offset === 'number' && query.offset > 0) {
    search.set('offset', String(query.offset));
  }
  search.set('project_id', projectId);
  const events = await request<RawRuntimeEvent[] | null>(`/runtime/events?${search.toString()}`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  if (!Array.isArray(events)) {
    return [];
  }
  return events.map(normalizeRuntimeEvent);
}
