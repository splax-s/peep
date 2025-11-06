import { LoginForm } from '@/components/LoginForm';
import { DashboardClient } from '@/components/DashboardClient';
import {
  ApiError,
  getProject,
  listDeployments,
  listEnvVars,
  listLogs,
  listProjects,
  listRuntimeEvents,
  listRuntimeRollups,
  listTeams,
} from '@/lib/api';
import {
  DEPLOYMENTS_LIMIT,
  LOGS_PAGE_SIZE,
  RUNTIME_EVENTS_PAGE_SIZE,
  RUNTIME_METRIC_BUCKET_SECONDS,
  RUNTIME_METRICS_LIMIT,
} from '@/lib/dashboard';
import { getSession } from '@/lib/session';

type SearchParamsInput = Record<string, string | string[] | undefined> | Promise<Record<string, string | string[] | undefined>>;

interface PageProps {
  searchParams?: SearchParamsInput;
}

function isPromise<T>(value: PromiseLike<T> | T): value is PromiseLike<T> {
  return typeof value === 'object' && value !== null && 'then' in value && typeof (value as PromiseLike<T>).then === 'function';
}

async function resolveSearchParams(
  params?: SearchParamsInput,
): Promise<Record<string, string | string[] | undefined> | undefined> {
  if (!params) {
    return undefined;
  }
  if (isPromise(params)) {
    return params;
  }
  return params;
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
    const params = await resolveSearchParams(searchParams);
    const teams = await listTeams(session.tokens.AccessToken);
    const teamParam = params?.team;
    const teamFromQuery = typeof teamParam === 'string' ? teamParam : null;
    const activeTeamId = teams.some((team) => team.id === teamFromQuery)
      ? teamFromQuery
      : teams[0]?.id ?? null;

    const projects = activeTeamId ? await listProjects(session.tokens.AccessToken, activeTeamId) : [];
    const projectParam = params?.project;
    const projectFromQuery = typeof projectParam === 'string' ? projectParam : null;
    const activeProjectId = projects.some((project) => project.id === projectFromQuery)
      ? projectFromQuery
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
    const logsHasMore = activeProjectId ? logs.length === LOGS_PAGE_SIZE : false;
    const deploymentsHasMore = activeProjectId ? deployments.length === DEPLOYMENTS_LIMIT : false;
    const runtimeEventsHasMore = activeProjectId ? runtimeEvents.length === RUNTIME_EVENTS_PAGE_SIZE : false;

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
          deployments={deployments}
          logs={logs}
          logsHasMore={logsHasMore}
          deploymentsHasMore={deploymentsHasMore}
          runtimeRollups={runtimeRollups}
          runtimeEvents={runtimeEvents}
          runtimeEventsHasMore={runtimeEventsHasMore}
        />
      </main>
    );
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 401) {
        return (
          <main className="flex min-h-screen items-center justify-center px-4 py-12">
            <LoginForm />
          </main>
        );
      }
      if (error.status === 429) {
        const message =
          typeof (error.payload as { error?: string } | null)?.error === 'string'
            ? (error.payload as { error: string }).error
            : 'API rate limit exceeded. Please try again shortly.';
        return (
          <main className="flex min-h-screen items-center justify-center px-4 py-12">
            <div className="glass-card max-w-lg space-y-4 px-6 py-6 text-center">
              <h1 className="text-2xl font-semibold text-white">Temporarily throttled</h1>
              <p className="text-sm text-slate-300">{message}</p>
              <p className="text-xs text-slate-500">
                The dashboard will be available again once the rate limit cools down. You can refresh the page to retry.
              </p>
            </div>
          </main>
        );
      }
    }
    throw error;
  }
}
