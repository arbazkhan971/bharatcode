'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useCallback, useEffect, useState, type ReactNode } from 'react';

/**
 * Docs shell — left sidebar navigation + main content area.
 *
 * This layout is a client component because it needs `usePathname()` to
 * highlight the active route and `useState` for the mobile drawer toggle.
 * That's fine for a layout: `{children}` arrives as a prop, so the docs
 * *pages* it wraps still render as server components and can keep
 * `export const metadata`.
 *
 * Navigation is driven by a single source-of-truth `NAV` array so the
 * sidebar (desktop) and drawer (mobile) stay in lockstep. Active-route
 * detection normalizes trailing slashes (the site exports with
 * `trailingSlash: true`, so `usePathname()` yields e.g. `/docs/installation/`)
 * and compares with exact equality — `startsWith` would mark `/docs` active
 * everywhere since it prefixes every route.
 */

type NavItem = {
  label: string;
  href: string;
};

type NavGroup = {
  group: string;
  items: NavItem[];
};

/**
 * Source-of-truth docs navigation. The flat top-to-bottom order here is also
 * the canonical prev/next sequence handed to page components.
 */
export const NAV: NavGroup[] = [
  {
    group: 'Getting Started',
    items: [
      { label: 'Introduction', href: '/docs' },
      { label: 'Installation', href: '/docs/installation' },
      { label: 'Quick Start', href: '/docs/quick-start' },
    ],
  },
  {
    group: 'Configuration',
    items: [
      { label: 'Config files', href: '/docs/configuration' },
      { label: 'Providers & Models', href: '/docs/providers' },
      { label: 'Profiles', href: '/docs/profiles' },
      { label: 'AGENTS.md', href: '/docs/agents-md' },
    ],
  },
  {
    group: 'Usage',
    items: [
      { label: 'TUI & Slash Commands', href: '/docs/commands' },
      { label: 'Built-in Tools', href: '/docs/tools' },
      { label: '/goal Autonomous Mode', href: '/docs/goal' },
      { label: 'Sessions & Fork', href: '/docs/sessions' },
      { label: 'Permissions', href: '/docs/permissions' },
    ],
  },
  {
    group: 'Integrations',
    items: [
      { label: 'MCP', href: '/docs/mcp' },
      { label: 'LSP', href: '/docs/lsp' },
      { label: 'Hooks', href: '/docs/hooks' },
    ],
  },
  {
    group: 'Reference',
    items: [{ label: 'CLI Reference', href: '/docs/cli' }],
  },
];

/** Strip trailing slashes for trailing-slash-agnostic route comparison. */
const normalizePath = (path: string): string => path.replace(/\/+$/, '') || '/';

