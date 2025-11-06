'use server';

import { NextResponse } from 'next/server';
import {
  ApiError,
  getProject,
  listDeployments,
  listEnvVars,
  listLogs,
  listRuntimeEvents,
  listRuntimeRollups,
  listProjects,
  listTeams,
} from '@/lib/api';
import {
  DEPLOYMENTS_LIMIT,
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

    const [project, envVars, deployments, logs, runtimeRollups, runtimeEvents] = activeProjectId
      ? await Promise.all([
          getProject(session.tokens.AccessToken, activeProjectId),
          listEnvVars(session.tokens.AccessToken, activeProjectId),
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
        ])
      : [
          null,
          [] as Awaited<ReturnType<typeof listEnvVars>>,
          [] as Awaited<ReturnType<typeof listDeployments>>,
          [] as Awaited<ReturnType<typeof listLogs>>,
          [] as Awaited<ReturnType<typeof listRuntimeRollups>>,
          [] as Awaited<ReturnType<typeof listRuntimeEvents>>,
        ];
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
        envVars,
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
