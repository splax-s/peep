import type {
  CreateProjectInput,
  CreateTeamInput,
  Project,
  ProjectEnvVar,
  SessionPayload,
  Team,
} from '@/types';

const globalProcess = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process;
const API_BASE_URL = globalProcess?.env?.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:4000';

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
  return request<Team[]>('/teams', {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
}

export async function listProjects(accessToken: string, teamId: string): Promise<Project[]> {
  const search = new URLSearchParams({ team_id: teamId });
  return request<Project[]>(`/projects?${search.toString()}`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
}

export async function createTeam(accessToken: string, input: CreateTeamInput): Promise<Team> {
  return request<Team>('/teams', {
    method: 'POST',
    headers: authHeaders(accessToken),
    body: JSON.stringify(input),
  });
}

export async function createProject(accessToken: string, input: CreateProjectInput): Promise<Project> {
  return request<Project>('/projects', {
    method: 'POST',
    headers: authHeaders(accessToken),
    body: JSON.stringify(input),
  });
}

export async function getProject(accessToken: string, projectId: string): Promise<Project> {
  return request<Project>(`/projects/${encodeURIComponent(projectId)}`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
}

export async function listEnvVars(accessToken: string, projectId: string): Promise<ProjectEnvVar[]> {
  return request<ProjectEnvVar[]>(`/projects/${encodeURIComponent(projectId)}/env`, {
    method: 'GET',
    headers: authHeaders(accessToken),
  });
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