export default function DocsLayout({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const current = normalizePath(pathname ?? '');
  const [drawerOpen, setDrawerOpen] = useState(false);

  const closeDrawer = useCallback(() => setDrawerOpen(false), []);

  // Close the mobile drawer on route change so navigating feels instant.
  useEffect(() => {
    setDrawerOpen(false);
  }, [pathname]);

  // Escape closes the drawer; lock body scroll while it's open.
  useEffect(() => {
    if (!drawerOpen) return;

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDrawerOpen(false);
    };
    document.addEventListener('keydown', onKeyDown);

    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [drawerOpen]);

  return (
    <div className="min-h-screen bg-bg text-fg">
      {/* Mobile top bar: brand + drawer toggle. Hidden on lg+ where the
          persistent sidebar takes over. */}
      <div className="sticky top-0 z-40 flex items-center justify-between border-b border-border bg-bg/90 px-5 py-3 backdrop-blur lg:hidden">
        <BrandMark />
        <button
          type="button"
          onClick={() => setDrawerOpen((open) => !open)}
          aria-expanded={drawerOpen}
          aria-controls="docs-nav"
          className="inline-flex h-9 items-center gap-2 rounded-lg border border-border-strong bg-surface px-3 text-sm text-muted transition-colors hover:bg-surface-hover hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-saffron/70"
        >
          <MenuIcon />
          <span>Docs menu</span>
        </button>
      </div>

      <div className="mx-auto flex w-full max-w-content px-5 sm:px-8">
        {/* Desktop sidebar — sticky, scrolls independently. */}
        <aside className="hidden w-64 shrink-0 lg:block">
          <div className="sticky top-0 max-h-screen overflow-y-auto py-10 pr-6">
            <Link
              href="/"
              className="mb-8 inline-flex items-baseline gap-2 rounded-md font-mono text-lg font-semibold tracking-tight text-fg transition-colors hover:text-saffron focus-visible:text-saffron focus-visible:outline-none"
            >
              <span aria-hidden="true" className="text-saffron">
                &gt;_
              </span>
              BharatCode
            </Link>
            <SidebarNav current={current} />
          </div>
        </aside>

        {/* Main content area. */}
        <main className="min-w-0 flex-1 py-10 lg:py-14 lg:pl-10">
          {children}
        </main>
      </div>

      {/* Mobile drawer + backdrop. */}
      <div
        className={`fixed inset-0 z-50 lg:hidden ${
          drawerOpen ? '' : 'pointer-events-none'
        }`}
        aria-hidden={!drawerOpen}
      >
        {/* Backdrop. */}
        <button
          type="button"
          tabIndex={drawerOpen ? 0 : -1}
          aria-label="Close docs menu"
          onClick={closeDrawer}
          className={`absolute inset-0 bg-bg/70 backdrop-blur-sm transition-opacity duration-200 ${
            drawerOpen ? 'opacity-100' : 'opacity-0'
          }`}
        />

        {/* Panel. Uses a plain <div> wrapper so the only <nav> landmark inside
            is SidebarNav's own — avoids duplicate "Documentation" landmarks. */}
        <div
          id="docs-nav"
          className={`absolute inset-y-0 left-0 flex w-72 max-w-[85vw] flex-col border-r border-border bg-bg-elevated shadow-terminal transition-transform duration-200 ease-out ${
            drawerOpen ? 'translate-x-0' : '-translate-x-full'
          }`}
        >
          <div className="flex items-center justify-between border-b border-border px-5 py-4">
            <BrandMark />
            <button
              type="button"
              onClick={closeDrawer}
              aria-label="Close docs menu"
              className="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-border-strong bg-surface text-muted transition-colors hover:bg-surface-hover hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-saffron/70"
            >
              <CloseIcon />
            </button>
          </div>
          <div className="flex-1 overflow-y-auto px-5 py-6">
            <SidebarNav current={current} />
          </div>
        </div>
      </div>
    </div>
  );
}

/**
 * SidebarNav — grouped link list shared by the desktop sidebar and the mobile
 * drawer. Pass the normalized current path so it can mark the active link.
 */
function SidebarNav({ current }: { current: string }) {
  return (
    <nav aria-label="Documentation" className="flex flex-col gap-7">
      {NAV.map((section) => (
        <div key={section.group}>
          <h2 className="mb-2.5 px-3 font-mono text-xs font-medium uppercase tracking-wider text-faint">
            {section.group}
          </h2>
          <ul className="space-y-0.5">
            {section.items.map((item) => {
              const isActive = normalizePath(item.href) === current;
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    aria-current={isActive ? 'page' : undefined}
                    className={`block rounded-lg px-3 py-1.5 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-saffron/70 ${
                      isActive
                        ? 'bg-saffron/10 font-medium text-saffron'
                        : 'text-muted hover:bg-surface hover:text-fg'
                    }`}
                  >
                    {item.label}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}

function BrandMark() {
  return (
    <Link
      href="/"
      className="inline-flex items-baseline gap-2 rounded-md font-mono text-base font-semibold tracking-tight text-fg transition-colors hover:text-saffron focus-visible:text-saffron focus-visible:outline-none"
    >
      <span aria-hidden="true" className="text-saffron">
        &gt;_
      </span>
      BharatCode
    </Link>
  );
}

function MenuIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <line x1="3" y1="6" x2="21" y2="6" />
      <line x1="3" y1="12" x2="21" y2="12" />
      <line x1="3" y1="18" x2="21" y2="18" />
    </svg>
  );
}

function CloseIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
  );
}
