import type { ReactNode } from 'react';

interface SectionProps {
  title: string;
  /** One line under the title: what the section shows, units, period. */
  subtitle?: ReactNode;
  /** Right-aligned controls (period selector, buttons). Wraps below the title on phones. */
  actions?: ReactNode;
  /** First section on a page/tab: no hairline above. */
  first?: boolean;
  /** Heading level. Sections are h2; charts inside a section use `SubHeading`. */
  level?: 2 | 3;
  id?: string;
  className?: string;
  children: ReactNode;
}

/** Page section: hairline above, serif heading, optional subtitle and actions. */
export default function Section({ title, subtitle, actions, first = false, level = 2, id, className = '', children }: SectionProps) {
  const H = level === 2 ? 'h2' : 'h3';
  return (
    <section id={id} className={`${first ? 'mt-6' : 'mt-8 border-t border-divider pt-5'} ${className}`}>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <H className="font-serif text-heading text-ink">{title}</H>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {subtitle && <p className="mt-1 text-[13px] text-ink-muted">{subtitle}</p>}
      <div className="mt-4">{children}</div>
    </section>
  );
}

/** Heading for a chart or block inside a Section — visibly subordinate to the section title. */
export function SubHeading({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <h3 className={`text-[13px] leading-5 font-semibold text-ink ${className}`}>{children}</h3>;
}

/** Page title row. The h1 is shown on desktop; on phones the app header carries the title. */
export function PageHeader({ title, meta, actions }: { title: string; meta?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
      <h1 className="hidden md:block font-serif text-title text-ink">{title}</h1>
      {(meta || actions) && (
        <div className="flex flex-wrap items-center gap-3 ml-auto">
          {meta && <span className="text-[13px] text-ink-muted">{meta}</span>}
          {actions}
        </div>
      )}
    </div>
  );
}
