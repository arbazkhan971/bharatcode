import { type ReactNode } from 'react';

import { Badge } from '../ui/Badge';
import { Section } from '../ui/Section';

/**
 * Footer — site footer / `contentinfo` landmark.
 *
 * Server component (no client hooks): all copy is literal and the year is
 * resolved at build time, so it static-exports cleanly. `<Section as="footer">`
 * provides the landmark plus the shared gutters/max-width used site-wide.
 *
 * Link targets are intentionally repo-anchored so every href resolves today
 * (no dead `#` placeholders): the repo, its docs/ tree, its roadmap doc, and
 * the MIT license file.
 */

const REPO = 'https://github.com/arbazkhan971/bharatcode';

type FooterLink = {
  label: string;
  href: string;
  /** External links open in a new tab and get rel="noreferrer". */
  external?: boolean;
};

const LINKS: FooterLink[] = [
  { label: 'GitHub', href: REPO, external: true },
  { label: 'Docs', href: '/docs/' },
  { label: 'Roadmap', href: `${REPO}/blob/main/docs/ROADMAP.md`, external: true },
  { label: 'License (MIT)', href: `${REPO}/blob/main/LICENSE`, external: true },
];

export function Footer() {
  const year = new Date().getFullYear();

  return (
    <Section
      as="footer"
      width="content"
      spacing="none"
      className="border-t border-border bg-bg-elevated"
      innerClassName="py-12 sm:py-16"
    >
      <div className="flex flex-col gap-10 md:flex-row md:items-start md:justify-between">
        {/* Brand block */}
        <div className="max-w-sm">
          <a
            href="#top"
            className="inline-flex items-baseline gap-2 rounded-md font-mono text-lg font-semibold tracking-tight text-fg transition-colors hover:text-saffron focus-visible:text-saffron"
          >
            <span aria-hidden="true" className="text-saffron">
              &gt;_
            </span>
            BharatCode
          </a>

          <p className="mt-3 text-sm font-medium text-muted">
            OpenCode for India — a Go-native, MIT-licensed, open-weight-first CLI
            coding agent.
          </p>

          <p className="mt-3 text-sm leading-relaxed text-faint">
            An open-source terminal AI coding agent where your data stays in
            India. Runs locally, connects only to the models you choose.
          </p>

          <div className="mt-5">
            <Badge variant="green" size="sm" dot>
              MIT licensed
            </Badge>
          </div>
        </div>

        {/* Link group */}
        <nav aria-label="Footer" className="md:text-right">
          <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-faint">
            Project
          </h2>
          <ul className="mt-4 flex flex-col gap-3 md:items-end">
            {LINKS.map((link) => (
              <li key={link.label}>
                <a
                  href={link.href}
                  {...(link.external
                    ? { target: '_blank', rel: 'noreferrer' }
                    : {})}
                  className="group inline-flex items-center gap-2 rounded-md text-sm text-muted transition-colors hover:text-fg focus-visible:text-fg"
                >
                  {link.label === 'GitHub' ? (
                    <GitHubIcon className="h-4 w-4 shrink-0 text-faint transition-colors group-hover:text-fg" />
                  ) : null}
                  <span>{link.label}</span>
                  {link.external ? (
                    <ArrowUpRightIcon className="h-3.5 w-3.5 shrink-0 text-faint opacity-0 transition-opacity group-hover:opacity-100" />
                  ) : null}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      </div>

      {/* Bottom bar */}
      <div className="mt-12 flex flex-col gap-4 border-t border-border pt-6 text-xs text-faint sm:flex-row sm:items-center sm:justify-between">
        <p>
          &copy; {year} BharatCode &middot;{' '}
          <span className="font-mono text-muted">bharatcode.dev</span>
        </p>

        <p className="inline-flex items-center gap-1.5 font-mono">
          <span>Built with</span>
          <span className="text-blue">Go</span>
          <span aria-hidden="true">+</span>
          <span className="text-saffron">Bubble Tea</span>
        </p>
      </div>
    </Section>
  );
}

/* --- Icons: inline SVGs matching the scaffold's conventions. No icon dep. --- */

function GitHubIcon({ className }: { className?: string }): ReactNode {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d="M12 2C6.477 2 2 6.486 2 12.02c0 4.428 2.865 8.184 6.839 9.51.5.092.682-.218.682-.483 0-.238-.009-.868-.014-1.704-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.467-1.11-1.467-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.531 2.341 1.089 2.91.833.092-.647.35-1.089.636-1.34-2.221-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.563 9.563 0 0 1 12 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.203 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.338 4.695-4.566 4.943.359.31.678.921.678 1.856 0 1.34-.012 2.421-.012 2.751 0 .268.18.58.688.482A10.02 10.02 0 0 0 22 12.02C22 6.486 17.523 2 12 2Z" />
    </svg>
  );
}

function ArrowUpRightIcon({ className }: { className?: string }): ReactNode {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <path d="M7 17 17 7" />
      <path d="M7 7h10v10" />
    </svg>
  );
}

export default Footer;
