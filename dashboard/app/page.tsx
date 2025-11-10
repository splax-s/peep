import type { Metadata } from 'next';
import {
  FeatureHighlights,
  FaqSection,
  FooterCta,
  HeroSection,
  StatsMarquee,
} from '@/components/marketing/sections';
import { redirect } from 'next/navigation';

type LandingSearchParams = Record<string, string | string[] | undefined>;

const DASHBOARD_QUERY_KEYS = new Set([
  'team',
  'project',
  'environment',
  'logsOffset',
  'deploymentsOffset',
  'runtimeOffset',
  'tab',
]);

export const metadata: Metadata = {
  title: 'peep — Runtime control plane for platform teams',
  description:
    'peep unifies runtime telemetry, deployments, and environment policy with an end-to-end control plane built for Kubernetes-first teams.',
};

export default function LandingPage({ searchParams }: { searchParams?: LandingSearchParams }) {
  if (searchParams) {
    const entries = Object.entries(searchParams);
    const shouldRedirect = entries.some(([key, value]) => {
      if (!value) {
        return false;
      }
      return DASHBOARD_QUERY_KEYS.has(key);
    });

    if (shouldRedirect) {
      const query = new URLSearchParams();
      for (const [key, value] of entries) {
        if (!value) {
          continue;
        }
        if (Array.isArray(value)) {
          value.forEach((item) => {
            if (typeof item === 'string') {
              query.append(key, item);
            }
          });
          continue;
        }
        query.set(key, value);
      }

      const queryString = query.toString();
      redirect(queryString ? `/app?${queryString}` : '/app');
    }
  }

  return (
    <main className="isolate">
      <div className="absolute inset-x-0 top-0 -z-10 h-[680px] bg-[radial-gradient(circle_at_top,rgba(56,189,248,0.16),rgba(14,165,233,0.03),transparent_75%)]" />
      <HeroSection />
      <StatsMarquee />
      <FeatureHighlights />
      <FaqSection />
      <FooterCta />
    </main>
  );
}
