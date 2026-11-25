// Class-name helpers shared by the QR pages (kept out of ui.jsx so that file
// exports only components, for fast refresh).

export const cx = (...p) => p.filter(Boolean).join(' ');

export const inputClass = invalid => cx(
  'block w-full rounded-xl border bg-white/[0.06] px-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none transition-colors',
  invalid ? 'border-rose-500/60 focus:border-rose-400' : 'border-white/10 focus:border-brand/60',
);
