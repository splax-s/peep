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
