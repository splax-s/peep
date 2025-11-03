import { LoginForm } from '@/components/LoginForm';
import { DashboardClient } from '@/components/DashboardClient';
import { ApiError, getProject, listEnvVars, listProjects, listTeams } from '@/lib/api';
import { getSession } from '@/lib/session';

interface PageProps {
  searchParams?: Record<string, string | string[] | undefined>;
}

export default async function DashboardPage({ searchParams }: PageProps) {
  const session = await getSession();

  if (!session) {
    return (
      <main className="flex min-h-screen items-center justify-center px-4 py-12">
        <LoginForm />
      </main>
    );
  }

  try {
    const teams = await listTeams(session.tokens.AccessToken);
    const teamParam = searchParams?.team;
    const teamFromQuery = typeof teamParam === 'string' ? teamParam : null;
    const activeTeamId = teams.some((team) => team.id === teamFromQuery)
      ? teamFromQuery
      : teams[0]?.id ?? null;

    const projects = activeTeamId ? await listProjects(session.tokens.AccessToken, activeTeamId) : [];
    const projectParam = searchParams?.project;
    const projectFromQuery = typeof projectParam === 'string' ? projectParam : null;
    const activeProjectId = projects.some((project) => project.id === projectFromQuery)
      ? projectFromQuery
      : projects[0]?.id ?? null;
    const [project, envVars] = activeProjectId
      ? await Promise.all([
          getProject(session.tokens.AccessToken, activeProjectId),
          listEnvVars(session.tokens.AccessToken, activeProjectId),
        ])
      : [null, [] as Awaited<ReturnType<typeof listEnvVars>>];

    return (
      <main className="px-4 py-10 md:px-8">
        <DashboardClient
          userEmail={session.user.email}
          teams={teams}
          activeTeamId={activeTeamId}
          projects={projects}
          activeProjectId={activeProjectId}
          project={project}
          envVars={envVars}
        />
      </main>
    );
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      return (
        <main className="flex min-h-screen items-center justify-center px-4 py-12">
          <LoginForm />
        </main>
      );
    }
    throw error;
  }
}
