import type {
  CreateEnvironmentInput,
  CreateEnvironmentVersionInput,
  CreateProjectInput,
  CreateTeamInput,
  Deployment,
  Environment,
  EnvironmentAudit,
  EnvironmentDetails,
  EnvironmentVersion,
  EnvironmentVersionDetails,
  EnvironmentVariable,
  EnvironmentVariableInput,
  Project,
  ProjectLog,
  RuntimeEvent,
  RuntimeMetricRollup,
  SessionPayload,
  Team,
  UpdateEnvironmentInput,
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

interface RawEnvironment {
  ID: string;
  ProjectID: string;
  Slug: string;
  Name: string;
  EnvironmentType: string;
  Protected: boolean;
  Position: number;
  CreatedAt: string;
  UpdatedAt: string;
}

interface RawEnvironmentVariable {
  Key: string;
  Value: string;
  Checksum?: string | null;
}

interface RawEnvironmentVersion {
  ID: string;
  EnvironmentID: string;
  Version: number;
  Description: string;
  CreatedBy: string | null;
  CreatedAt: string;
}

interface RawEnvironmentVersionDetails {
  Version: RawEnvironmentVersion;
  Variables: RawEnvironmentVariable[];
}

interface RawEnvironmentDetails {
  Environment: RawEnvironment;
  LatestVersion?: RawEnvironmentVersionDetails | null;
}

interface RawEnvironmentAudit {
  ID: number;
  ProjectID: string;
  EnvironmentID: string | null;
  VersionID: string | null;
  ActorID: string | null;
  Action: string;
  Metadata: unknown;
  CreatedAt: string;
}

function sanitizeUUID(value: string | null | undefined): string | null {
  if (typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  if (!trimmed) {
    return null;
  }
  const match = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$/.test(trimmed);
  return match ? trimmed : null;
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

function normalizeEnvironment(raw: RawEnvironment): Environment {
  return {
    id: raw.ID,
    project_id: raw.ProjectID,
    slug: raw.Slug,
    name: raw.Name,
    environment_type: raw.EnvironmentType,
    protected: raw.Protected,
    position: raw.Position,
    created_at: raw.CreatedAt,
    updated_at: raw.UpdatedAt,
  };
}

function normalizeEnvironmentVariable(raw: RawEnvironmentVariable): EnvironmentVariable {
  return {
    key: raw.Key,
    value: raw.Value,
    checksum: raw.Checksum ?? null,
  };
}

function normalizeEnvironmentVersion(raw: RawEnvironmentVersion): EnvironmentVersion {
  return {
    id: raw.ID,
    environment_id: raw.EnvironmentID,
    version: raw.Version,
    description: raw.Description,
    created_by: raw.CreatedBy ?? null,
    created_at: raw.CreatedAt,
  };
}

function normalizeEnvironmentVersionDetails(raw?: RawEnvironmentVersionDetails | null): EnvironmentVersionDetails | undefined {
  if (!raw) {
    return undefined;
  }
  return {
    version: normalizeEnvironmentVersion(raw.Version),
    variables: Array.isArray(raw.Variables) ? raw.Variables.map(normalizeEnvironmentVariable) : [],
  };
}

function normalizeEnvironmentDetails(raw?: RawEnvironmentDetails | null): EnvironmentDetails | null {
  if (!raw || !raw.Environment) {
    return null;
  }
  return {
    environment: normalizeEnvironment(raw.Environment),
    latest_version: normalizeEnvironmentVersionDetails(raw.LatestVersion),
  };
}

function normalizeEnvironmentAudit(raw: RawEnvironmentAudit): EnvironmentAudit {
  return {
    id: raw.ID,
    project_id: raw.ProjectID,
    environment_id: raw.EnvironmentID,
    version_id: raw.VersionID,
    actor_id: raw.ActorID,
    action: raw.Action,
    metadata: parseMetadata(raw.Metadata),
    created_at: raw.CreatedAt,
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
const DEFAULT_API_BASE_URL = 'http://localhost:4000';
const serverApiBase =
  globalProcess?.env?.API_INTERNAL_BASE_URL ??
  globalProcess?.env?.NEXT_PUBLIC_API_BASE_URL ??
  DEFAULT_API_BASE_URL;
const clientApiBase = globalProcess?.env?.NEXT_PUBLIC_API_BASE_URL ?? DEFAULT_API_BASE_URL;
export const API_BASE_URL = typeof window === 'undefined' ? serverApiBase : clientApiBase;

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

export async function listEnvironments(accessToken: string, projectId: string): Promise<EnvironmentDetails[]> {
  const environments = await request<RawEnvironmentDetails[]>(`/projects/${encodeURIComponent(projectId)}/environments`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
  return environments
    .map((entry) => normalizeEnvironmentDetails(entry))
    .filter((detail): detail is EnvironmentDetails => detail !== null);
}

export async function createEnvironment(
  accessToken: string,
  projectId: string,
  input: CreateEnvironmentInput,
): Promise<EnvironmentDetails> {
  const payload: Record<string, unknown> = {
    name: input.name.trim(),
  };
  if (input.slug) {
    payload.slug = input.slug.trim();
  }
  if (input.type) {
    payload.type = input.type.trim();
  }
  if (typeof input.protected === 'boolean') {
    payload.protected = input.protected;
  }
  if (typeof input.position === 'number' && Number.isFinite(input.position)) {
    payload.position = input.position;
  }

  const detail = await request<RawEnvironmentDetails>(`/projects/${encodeURIComponent(projectId)}/environments`, {
    method: 'POST',
    headers: authHeaders(accessToken),
    body: JSON.stringify(payload),
  });
  const normalized = normalizeEnvironmentDetails(detail);
  if (!normalized) {
    throw new Error('environment payload missing from API response');
  }
  return normalized;
}

export async function updateEnvironment(
  accessToken: string,
  projectId: string,
  environmentId: string,
  input: UpdateEnvironmentInput,
): Promise<EnvironmentDetails> {
  const payload: Record<string, unknown> = {};
  if (typeof input.name === 'string') {
    payload.name = input.name.trim();
  }
  if (typeof input.slug === 'string') {
    payload.slug = input.slug.trim();
  }
  if (typeof input.type === 'string') {
    payload.type = input.type.trim();
  }
  if (typeof input.protected === 'boolean') {
    payload.protected = input.protected;
  }
  if (typeof input.position === 'number' && Number.isFinite(input.position)) {
    payload.position = input.position;
  }

  const detail = await request<RawEnvironmentDetails>(
    `/projects/${encodeURIComponent(projectId)}/environments/${encodeURIComponent(environmentId)}`,
    {
      method: 'PATCH',
      headers: authHeaders(accessToken),
      body: JSON.stringify(payload),
    },
  );
  const normalized = normalizeEnvironmentDetails(detail);
  if (!normalized) {
    throw new Error('environment payload missing from API response');
  }
  return normalized;
}

export async function listEnvironmentVersions(
  accessToken: string,
  projectId: string,
  environmentId: string,
  limit?: number,
): Promise<EnvironmentVersion[]> {
  const search = new URLSearchParams();
  if (typeof limit === 'number' && limit > 0) {
    search.set('limit', String(limit));
  }
  const suffix = search.size ? `?${search.toString()}` : '';
  const versions = await request<RawEnvironmentVersion[] | null>(
    `/projects/${encodeURIComponent(projectId)}/environments/${encodeURIComponent(environmentId)}/versions${suffix}`,
    {
      method: 'GET',
      headers: authHeaders(accessToken),
    },
  );
  if (!Array.isArray(versions)) {
    return [];
  }
  return versions.map(normalizeEnvironmentVersion);
}

export async function createEnvironmentVersion(
  accessToken: string,
  projectId: string,
  environmentId: string,
  input: CreateEnvironmentVersionInput,
): Promise<EnvironmentVersionDetails> {
  const variables = Array.isArray(input.variables)
    ? input.variables
        .filter((variable): variable is EnvironmentVariableInput =>
          typeof variable?.key === 'string' && typeof variable?.value === 'string',
        )
        .map((variable) => ({
          key: variable.key,
          value: variable.value,
        }))
    : [];

  const payload = {
    description: input.description?.trim() ?? '',
    variables,
  };

  const raw = await request<RawEnvironmentVersionDetails>(
    `/projects/${encodeURIComponent(projectId)}/environments/${encodeURIComponent(environmentId)}/versions`,
    {
      method: 'POST',
      headers: authHeaders(accessToken),
      body: JSON.stringify(payload),
    },
  );
  const detail = normalizeEnvironmentVersionDetails(raw);
  if (!detail) {
    throw new ApiError('Invalid environment version response.', 502, raw);
  }
  return detail;
}

export async function getEnvironmentVersion(
  accessToken: string,
  projectId: string,
  environmentId: string,
  versionId: string,
): Promise<EnvironmentVersionDetails> {
  const raw = await request<RawEnvironmentVersionDetails>(
    `/projects/${encodeURIComponent(projectId)}/environments/${encodeURIComponent(environmentId)}/versions/${encodeURIComponent(versionId)}`,
    {
      method: 'GET',
      headers: authHeaders(accessToken),
    },
  );
  const detail = normalizeEnvironmentVersionDetails(raw);
  if (!detail) {
    throw new ApiError('Invalid environment version response.', 502, raw);
  }
  return detail;
}

export interface EnvironmentAuditQuery {
  environmentId?: string;
  limit?: number;
}

export async function listEnvironmentAudits(
  accessToken: string,
  projectId: string,
  query: EnvironmentAuditQuery = {},
): Promise<EnvironmentAudit[]> {
  const search = new URLSearchParams();
  const environmentId = sanitizeUUID(query.environmentId ?? null);
  if (environmentId) {
    search.set('environment_id', environmentId);
  }
  if (typeof query.limit === 'number' && query.limit > 0) {
    search.set('limit', String(query.limit));
  }
  const suffix = search.size ? `?${search.toString()}` : '';
  const audits = await request<RawEnvironmentAudit[] | null>(
    `/projects/${encodeURIComponent(projectId)}/environments/audits${suffix}`,
    {
      method: 'GET',
      headers: authHeaders(accessToken),
    },
  );
  if (!Array.isArray(audits)) {
    return [];
  }
  return audits.map(normalizeEnvironmentAudit);
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
