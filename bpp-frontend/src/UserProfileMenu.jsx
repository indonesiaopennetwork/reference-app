import { useEffect, useRef, useState } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { User, LogOut, ChevronDown } from 'lucide-react'
import { useI18n } from './i18n.jsx'
import { getIdToken, logout } from './auth.js'
import { decodeJwtPayload } from './jwt.js'

// Replaces the old standalone Log out button in the sidebar footer -- shows
// the logged-in user's name/email (decoded from the id_token landing-page
// forwards on cross-nav, see auth.js's getIdToken()) with Log out moved
// inside, unchanged from what it always did. The panel opens upward
// (bottom-full) since the trigger sits at the bottom of a fixed sidebar.
export default function UserProfileMenu() {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [profile, setProfile] = useState(null)
  const ref = useRef(null)

  useEffect(() => {
    const idToken = getIdToken()
    if (idToken) setProfile(decodeJwtPayload(idToken))
  }, [])

  useEffect(() => {
    function onClickOutside(e) {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [])

  const name = profile?.name || profile?.email || t('Logged in')
  const email = profile?.email && profile.email !== name ? profile.email : null
  const initial = (profile?.name || profile?.email || '?').charAt(0).toUpperCase()

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold text-slate-300 transition-all bg-white/[0.04] hover:bg-white/[0.08] border border-white/[0.08] hover:border-white/[0.15]"
      >
        <span className="w-5 h-5 rounded-full bg-cyan-500/20 text-cyan-300 flex items-center justify-center text-[10px] font-bold shrink-0">
          {profile ? initial : <User size={11} />}
        </span>
        <span className="flex-1 min-w-0 text-left truncate">{name}</span>
        <ChevronDown size={13} className={`shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      <AnimatePresence>
        {open && (
          <motion.div
            initial={{ opacity: 0, y: 8, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 8, scale: 0.97 }}
            transition={{ duration: 0.15 }}
            className="absolute left-0 right-0 bottom-full mb-2 rounded-xl border border-white/10 bg-[#0d1220] shadow-2xl backdrop-blur-xl p-3 z-50"
          >
            <p className="text-xs font-semibold text-white truncate">{name}</p>
            {email && <p className="text-[10px] text-slate-400 truncate mt-0.5">{email}</p>}
            <button
              onClick={logout}
              className="mt-3 w-full flex items-center justify-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold text-rose-400 transition-all bg-rose-500/10 hover:bg-rose-500/20 border border-rose-500/20 hover:border-rose-500/40"
            >
              <LogOut size={13} /> {t('Log out')}
            </button>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}
