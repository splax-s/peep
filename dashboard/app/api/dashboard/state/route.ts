'use server';

import { NextResponse } from 'next/server';
import { ApiError, getProject, listEnvVars, listProjects, listTeams } from '@/lib/api';
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

    const [project, envVars] = activeProjectId
      ? await Promise.all([
          getProject(session.tokens.AccessToken, activeProjectId),
          listEnvVars(session.tokens.AccessToken, activeProjectId),
        ])
      : [null, []];

    return NextResponse.json(
      {
        userEmail: session.user.email,
        teams,
        activeTeamId,
        projects,
        activeProjectId,
        project,
        envVars,
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
