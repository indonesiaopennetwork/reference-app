// Small presentational pieces for the QR pages, in the shop's dark style.
import { Loader2, X } from 'lucide-react';

import { cx } from './styles.js';

const BUTTON = {
  primary: 'bg-brand text-slate-950 hover:bg-brand-dark disabled:bg-brand/40',
  secondary: 'bg-white/[0.06] text-white border border-white/10 hover:bg-white/[0.1]',
  success: 'bg-emerald-500 text-slate-950 hover:bg-emerald-400',
  warning: 'bg-amber-400 text-slate-950 hover:bg-amber-300',
  danger: 'bg-rose-500 text-white hover:bg-rose-400',
  ghost: 'text-slate-300 hover:bg-white/[0.06]',
};

export function Button({ variant = 'primary', size = 'md', busy, className, children, ...props }) {
  return (
    <button
      type="button"
      {...props}
      disabled={props.disabled || busy}
      className={cx(
        'inline-flex items-center justify-center gap-1.5 rounded-xl font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50',
        size === 'sm' ? 'px-2.5 py-1.5 text-xs' : size === 'lg' ? 'px-5 py-3.5 text-base' : 'px-3.5 py-2 text-sm',
        BUTTON[variant], className,
      )}
    >
      {busy && <Loader2 className="w-4 h-4 animate-spin" />}
      {children}
    </button>
  );
}

export function Card({ title, actions, className, bodyClassName, children }) {
  return (
    <section className={cx('glass rounded-2xl', className)}>
      {(title || actions) && (
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-white/[0.06] px-4 py-3">
          <h2 className="text-sm font-bold tracking-wide text-slate-200 uppercase" style={{ fontFamily: 'inherit', letterSpacing: '0.04em' }}>{title}</h2>
          {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={cx('p-4', bodyClassName)}>{children}</div>
    </section>
  );
}

const ALERT = {
  error: 'bg-rose-500/10 text-rose-200 border-rose-500/30',
  warning: 'bg-amber-500/10 text-amber-100 border-amber-500/30',
  success: 'bg-emerald-500/10 text-emerald-100 border-emerald-500/30',
  info: 'bg-sky-500/10 text-sky-100 border-sky-500/30',
};

export function Alert({ tone = 'info', title, children, onClose }) {
  return (
    <div role={tone === 'error' ? 'alert' : 'status'} className={cx('flex gap-3 rounded-xl border px-3.5 py-3 text-sm', ALERT[tone])}>
      <div className="min-w-0 flex-1">
        {title && <p className="font-bold">{title}</p>}
        {children && <div className={cx(title && 'mt-1', 'space-y-1')}>{children}</div>}
      </div>
      {onClose && (
        <button type="button" onClick={onClose} className="self-start opacity-60 hover:opacity-100" aria-label="Dismiss">
          <X className="w-4 h-4" />
        </button>
      )}
    </div>
  );
}

const STATUS = {
  DRAFT: 'bg-slate-500/20 text-slate-200 border-slate-400/30',
  ACTIVE: 'bg-emerald-500/15 text-emerald-300 border-emerald-500/30',
  PAUSED: 'bg-amber-500/15 text-amber-300 border-amber-500/30',
  RETIRED: 'bg-rose-500/15 text-rose-300 border-rose-500/30',
};

export function StatusBadge({ status }) {
  return (
    <span className={cx('inline-flex min-w-[84px] items-center justify-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-bold', STATUS[status] || STATUS.DRAFT)}>
      <span className={cx('w-1.5 h-1.5 rounded-full', status === 'ACTIVE' ? 'bg-emerald-400' : 'bg-current opacity-60')} />
      {status}
    </span>
  );
}

export function Tag({ children, className }) {
  return (
    <span className={cx('inline-flex items-center rounded-md border border-white/10 bg-white/[0.06] px-1.5 py-0.5 font-mono text-[11px] font-semibold text-slate-300', className)}>
      {children}
    </span>
  );
}

export function Field({ label, error, hint, htmlFor, className, children }) {
  return (
    <div className={className}>
      <label htmlFor={htmlFor} className="mb-1 block text-xs font-semibold text-slate-400">{label}</label>
      {children}
      {error ? <p className="mt-1 text-xs font-semibold text-rose-300">{error}</p> : hint && <p className="mt-1 text-xs text-slate-500">{hint}</p>}
    </div>
  );
}


export function Spinner({ className = 'w-5 h-5' }) {
  return <Loader2 className={cx('animate-spin', className)} />;
}

export function ErrorNote({ error, title = 'Request failed', onClose }) {
  if (!error) return null;
  const msgs = error.messages?.length > 1 ? error.messages : null;
  return (
    <Alert tone="error" title={`${title}${error.status ? ` (HTTP ${error.status})` : ''}`} onClose={onClose}>
      {msgs ? (
        <ul className="list-disc pl-4">{msgs.map((m, i) => <li key={i}><span className="font-mono">{m.errcode}</span>{m.field && ` on ${m.field}`}{m.vals?.length > 0 && `: ${m.vals.join(' ')}`}</li>)}</ul>
      ) : <p>{error.message}</p>}
    </Alert>
  );
}
