import Link from 'next/link';
import { type ReactNode } from 'react';

/**
 * DocsPage — reusable content wrapper for every docs page.
 *
 * Server component (no client hooks) so pages can stay server-rendered and
 * keep `export const metadata`. Renders a consistent header (title + optional
 * lede), the page body, and an optional prev/next footer.
 *
 * Prev/next are passed explicitly as `{ href, label }` rather than derived
 * from the layout's NAV array — this keeps DocsPage fully decoupled from the
 * client layout and gives page authors a dead-simple contract. Pair the prose
 * helpers below (DocsH2, DocsH3, DocsP, DocsList, InlineCode, DocsCallout)
 * with the shared `CodeBlock` for code so every page reads the same.
 */

type DocsLink = {
  href: string;
  label: string;
};

type DocsPageProps = {
  /** Page title — rendered as the single h1. */
  title: string;
  /** Optional eyebrow/section label above the title (e.g. "Getting Started"). */
  eyebrow?: string;
  /** Optional lede paragraph under the title. */
  lede?: ReactNode;
  /** Previous page in reading order. */
  prev?: DocsLink;
  /** Next page in reading order. */
  next?: DocsLink;
  children: ReactNode;
};

export function DocsPage({
  title,
  eyebrow,
  lede,
  prev,
  next,
  children,
}: DocsPageProps) {
  return (
    <article className="mx-auto w-full max-w-3xl">
      <header className="mb-10">
        {eyebrow ? (
          <p className="mb-3 font-mono text-xs font-medium uppercase tracking-wider text-saffron">
            {eyebrow}
          </p>
        ) : null}
        <h1 className="text-balance text-3xl font-semibold tracking-tight text-fg sm:text-4xl">
          {title}
        </h1>
        {lede ? (
          <p className="mt-4 text-pretty text-lg leading-relaxed text-muted">
            {lede}
          </p>
        ) : null}
      </header>

      {/* Body. Helpers below give consistent vertical rhythm and styling. */}
      <div className="space-y-5">{children}</div>

      {(prev || next) && (
        <nav
          aria-label="Pagination"
          className="mt-16 grid grid-cols-1 gap-3 border-t border-border pt-8 sm:grid-cols-2"
        >
          {prev ? (
            <Link
              href={prev.href}
              className="group flex flex-col rounded-xl border border-border bg-surface/40 px-4 py-3 transition-colors hover:border-border-strong hover:bg-surface focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-saffron/70"
            >
              <span className="inline-flex items-center gap-1.5 text-xs text-faint">
                <ArrowLeftIcon />
                Previous
              </span>
              <span className="mt-1 text-sm font-medium text-fg group-hover:text-saffron">
                {prev.label}
              </span>
            </Link>
          ) : (
            <span aria-hidden="true" className="hidden sm:block" />
          )}

          {next ? (
            <Link
              href={next.href}
              className="group flex flex-col rounded-xl border border-border bg-surface/40 px-4 py-3 text-right transition-colors hover:border-border-strong hover:bg-surface focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-saffron/70 sm:col-start-2"
            >
              <span className="inline-flex items-center justify-end gap-1.5 text-xs text-faint">
                Next
                <ArrowRightIcon />
              </span>
              <span className="mt-1 text-sm font-medium text-fg group-hover:text-saffron">
                {next.label}
              </span>
            </Link>
          ) : null}
        </nav>
      )}
    </article>
  );
}

/* --------------------------------------------------------------------------
 * Prose helpers — hand-styled (no Tailwind `prose` plugin dependency) so
 * every docs page renders consistent headings, paragraphs, lists, and inline
 * code. Use the shared CodeBlock component for fenced code.
 * ------------------------------------------------------------------------ */

/** Section heading (h2). */
export function DocsH2({
  id,
  children,
}: {
  id?: string;
  children: ReactNode;
}) {
  return (
    <h2
      id={id}
      className="scroll-mt-24 pt-6 text-xl font-semibold tracking-tight text-fg sm:text-2xl"
    >
      {children}
    </h2>
  );
}

/** Sub-section heading (h3). */
export function DocsH3({
  id,
  children,
}: {
  id?: string;
  children: ReactNode;
}) {
  return (
    <h3
      id={id}
      className="scroll-mt-24 pt-4 text-base font-semibold tracking-tight text-fg sm:text-lg"
    >
      {children}
    </h3>
  );
}

/** Body paragraph. */
export function DocsP({ children }: { children: ReactNode }) {
  return <p className="text-base leading-relaxed text-muted">{children}</p>;
}

/** Bulleted (default) or numbered list. Pass children as <li> elements. */
export function DocsList({
  children,
  ordered = false,
}: {
  children: ReactNode;
  ordered?: boolean;
}) {
  const className =
    'space-y-2 pl-5 text-base leading-relaxed text-muted marker:text-faint';
  return ordered ? (
    <ol className={`list-decimal ${className}`}>{children}</ol>
  ) : (
    <ul className={`list-disc ${className}`}>{children}</ul>
  );
}

/** A single list item. Children may include InlineCode, links, etc. */
export function DocsListItem({ children }: { children: ReactNode }) {
  return <li className="pl-1">{children}</li>;
}

/** Inline monospace code — matches the marketing site's inline-code styling. */
export function InlineCode({ children }: { children: ReactNode }) {
  return (
    <code className="rounded-md border border-border bg-bg-elevated px-1.5 py-0.5 font-mono text-[0.8125rem] text-fg">
      {children}
    </code>
  );
}

/** Internal docs link (client-routed). Use plain <a> for external links. */
export function DocsLinkInline({
  href,
  children,
}: {
  href: string;
  children: ReactNode;
}) {
  return (
    <Link
      href={href}
      className="font-medium text-blue underline decoration-blue/30 underline-offset-2 transition-colors hover:decoration-blue focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-saffron/70"
    >
      {children}
    </Link>
  );
}

type CalloutTone = 'note' | 'tip' | 'warn';

const CALLOUT_STYLES: Record<CalloutTone, { wrap: string; label: string }> = {
  note: {
    wrap: 'border-border-strong bg-surface/50',
    label: 'text-muted',
  },
  tip: {
    wrap: 'border-green/30 bg-green/5',
    label: 'text-green',
  },
  warn: {
    wrap: 'border-saffron/30 bg-saffron/5',
    label: 'text-saffron',
  },
};

const CALLOUT_DEFAULT_LABEL: Record<CalloutTone, string> = {
  note: 'Note',
  tip: 'Tip',
  warn: 'Heads up',
};

/** Highlighted aside for notes, tips, and warnings. */
export function DocsCallout({
  tone = 'note',
  title,
  children,
}: {
  tone?: CalloutTone;
  title?: string;
  children: ReactNode;
}) {
  const style = CALLOUT_STYLES[tone];
  return (
    <div className={`rounded-xl border p-4 sm:p-5 ${style.wrap}`}>
      <p
        className={`mb-1.5 font-mono text-xs font-medium uppercase tracking-wider ${style.label}`}
      >
        {title ?? CALLOUT_DEFAULT_LABEL[tone]}
      </p>
      <div className="text-sm leading-relaxed text-muted">{children}</div>
    </div>
  );
}

function ArrowLeftIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M19 12H5" />
      <path d="m12 19-7-7 7-7" />
    </svg>
  );
}

function ArrowRightIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M5 12h14" />
      <path d="m12 5 7 7-7 7" />
    </svg>
  );
}

export default DocsPage;
