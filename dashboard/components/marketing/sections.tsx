'use client';

import { useState } from 'react';
import { motion, useReducedMotion } from 'framer-motion';
import Link from 'next/link';

interface Feature {
  title: string;
  description: string;
}

interface FaqItem {
  question: string;
  answer: string;
}

const features: Feature[] = [
  {
    title: 'Runtime telemetry without toil',
    description: 'Capture latency percentiles, saturation, and error budgets with one SDK that ships alongside every deploy.',
  },
  {
    title: 'Environment-aware rollouts',
    description: 'Model dev, staging, and prod with immutable version history and instant rollbacks backed by Kubernetes primitives.',
  },
  {
    title: 'CLI for modern platform teams',
    description: 'Automate deploys, tail logs, and manage secrets via a Go-native CLI with device-code auth and GitHub Releases.',
  },
];

const faqs: FaqItem[] = [
  {
    question: 'When will the beta open?',
    answer: 'We are targeting a staggered invite rollout aligned with the telemetry pipeline milestone. Join the waitlist for updates.',
  },
  {
    question: 'How does peep integrate with existing clusters?',
    answer: 'Deploy the peep control plane into Minikube or production Kubernetes clusters using the provided manifests—no sidecars or mesh required.',
  },
  {
    question: 'What differentiates peep from other platforms?',
    answer: 'peep treats runtime insights as a first-class surface, unifying deployments, observability, and environment policy in one workflow.',
  },
];

const stats = [
  { label: 'Telemetry events processed', value: '2.4M / hr' },
  { label: 'Median deploy time', value: '6.2 min' },
  { label: 'Config drift detected', value: '0 incidents' },
  { label: 'CLI install footprint', value: '< 20 MB' },
];

export function HeroSection() {
  const prefersReducedMotion = useReducedMotion();
  return (
    <section className="relative overflow-hidden px-4 pb-24 pt-20 md:px-12">
      <div className="absolute inset-0 -z-10 bg-[radial-gradient(circle_at_top,rgba(16,186,242,0.14),transparent_70%)]" />
      <motion.div
        className="mx-auto max-w-5xl text-center"
        initial={prefersReducedMotion ? undefined : { opacity: 0, y: 16 }}
        animate={prefersReducedMotion ? undefined : { opacity: 1, y: 0 }}
        transition={prefersReducedMotion ? undefined : { duration: 0.8, ease: 'easeOut' }}
      >
        <span className="inline-flex items-center rounded-full bg-cyan-500/10 px-4 py-1 text-xs font-semibold uppercase tracking-wide text-cyan-200">
          Phase 5 preview
        </span>
        <h1 className="mt-6 text-balance text-4xl font-semibold text-white sm:text-5xl lg:text-6xl">
          Observability and deployments finally converge.
        </h1>
        <p className="mt-6 text-pretty text-base text-slate-300 sm:text-lg">
          peep is the control plane for platform teams who ship fast and sleep soundly. Model environments, stream runtime events, and deploy with confidence from one place.
        </p>
        <div className="mt-8 flex flex-col items-center justify-center gap-4 sm:flex-row">
          <Link
            href="/app"
            className="inline-flex items-center justify-center rounded-xl bg-cyan-500 px-6 py-3 text-base font-semibold text-slate-950 transition hover:bg-cyan-400 focus:outline-none focus:ring-4 focus:ring-cyan-500/40"
          >
            Launch console
          </Link>
          <Link
            href="mailto:hello@peep.com"
            className="inline-flex items-center justify-center rounded-xl border border-slate-700/80 px-6 py-3 text-base font-semibold text-slate-200 transition hover:border-slate-600 hover:text-white focus:outline-none focus:ring-4 focus:ring-slate-600/30"
          >
            Join the waitlist
          </Link>
        </div>
      </motion.div>
    </section>
  );
}

export function FeatureHighlights() {
  const prefersReducedMotion = useReducedMotion();
  const fadeIn = (delay: number) => {
    if (prefersReducedMotion) {
      return {};
    }
    return {
      initial: { opacity: 0, y: 12 },
      whileInView: { opacity: 1, y: 0 },
      viewport: { once: true, margin: '-80px' },
      transition: { duration: 0.6, delay },
    } as const;
  };

  return (
    <section className="px-4 py-16 md:px-12">
      <div className="mx-auto grid max-w-5xl gap-8 md:grid-cols-3">
        {features.map((feature, index) => (
          <motion.article
            key={feature.title}
            className="glass-card h-full space-y-4 p-6"
            {...fadeIn(index * 0.12)}
          >
            <div className="badge badge-muted">{String(index + 1).padStart(2, '0')}</div>
            <h2 className="text-lg font-semibold text-white">{feature.title}</h2>
            <p className="text-sm text-slate-300">{feature.description}</p>
          </motion.article>
        ))}
      </div>
    </section>
  );
}

