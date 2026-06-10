import { type ReactNode } from 'react';

/** A single rendered line inside the terminal body. */
export type TerminalLine =
  | { type: 'command'; text: string; prompt?: string }
  | { type: 'output'; text: ReactNode }
  | { type: 'comment'; text: string }
  | { type: 'success'; text: ReactNode }
  | { type: 'spacer' };

type TerminalProps = {
  /** Title shown in the window titlebar (e.g. "bharatcode — ~/project"). */
  title?: string;
  /** Structured lines to render. Use this OR `children`, not both. */
  lines?: TerminalLine[];
  /** Raw children rendered inside the terminal body (full control). */
  children?: ReactNode;
  /** Show a blinking cursor at the end of the body. */
  cursor?: boolean;
  className?: string;
};

/**
 * Terminal — a stylized fake terminal window with a titlebar, traffic-light
 * dots, and a dark monospace body. Use it to showcase CLI demos.
 *
 * Presentational and server-rendered. Pass `lines` for quick structured demos
 * or `children` for full control of the body. The blinking cursor uses a
 * CSS animation that is disabled under `prefers-reduced-motion`.
 */
export function Terminal({
  title = 'bharatcode',
  lines,
  children,
  cursor = false,
  className = '',
}: TerminalProps) {
  return (
    <div
      className={`overflow-hidden rounded-2xl border border-border bg-bg-elevated shadow-terminal ${className}`}
    >
      {/* Titlebar */}
      <div className="flex items-center gap-2 border-b border-border bg-surface/60 px-4 py-2.5">
        <span className="flex items-center gap-1.5" aria-hidden="true">
          <span className="h-3 w-3 rounded-full bg-[#FF5F56]" />
          <span className="h-3 w-3 rounded-full bg-[#FFBD2E]" />
          <span className="h-3 w-3 rounded-full bg-[#27C93F]" />
        </span>
        <span className="flex-1 truncate text-center font-mono text-xs text-faint">
          {title}
        </span>
        {/* Spacer to visually balance the traffic-light dots. */}
        <span aria-hidden="true" className="w-[52px]" />
      </div>

      {/* Body */}
      <div className="overflow-x-auto px-4 py-4 font-mono text-[0.8125rem] leading-relaxed sm:text-sm">
        {lines ? (
          <div className="space-y-1">
            {lines.map((line, i) => (
              <TerminalLineRow key={i} line={line} />
            ))}
            {cursor ? <Cursor inline /> : null}
          </div>
        ) : (
          <>
            {children}
            {cursor ? <Cursor /> : null}
          </>
        )}
      </div>
    </div>
  );
}

function TerminalLineRow({ line }: { line: TerminalLine }) {
  switch (line.type) {
    case 'command':
      return (
        <div className="text-fg">
          <span className="select-none text-green" aria-hidden="true">
            {line.prompt ?? '$'}{' '}
          </span>
          {line.text}
        </div>
      );
    case 'output':
      return <div className="text-muted">{line.text}</div>;
    case 'comment':
      return <div className="text-faint">{line.text}</div>;
    case 'success':
      return <div className="text-green">{line.text}</div>;
    case 'spacer':
      return <div className="h-2" aria-hidden="true" />;
    default:
      return null;
  }
}

function Cursor({ inline = false }: { inline?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={`${inline ? 'inline-block' : ''} h-4 w-2 translate-y-0.5 bg-saffron animate-cursor-blink`}
    />
  );
}

export default Terminal;
