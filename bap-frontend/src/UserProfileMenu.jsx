import { useEffect, useRef, useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { User, LogOut } from 'lucide-react';
import { useI18n } from './i18n.jsx';
import { getIdToken, logout } from './auth.js';
import { decodeJwtPayload } from './jwt.js';

// Replaces the old standalone Log out button in the nav -- shows the
// logged-in user's name/email (decoded from the id_token landing-page
// forwards on cross-nav, see auth.js's getIdToken()) with Log out moved
// inside, unchanged from what it always did.
export default function UserProfileMenu() {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [profile, setProfile] = useState(null);
  const ref = useRef(null);

  useEffect(() => {
    const idToken = getIdToken();
    if (idToken) setProfile(decodeJwtPayload(idToken));
  }, []);

  useEffect(() => {
    function onClickOutside(e) {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false);
    }
    document.addEventListener('mousedown', onClickOutside);
    return () => document.removeEventListener('mousedown', onClickOutside);
  }, []);

  const name = profile?.name || profile?.email || t('Logged in');
  const email = profile?.email && profile.email !== name ? profile.email : null;
  const initial = (profile?.name || profile?.email || '?').charAt(0).toUpperCase();

  return (
    <div ref={ref} className="relative shrink-0">
      <button
        onClick={() => setOpen((o) => !o)} title={t('User profile')}
        className="shrink-0 flex items-center justify-center w-9 h-9 rounded-full transition-all text-sm font-bold text-white shadow-sm hover:brightness-110"
        style={{ background: 'linear-gradient(135deg,#00b8e6,#1e2fa0)' }}
      >
        {profile ? initial : <User className="w-4 h-4" />}
      </button>
      <AnimatePresence>
        {open && (
          <motion.div
            initial={{ opacity: 0, y: -8, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -8, scale: 0.97 }}
            transition={{ duration: 0.15 }}
            className="absolute right-0 top-full mt-2 w-64 rounded-2xl border border-white/10 bg-[#0d1220] shadow-2xl backdrop-blur-xl p-4 z-50"
          >
            <p className="text-sm font-semibold text-white truncate">{name}</p>
            {email && <p className="text-xs text-slate-400 truncate mt-0.5">{email}</p>}
            <button
              onClick={logout}
              className="mt-4 w-full flex items-center justify-center gap-2 px-3 py-2 rounded-xl transition-all text-sm font-medium text-rose-400 hover:text-rose-300 border border-rose-500/20 hover:border-rose-500/40 bg-rose-500/10"
            >
              <LogOut className="w-4 h-4" />
              {t('Log out')}
            </button>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
