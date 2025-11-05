'use server';

import { revalidatePath } from 'next/cache';
import { ApiError, addEnvVar, createProject, createTeam, deleteDeployment, triggerDeployment } from '@/lib/api';
import { getSession } from '@/lib/session';

export interface ActionResponse {
  success: boolean;
  error?: string;
}

export interface CreateTeamActionInput {
  name: string;
  maxProjects?: number;
  maxContainers?: number;
  storageLimitMb?: number;
}

export interface CreateProjectActionInput {
  teamId: string;
  name: string;
  repoUrl: string;
  type: 'frontend' | 'backend';
  buildCommand: string;
  runCommand: string;
}

export interface AddEnvVarActionInput {
  projectId: string;
  key: string;
  value: string;
}

export interface TriggerDeploymentActionInput {
  projectId: string;
  commit?: string;
}

export interface DeleteDeploymentActionInput {
  deploymentId: string;
}

function formatError(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return 'Request failed.';
}

export async function createTeamAction(input: CreateTeamActionInput): Promise<ActionResponse> {
  const session = await getSession();
  if (!session) {
    return { success: false, error: 'Not authenticated.' };
  }

  const name = input.name.trim();
  if (!name) {
    return { success: false, error: 'Team name is required.' };
  }

  const limits = {
    max_projects: input.maxProjects && input.maxProjects > 0 ? input.maxProjects : 5,
    max_containers: input.maxContainers && input.maxContainers > 0 ? input.maxContainers : 10,
    storage_limit_mb: input.storageLimitMb && input.storageLimitMb > 0 ? input.storageLimitMb : 10240,
  };

  try {
    await createTeam(session.tokens.AccessToken, { name, limits });
    revalidatePath('/');
    return { success: true };
  } catch (error) {
    return { success: false, error: formatError(error) };
  }
}

export async function createProjectAction(input: CreateProjectActionInput): Promise<ActionResponse> {
  const session = await getSession();
  if (!session) {
    return { success: false, error: 'Not authenticated.' };
  }

  const payload = {
    TeamID: input.teamId,
    Name: input.name.trim(),
    RepoURL: input.repoUrl.trim(),
    Type: input.type,
    BuildCommand: input.buildCommand.trim(),
    RunCommand: input.runCommand.trim(),
  };

  if (!payload.TeamID) {
    return { success: false, error: 'Select a team first.' };
  }

  if (!payload.Name) {
    return { success: false, error: 'Project name is required.' };
  }

  if (!payload.RepoURL) {
    return { success: false, error: 'Repository URL is required.' };
  }

  if (!payload.BuildCommand || !payload.RunCommand) {
    return { success: false, error: 'Build and run commands are required.' };
  }

  try {
    await createProject(session.tokens.AccessToken, payload);
    revalidatePath('/');
    return { success: true };
  } catch (error) {
    return { success: false, error: formatError(error) };
  }
}

export async function addEnvVarAction(input: AddEnvVarActionInput): Promise<ActionResponse> {
  const session = await getSession();
  if (!session) {
    return { success: false, error: 'Not authenticated.' };
  }

  const projectId = input.projectId.trim();
  const key = input.key.trim().toUpperCase();
  const value = input.value.trim();

  if (!projectId) {
    return { success: false, error: 'Select a project before adding secrets.' };
  }

  if (!key) {
    return { success: false, error: 'Key is required.' };
  }

  if (!value) {
    return { success: false, error: 'Value is required.' };
  }

  try {
    await addEnvVar(session.tokens.AccessToken, projectId, key, value);
    revalidatePath('/');
    return { success: true };
  } catch (error) {
    return { success: false, error: formatError(error) };
  }
}

export async function triggerDeploymentAction(input: TriggerDeploymentActionInput): Promise<ActionResponse> {
  const session = await getSession();
  if (!session) {
    return { success: false, error: 'Not authenticated.' };
  }

  const projectId = input.projectId.trim();
  const commit = input.commit?.trim();

  if (!projectId) {
    return { success: false, error: 'Select a project before triggering a deployment.' };
  }

  try {
    await triggerDeployment(session.tokens.AccessToken, projectId, commit);
    revalidatePath('/');
    return { success: true };
  } catch (error) {
    return { success: false, error: formatError(error) };
  }
}

export async function deleteDeploymentAction(input: DeleteDeploymentActionInput): Promise<ActionResponse> {
  const session = await getSession();
  if (!session) {
    return { success: false, error: 'Not authenticated.' };
  }

  const deploymentId = input.deploymentId.trim();
  if (!deploymentId) {
    return { success: false, error: 'Deployment identifier missing.' };
  }

  try {
    await deleteDeployment(session.tokens.AccessToken, deploymentId);
    revalidatePath('/');
    return { success: true };
  } catch (error) {
    return { success: false, error: formatError(error) };
  }
}
