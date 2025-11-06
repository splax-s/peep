export interface TokenPair {
  AccessToken: string;
  RefreshToken: string;
  ExpiresIn: number;
}

export interface User {
  id: string;
  email: string;
}

export interface SessionPayload {
  user: User;
  tokens: TokenPair;
}

export interface Team {
  id: string;
  name: string;
  owner_id: string;
  max_projects: number;
  max_containers: number;
  storage_limit_mb: number;
  created_at: string;
}

export interface TeamLimits {
  max_projects: number;
  max_containers: number;
  storage_limit_mb: number;
}

export interface Project {
  id: string;
  team_id: string;
  name: string;
  repo_url: string;
  type: string;
  build_command: string;
  run_command: string;
  created_at: string;
}

export interface ProjectEnvVar {
  key: string;
  value: string;
}

export interface Deployment {
  id: string;
  project_id: string;
  commit_sha: string;
  status: string;
  stage: string;
  message: string;
  url: string | null;
  error: string | null;
  metadata: Record<string, unknown> | null;
  started_at: string;
  completed_at: string | null;
  updated_at: string;
}

export interface ProjectLog {
  id: number;
  project_id: string;
  source: string;
  level: string;
  message: string;
  metadata: Record<string, unknown> | null;
  created_at: string;
}

export interface RuntimeMetricRollup {
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

export interface RuntimeEvent {
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
  metadata: Record<string, unknown> | null;
  occurred_at: string;
  ingested_at: string;
}

export interface CreateTeamInput {
  name: string;
  limits: TeamLimits;
}

export interface CreateProjectInput {
  TeamID: string;
  Name: string;
  RepoURL: string;
  Type: string;
  BuildCommand: string;
  RunCommand: string;
}