export function StatsMarquee() {
  const prefersReducedMotion = useReducedMotion();
  return (
    <section className="border-y border-slate-800/80 bg-slate-950/60 py-10">
      <div className="relative mx-auto flex max-w-5xl overflow-hidden">
        <motion.div
          className="flex min-w-full shrink-0 items-center justify-around gap-12 text-center text-sm text-slate-300 sm:text-base"
          animate={prefersReducedMotion ? undefined : { x: ['0%', '-50%'] }}
          transition={prefersReducedMotion ? undefined : { duration: 18, repeat: Infinity, ease: 'linear' }}
        >
          {[...stats, ...stats].map((stat, index) => (
            <div key={`${stat.label}-${index}`} className="space-y-2">
              <p className="text-xs uppercase tracking-wide text-slate-500">{stat.label}</p>
              <p className="text-lg font-semibold text-white">{stat.value}</p>
            </div>
          ))}
        </motion.div>
      </div>
    </section>
  );
}

export function FaqSection() {
  const prefersReducedMotion = useReducedMotion();
  const fadeIn = (delay: number) => {
    if (prefersReducedMotion) {
      return {};
    }
    return {
      initial: { opacity: 0, y: 12 },
      whileInView: { opacity: 1, y: 0 },
      viewport: { once: true, margin: '-80px' },
      transition: { duration: 0.6, delay },
    } as const;
  };
  const [activeIndex, setActiveIndex] = useState<number | null>(null);

  return (
    <section className="px-4 py-16 md:px-12">
      <div className="mx-auto flex max-w-4xl flex-col gap-10 md:flex-row">
        <motion.div className="md:w-1/3" {...fadeIn(0)}>
          <h2 className="text-2xl font-semibold text-white">Frequently asked</h2>
          <p className="mt-3 text-sm text-slate-300">
            Placeholder roadmap details while we finalize pricing, packaging, and general availability plans.
          </p>
        </motion.div>
        <div className="flex-1 space-y-4">
          {faqs.map((item, index) => {
            const isActive = activeIndex === index;
            return (
              <motion.article
                key={item.question}
                className="glass-card overflow-hidden"
                {...fadeIn(index * 0.1)}
              >
                <button
                  type="button"
                  onClick={() => setActiveIndex(isActive ? null : index)}
                  className="flex w-full items-center justify-between px-6 py-5 text-left"
                >
                  <span className="text-base font-medium text-white">{item.question}</span>
                  <span className="text-cyan-300">{isActive ? '−' : '+'}</span>
                </button>
                <motion.div
                  initial={false}
                  animate={{ height: isActive ? 'auto' : 0, opacity: isActive ? 1 : 0 }}
                  transition={{ duration: 0.25 }}
                >
                  <p className="px-6 pb-6 text-sm text-slate-300">{item.answer}</p>
                </motion.div>
              </motion.article>
            );
          })}
        </div>
      </div>
    </section>
  );
}

export function FooterCta() {
  const prefersReducedMotion = useReducedMotion();
  const fadeIn = (delay: number) => {
    if (prefersReducedMotion) {
      return {};
    }
    return {
      initial: { opacity: 0, y: 12 },
      whileInView: { opacity: 1, y: 0 },
      viewport: { once: true, margin: '-80px' },
      transition: { duration: 0.6, delay },
    } as const;
  };

  return (
    <section className="px-4 pb-24 pt-16 md:px-12">
      <motion.div
        className="glass-card relative mx-auto max-w-4xl overflow-hidden px-8 py-12 text-center"
        {...fadeIn(0)}
      >
        <div className="absolute inset-0 -z-10 bg-gradient-to-r from-cyan-500/15 via-fuchsia-500/10 to-indigo-500/15" />
        <h2 className="text-3xl font-semibold text-white">Ready to preview peep?</h2>
        <p className="mt-4 text-sm text-slate-300">
          Request an invite and we will reach out once the beta cohort opens. Existing users can jump straight into the console.
        </p>
        <div className="mt-8 flex flex-col items-center justify-center gap-4 sm:flex-row">
          <Link
            href="mailto:hello@peep.com"
            className="inline-flex items-center justify-center rounded-xl bg-cyan-500 px-6 py-3 text-base font-semibold text-slate-950 transition hover:bg-cyan-400 focus:outline-none focus:ring-4 focus:ring-cyan-500/40"
          >
            Request invite
          </Link>
          <Link
            href="/app"
            className="inline-flex items-center justify-center rounded-xl border border-slate-700/80 px-6 py-3 text-base font-semibold text-slate-200 transition hover:border-slate-600 hover:text-white focus:outline-none focus:ring-4 focus:ring-slate-600/30"
          >
            Launch console
          </Link>
        </div>
      </motion.div>
    </section>
  );
}
