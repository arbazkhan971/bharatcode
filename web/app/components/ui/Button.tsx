import {
  type AnchorHTMLAttributes,
  type ButtonHTMLAttributes,
  type ReactNode,
} from 'react';

type Variant = 'primary' | 'secondary' | 'ghost';
type Size = 'sm' | 'md' | 'lg';

type CommonProps = {
  variant?: Variant;
  size?: Size;
  /** Optional leading icon/element. */
  leading?: ReactNode;
  /** Optional trailing icon/element. */
  trailing?: ReactNode;
  className?: string;
  children: ReactNode;
};

type ButtonAsButton = CommonProps &
  Omit<ButtonHTMLAttributes<HTMLButtonElement>, keyof CommonProps> & {
    href?: undefined;
  };

type ButtonAsLink = CommonProps &
  Omit<AnchorHTMLAttributes<HTMLAnchorElement>, keyof CommonProps> & {
    href: string;
  };

type ButtonProps = ButtonAsButton | ButtonAsLink;

const BASE =
  'inline-flex items-center justify-center gap-2 rounded-xl font-medium leading-none transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-bg';

const VARIANTS: Record<Variant, string> = {
  primary:
    'bg-saffron text-bg hover:bg-saffron-soft focus-visible:ring-saffron/70 shadow-glow',
  secondary:
    'border border-border-strong bg-surface text-fg hover:bg-surface-hover focus-visible:ring-saffron/70',
  ghost:
    'text-muted hover:text-fg hover:bg-surface focus-visible:ring-saffron/70',
};

const SIZES: Record<Size, string> = {
  sm: 'h-9 px-3.5 text-sm',
  md: 'h-11 px-5 text-sm',
  lg: 'h-12 px-6 text-base',
};

/**
 * Button — primary CTA primitive. Renders a semantic `<button>` by default,
 * or an `<a>` when an `href` is supplied (so links stay links). Keyboard
 * focus shows an on-brand saffron ring.
 *
 * This component is server-friendly: it adds no client hooks. Pass an
 * `onClick` handler from a client component (a file with "use client") when
 * you need interactivity.
 */
export function Button(props: ButtonProps) {
  const {
    variant = 'primary',
    size = 'md',
    leading,
    trailing,
    className = '',
    children,
  } = props;

  const classes = `${BASE} ${VARIANTS[variant]} ${SIZES[size]} ${className}`;

  const inner = (
    <>
      {leading ? (
        <span className="-ml-0.5 inline-flex shrink-0" aria-hidden="true">
          {leading}
        </span>
      ) : null}
      <span>{children}</span>
      {trailing ? (
        <span className="-mr-0.5 inline-flex shrink-0" aria-hidden="true">
          {trailing}
        </span>
      ) : null}
    </>
  );

  if ('href' in props && props.href !== undefined) {
    const {
      variant: _v,
      size: _s,
      leading: _l,
      trailing: _t,
      className: _c,
      children: _ch,
      ...anchorProps
    } = props;
    return (
      <a className={classes} {...anchorProps}>
        {inner}
      </a>
    );
  }

  const {
    variant: _v,
    size: _s,
    leading: _l,
    trailing: _t,
    className: _c,
    children: _ch,
    href: _h,
    type,
    ...buttonProps
  } = props as ButtonAsButton;

  return (
    <button type={type ?? 'button'} className={classes} {...buttonProps}>
      {inner}
    </button>
  );
}

export default Button;
