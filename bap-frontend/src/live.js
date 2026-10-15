import { useEffect, useRef } from 'react';

// LIVE_EVERY is how often a live view refreshes its data in the background,
// so changes made elsewhere -- the seller's answers, delivery, completion,
// reconciliation replies -- show without reloading the page.
export const LIVE_EVERY = 5000;

// useLive calls refresh every LIVE_EVERY while active and the tab is
// visible, and when the tab comes back into view. refresh should update the
// view quietly (no spinner).
export function useLive(refresh, active = true) {
  const fn = useRef(refresh);
  fn.current = refresh;
  useEffect(() => {
    if (!active) return undefined;
    const tick = () => { if (!document.hidden) fn.current(); };
    const timer = setInterval(tick, LIVE_EVERY);
    document.addEventListener('visibilitychange', tick);
    window.addEventListener('focus', tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
      window.removeEventListener('focus', tick);
    };
  }, [active]);
}
