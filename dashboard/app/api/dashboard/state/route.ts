'use server';

import { NextResponse } from 'next/server';
import {
  ApiError,
  getProject,
  listDeployments,
  listEnvironments,
  listEnvironmentAudits,
  listEnvironmentVersions,
  listLogs,
  listRuntimeEvents,
  listRuntimeRollups,
  listProjects,
  listTeams,
} from '@/lib/api';
import {
  DEPLOYMENTS_LIMIT,
  ENVIRONMENT_AUDIT_LIMIT,
  ENVIRONMENT_VERSIONS_LIMIT,
  LOGS_PAGE_SIZE,
  RUNTIME_EVENTS_PAGE_SIZE,
  RUNTIME_METRIC_BUCKET_SECONDS,
  RUNTIME_METRICS_LIMIT,
} from '@/lib/dashboard';
import { clearSession, getSession } from '@/lib/session';

export async function GET(request: Request) {
  const session = await getSession();
  if (!session) {
    return NextResponse.json({ error: 'Not authenticated.' }, { status: 401 });
  }

  const url = new URL(request.url);
  const teamParam = url.searchParams.get('team');
  const projectParam = url.searchParams.get('project');

  try {
    const teams = await listTeams(session.tokens.AccessToken);
    const activeTeamId = teams.some((team) => team.id === teamParam) ? teamParam : teams[0]?.id ?? null;

    const projects = activeTeamId ? await listProjects(session.tokens.AccessToken, activeTeamId) : [];
    const activeProjectId = projects.some((project) => project.id === projectParam)
      ? projectParam
      : projects[0]?.id ?? null;

    let project: Awaited<ReturnType<typeof getProject>> | null = null;
    let environments: Awaited<ReturnType<typeof listEnvironments>> = [];
    let deployments: Awaited<ReturnType<typeof listDeployments>> = [];
    let logs: Awaited<ReturnType<typeof listLogs>> = [];
    let runtimeRollups: Awaited<ReturnType<typeof listRuntimeRollups>> = [];
    let runtimeEvents: Awaited<ReturnType<typeof listRuntimeEvents>> = [];
    let activeEnvironmentId: string | null = null;
    let environmentVersions: Awaited<ReturnType<typeof listEnvironmentVersions>> = [];
    let environmentAudits: Awaited<ReturnType<typeof listEnvironmentAudits>> = [];

    if (activeProjectId) {
      [project, environments, deployments, logs, runtimeRollups, runtimeEvents] = await Promise.all([
        getProject(session.tokens.AccessToken, activeProjectId),
        listEnvironments(session.tokens.AccessToken, activeProjectId),
        listDeployments(session.tokens.AccessToken, activeProjectId, DEPLOYMENTS_LIMIT),
        listLogs(session.tokens.AccessToken, activeProjectId, LOGS_PAGE_SIZE, 0),
        listRuntimeRollups(session.tokens.AccessToken, activeProjectId, {
          bucketSpanSeconds: RUNTIME_METRIC_BUCKET_SECONDS,
          limit: RUNTIME_METRICS_LIMIT,
        }),
        listRuntimeEvents(session.tokens.AccessToken, activeProjectId, {
          limit: RUNTIME_EVENTS_PAGE_SIZE,
          offset: 0,
        }),
      ]);

      const environmentParam = url.searchParams.get('environment');
      activeEnvironmentId = environments.some((candidate) => candidate.environment.id === environmentParam)
        ? environmentParam
        : environments[0]?.environment.id ?? null;

      if (activeEnvironmentId) {
        [environmentVersions, environmentAudits] = await Promise.all([
          listEnvironmentVersions(
            session.tokens.AccessToken,
            activeProjectId,
            activeEnvironmentId,
            ENVIRONMENT_VERSIONS_LIMIT,
          ),
          listEnvironmentAudits(session.tokens.AccessToken, activeProjectId, {
            environmentId: activeEnvironmentId,
            limit: ENVIRONMENT_AUDIT_LIMIT,
          }),
        ]);
      } else {
        environmentAudits = await listEnvironmentAudits(session.tokens.AccessToken, activeProjectId, {
          limit: ENVIRONMENT_AUDIT_LIMIT,
        });
      }
    }

    const logsHasMore = logs.length === LOGS_PAGE_SIZE;
    const deploymentsHasMore = deployments.length === DEPLOYMENTS_LIMIT;
    const runtimeEventsHasMore = runtimeEvents.length === RUNTIME_EVENTS_PAGE_SIZE;

    return NextResponse.json(
      {
        userEmail: session.user.email,
        teams,
        activeTeamId,
        projects,
        activeProjectId,
        project,
        environments,
        activeEnvironmentId,
        environmentVersions,
        environmentAudits,
        deployments,
        logs,
        logsHasMore,
        deploymentsHasMore,
        runtimeRollups,
        runtimeEvents,
        runtimeEventsHasMore,
      },
      {
        status: 200,
        headers: {
          'Cache-Control': 'no-store',
        },
      },
    );
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      await clearSession();
      return NextResponse.json({ error: 'Not authenticated.' }, { status: 401 });
    }
    const message = error instanceof Error ? error.message : 'Failed to fetch dashboard data.';
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
