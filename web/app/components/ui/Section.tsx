import { type ElementType, type ReactNode } from 'react';

type SectionProps = {
  /** Render as a semantic element (section, div, footer, header...). */
  as?: ElementType;
  /** Anchor id for in-page navigation. */
  id?: string;
  /** Inner max-width container width. */
  width?: 'content' | 'narrow' | 'wide';
  /** Vertical padding rhythm. */
  spacing?: 'none' | 'sm' | 'md' | 'lg';
  /** Extra classes applied to the outer element (full-bleed background, etc.). */
  className?: string;
  /** Extra classes applied to the inner centered container. */
  innerClassName?: string;
  children: ReactNode;
};

const WIDTHS: Record<NonNullable<SectionProps['width']>, string> = {
  narrow: 'max-w-3xl',
  content: 'max-w-content',
  wide: 'max-w-7xl',
};

const SPACING: Record<NonNullable<SectionProps['spacing']>, string> = {
  none: '',
  sm: 'py-10 sm:py-12',
  md: 'py-16 sm:py-20',
  lg: 'py-20 sm:py-28',
};

/**
 * Section — full-bleed outer element + a centered, max-width inner container.
 *
 * Use it as the layout primitive for every page section so horizontal
 * gutters and content width stay consistent across the site. Pass
 * background/border classes via `className` (applied to the full-bleed
 * outer element); content sits inside the centered container.
 */
export function Section({
  as: Tag = 'section',
  id,
  width = 'content',
  spacing = 'md',
  className = '',
  innerClassName = '',
  children,
}: SectionProps) {
  return (
    <Tag id={id} className={`w-full ${className}`}>
      <div
        className={`mx-auto w-full px-5 sm:px-8 ${WIDTHS[width]} ${SPACING[spacing]} ${innerClassName}`}
      >
        {children}
      </div>
    </Tag>
  );
}

export default Section;
