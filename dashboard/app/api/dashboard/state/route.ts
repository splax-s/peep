'use server';

import { NextResponse } from 'next/server';
import {
  ApiError,
  getProject,
  listDeployments,
  listEnvVars,
  listLogs,
  listProjects,
  listTeams,
} from '@/lib/api';
import { DEPLOYMENTS_LIMIT, LOGS_PAGE_SIZE } from '@/lib/dashboard';
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

    const [project, envVars, deployments, logs] = activeProjectId
      ? await Promise.all([
          getProject(session.tokens.AccessToken, activeProjectId),
          listEnvVars(session.tokens.AccessToken, activeProjectId),
          listDeployments(session.tokens.AccessToken, activeProjectId, DEPLOYMENTS_LIMIT),
          listLogs(session.tokens.AccessToken, activeProjectId, LOGS_PAGE_SIZE, 0),
        ])
      : [null, [], [], []];
    const logsHasMore = logs.length === LOGS_PAGE_SIZE;
    const deploymentsHasMore = deployments.length === DEPLOYMENTS_LIMIT;

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
