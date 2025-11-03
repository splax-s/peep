import type { Metadata } from 'next';
import type { ReactNode } from 'react';
import './globals.css';

export const metadata: Metadata = {
  title: 'Peep Dashboard',
  description: 'Control plane for managing projects and deployments in peep.',
};

export default function RootLayout({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <html lang="en">
      <body className="bg-radial-dusk min-h-screen">
        <div className="bg-gradient-to-b from-slate-950/60 to-slate-950/95">
          {children}
        </div>
      </body>
    </html>
  );
}
