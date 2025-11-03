'use client';

import {
  useEffect,
  useMemo,
  useState,
  useTransition,
  type ChangeEvent,
  type FormEvent,
} from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { addEnvVarAction, createProjectAction, createTeamAction } from '@/lib/actions/dashboard';
import { logout } from '@/lib/actions/auth';
import type { Project, ProjectEnvVar, Team } from '@/types';

interface DashboardClientProps {
  userEmail: string;
  teams: Team[];
  activeTeamId: string | null;
  projects: Project[];
  activeProjectId: string | null;
  project: Project | null;
  envVars: ProjectEnvVar[];
}

interface DashboardState {
  userEmail: string;
  teams: Team[];
  activeTeamId: string | null;
  projects: Project[];
  activeProjectId: string | null;
  project: Project | null;
  envVars: ProjectEnvVar[];
}

type ToastVariant = 'success' | 'error';

interface ToastState {
  id: number;
  message: string;
  variant: ToastVariant;
}

class UnauthorizedError extends Error {
  constructor() {
    super('unauthorized');
    this.name = 'UnauthorizedError';
  }
}

async function fetchDashboardState(teamId: string | null, projectId: string | null): Promise<DashboardState> {
  const params = new URLSearchParams();
  if (teamId) {
    params.set('team', teamId);
  }
  if (projectId) {
    params.set('project', projectId);
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

export function DashboardClient(props: DashboardClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [navigationPending, startNavigation] = useTransition();
  const [refreshTransitionPending, startRefreshTransition] = useTransition();
  const [logoutPending, startLogout] = useTransition();
  const [teamPending, startTeamAction] = useTransition();
  const [projectPending, startProjectAction] = useTransition();
  const [envPending, startEnvAction] = useTransition();

  const [state, setState] = useState<DashboardState>(() => ({
    userEmail: props.userEmail,
    teams: props.teams,
    activeTeamId: props.activeTeamId,
    projects: props.projects,
    activeProjectId: props.activeProjectId,
    project: props.project,
    envVars: props.envVars,
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
  const [projectBuildCommand, setProjectBuildCommand] = useState('npm install');
  const [projectRunCommand, setProjectRunCommand] = useState('npm run start');
  const [projectFormError, setProjectFormError] = useState<string | null>(null);

  const [envKey, setEnvKey] = useState('');
  const [envValue, setEnvValue] = useState('');
  const [envError, setEnvError] = useState<string | null>(null);

  useEffect(() => {
    setState({
      userEmail: props.userEmail,
      teams: props.teams,
      activeTeamId: props.activeTeamId,
      projects: props.projects,
      activeProjectId: props.activeProjectId,
      project: props.project,
      envVars: props.envVars,
    });
  }, [props.userEmail, props.teams, props.activeTeamId, props.projects, props.activeProjectId, props.project, props.envVars]);

  useEffect(() => {
    if (!toast) {
      return;
    }
    const timeoutId = window.setTimeout(() => setToast(null), 4000);
    return () => window.clearTimeout(timeoutId);
  }, [toast]);

  const selectedTeam = useMemo(() => {
    if (!state.activeTeamId) {
      return null;
    }
    return state.teams.find((team) => team.id === state.activeTeamId) ?? null;
  }, [state.activeTeamId, state.teams]);

  function showToast(message: string, variant: ToastVariant) {
    setToast({ id: Date.now(), message, variant });
  }

  async function reloadState(nextTeamId: string | null, nextProjectId: string | null, successMessage?: string) {
    setDataPending(true);
    try {
      const nextState = await fetchDashboardState(nextTeamId, nextProjectId);
      setState(nextState);
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
      envVars: [],
    }));
    startNavigation(() => {
      updateSearchParams({ team: teamId, project: null });
    });
    void reloadState(teamId, null);
  }

  function handleSelectProject(projectId: string) {
    if (projectId === state.activeProjectId) {
      return;
    }
    startNavigation(() => {
      updateSearchParams({ team: state.activeTeamId ?? null, project: projectId });
    });
    setState((prev) => ({
      ...prev,
      activeProjectId: projectId,
      project: prev.projects.find((project) => project.id === projectId) ?? prev.project,
      envVars: [],
    }));
    void reloadState(state.activeTeamId, projectId);
  }

  function handleRefresh() {
    startRefreshTransition(() => {
      void reloadState(state.activeTeamId, state.activeProjectId, 'Dashboard data refreshed.');
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
      setProjectBuildCommand('npm install');
      setProjectRunCommand('npm run start');
      setProjectFormOpen(false);
      await reloadState(state.activeTeamId, null, 'Project provisioned.');
    });
  }

  function handleAddEnvVar(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!state.activeProjectId) {
      setEnvError('Select a project first.');
      return;
    }
    const keyTrimmed = envKey.trim().toUpperCase();
    const valueTrimmed = envValue.trim();
    if (!keyTrimmed) {
      setEnvError('Key is required.');
      return;
    }
    if (!valueTrimmed) {
      setEnvError('Value is required.');
      return;
    }
    setEnvError(null);
    startEnvAction(async () => {
      const result = await addEnvVarAction({
        projectId: state.activeProjectId!,
        key: keyTrimmed,
        value: valueTrimmed,
      });
      if (!result.success) {
        setEnvError(result.error ?? 'Failed to save variable.');
        return;
      }
      setEnvKey('');
      setEnvValue('');
      await reloadState(state.activeTeamId, state.activeProjectId, 'Environment variable saved.');
    });
  }

  const disableInputs =
    navigationPending ||
    refreshTransitionPending ||
    logoutPending ||
    teamPending ||
    projectPending ||
    envPending ||
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
                        placeholder="npm install"
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
                      placeholder="npm run start"
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
              <section className="space-y-3">
                <div className="flex items-center justify-between">
                  <h3 className="text-sm font-semibold text-white">Environment variables</h3>
                  <span className="badge">{state.envVars.length} vars</span>
                </div>
                {state.envVars.length > 0 ? (
                  <ul className="space-y-2 text-sm text-slate-200">
                    {state.envVars.map((envVar) => (
                      <li
                        key={envVar.key}
                        className="flex items-center justify-between rounded-xl border border-white/10 bg-slate-900/70 px-3 py-2"
                      >
                        <span className="font-semibold">{envVar.key}</span>
                        <span className="font-mono text-xs text-slate-400">{envVar.value}</span>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="muted">No variables stored yet.</p>
                )}
                <form className="space-y-3" onSubmit={handleAddEnvVar}>
                  <div className="grid gap-3 md:grid-cols-2">
                    <label className="block text-sm text-slate-200">
                      Key
                      <input
                        className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                        value={envKey}
                        onChange={(event: ChangeEvent<HTMLInputElement>) => setEnvKey(event.target.value.toUpperCase())}
                        placeholder="API_KEY"
                        required
                        disabled={disableInputs}
                      />
                    </label>
                    <label className="block text-sm text-slate-200">
                      Value
                      <input
                        className="mt-1 w-full rounded-xl border border-white/10 bg-slate-900/80 px-3 py-2 text-sm text-slate-100 focus:border-emerald-400 focus:outline-none focus:ring-2 focus:ring-emerald-500/40"
                        value={envValue}
                        onChange={(event: ChangeEvent<HTMLInputElement>) => setEnvValue(event.target.value)}
                        placeholder="super-secret"
                        required
                        disabled={disableInputs}
                      />
                    </label>
                  </div>
                  {envError ? (
                    <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">
                      {envError}
                    </p>
                  ) : null}
                  <button
                    type="submit"
                    disabled={envPending || disableInputs}
                    className="flex w-full items-center justify-center gap-2 rounded-xl bg-emerald-400 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-emerald-300 focus:outline-none focus:ring-4 focus:ring-emerald-400/40 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {envPending ? 'Saving…' : 'Save variable'}
                  </button>
                </form>
              </section>
            </div>
          ) : null}
        </article>
      </section>
    </div>
  );
}
