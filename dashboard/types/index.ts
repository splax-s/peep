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
