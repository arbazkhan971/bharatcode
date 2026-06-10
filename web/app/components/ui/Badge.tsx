import { type ReactNode } from 'react';

type BadgeVariant = 'saffron' | 'blue' | 'green' | 'neutral';
type BadgeSize = 'sm' | 'md';

type BadgeProps = {
  children: ReactNode;
  variant?: BadgeVariant;
  size?: BadgeSize;
  /** Render a small leading status dot (e.g. for "MIT licensed" / live cues). */
  dot?: boolean;
  className?: string;
};

const VARIANTS: Record<BadgeVariant, string> = {
  saffron:
    'border-saffron/30 bg-saffron/10 text-saffron',
  blue: 'border-blue/30 bg-blue/10 text-blue',
  green: 'border-green/30 bg-green/10 text-green',
  neutral: 'border-border-strong bg-surface text-muted',
};

const DOTS: Record<BadgeVariant, string> = {
  saffron: 'bg-saffron',
  blue: 'bg-blue',
  green: 'bg-green',
  neutral: 'bg-muted',
};

const SIZES: Record<BadgeSize, string> = {
  sm: 'px-2 py-0.5 text-[0.6875rem]',
  md: 'px-2.5 py-1 text-xs',
};

/**
 * Badge — small monospace pill for labels, tags, and status cues.
 */
export function Badge({
  children,
  variant = 'neutral',
  size = 'md',
  dot = false,
  className = '',
}: BadgeProps) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border font-mono font-medium uppercase tracking-wider ${VARIANTS[variant]} ${SIZES[size]} ${className}`}
    >
      {dot ? (
        <span
          aria-hidden="true"
          className={`h-1.5 w-1.5 rounded-full ${DOTS[variant]}`}
        />
      ) : null}
      {children}
    </span>
  );
}

export default Badge;
