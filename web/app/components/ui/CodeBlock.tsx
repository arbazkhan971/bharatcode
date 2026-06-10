'use client';

import { useCallback, useState } from 'react';

type CodeBlockProps = {
  /** The code/command text to display and copy. */
  code: string;
  /** Optional language label shown in the header (e.g. "bash", "go"). */
  language?: string;
  /** Optional filename/label shown in the header. */
  label?: string;
  /** Render a leading `$` prompt on each line (for shell commands). */
  prompt?: boolean;
  /** Hide the copy button. */
  hideCopy?: boolean;
  className?: string;
};

/**
 * CodeBlock — dark, terminal-styled monospace block with copy-to-clipboard.
 *
 * Client component: uses the Clipboard API and local state for the copied
 * confirmation. The copy button carries a descriptive `aria-label` and a
 * polite live region announces the result for screen readers.
 */
export function CodeBlock({
  code,
  language,
  label,
  prompt = false,
  hideCopy = false,
  className = '',
}: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard unavailable (e.g. insecure context) — fail silently.
    }
  }, [code]);

  const lines = code.split('\n');
  const showHeader = Boolean(language || label);

  return (
    <div
      className={`group relative overflow-hidden rounded-xl border border-border bg-bg-elevated font-mono text-sm shadow-terminal ${className}`}
    >
      {showHeader ? (
        <div className="flex items-center justify-between border-b border-border px-4 py-2">
          <span className="text-xs text-faint">{label}</span>
          {language ? (
            <span className="text-[0.6875rem] uppercase tracking-wider text-faint">
              {language}
            </span>
          ) : null}
        </div>
      ) : null}

      <div className="relative">
        <pre className="overflow-x-auto px-4 py-3.5 leading-relaxed text-fg">
          <code>
            {lines.map((line, i) => (
              <span key={i} className="block whitespace-pre">
                {prompt ? (
                  <span className="select-none text-green" aria-hidden="true">
                    ${' '}
                  </span>
                ) : null}
                {line || ' '}
              </span>
            ))}
          </code>
        </pre>

        {!hideCopy ? (
          <button
            type="button"
            onClick={handleCopy}
            aria-label={copied ? 'Copied to clipboard' : 'Copy code to clipboard'}
            className="absolute right-2.5 top-2.5 inline-flex h-8 items-center gap-1.5 rounded-lg border border-border-strong bg-surface px-2.5 text-xs text-muted opacity-0 transition hover:bg-surface-hover hover:text-fg focus-visible:opacity-100 group-hover:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-saffron/70"
          >
            {copied ? (
              <>
                <CheckIcon />
                <span>Copied</span>
              </>
            ) : (
              <>
                <CopyIcon />
                <span>Copy</span>
              </>
            )}
          </button>
        ) : null}
      </div>

      <span className="sr-only" role="status" aria-live="polite">
        {copied ? 'Copied to clipboard' : ''}
      </span>
    </div>
  );
}

function CopyIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      className="text-green"
      aria-hidden="true"
    >
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

export default CodeBlock;
