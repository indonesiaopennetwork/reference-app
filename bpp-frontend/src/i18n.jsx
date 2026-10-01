import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import id from './locales/id.js'

// Shared with bap-frontend/src/i18n.jsx: keep the two copies in step.
//
// English / Bahasa Indonesia. The English text is the key: t('Create Catalog')
// looks it up in locales/id.js and falls back to the English when a string
// has no translation yet. {name} placeholders are filled from vars:
//   t('{count} products', { count: 3 })
// rich() renders <b>…</b> in a translated string as bold, for sentences
// that emphasise a button name:
//   rich(t('Click <b>Publish</b> and confirm.'))

export const LANGS = [
  { code: 'en', label: 'English', short: 'EN', locale: 'en-GB' },
  { code: 'id', label: 'Bahasa Indonesia', short: 'ID', locale: 'id-ID' },
]
const DICTS = { en: {}, id }
const STORAGE_KEY = 'lang'

function storedLang() {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return DICTS[v] ? v : null
  } catch { return null }
}

function translate(lang, text, vars) {
  let s = DICTS[lang]?.[text] ?? text
  if (vars) s = s.replace(/\{(\w+)\}/g, (m, k) => (vars[k] ?? m))
  return s
}

const I18nContext = createContext({
  lang: 'en', chosen: true, locale: 'en-GB', setLang: () => {}, t: (s, v) => translate('en', s, v),
})

export function I18nProvider({ children }) {
  const [lang, setLangState] = useState(() => storedLang() || 'en')
  // False until the visitor has picked a language (first visit).
  const [chosen, setChosen] = useState(() => storedLang() !== null)

  const setLang = useCallback(code => {
    if (!DICTS[code]) return
    setLangState(code)
    setChosen(true)
    try { localStorage.setItem(STORAGE_KEY, code) } catch { /* storage blocked */ }
  }, [])

  // Tell the browser (and Chrome's translate offer) which language is shown.
  useEffect(() => { document.documentElement.lang = lang }, [lang])

  const value = useMemo(() => ({
    lang, chosen, setLang,
    locale: LANGS.find(l => l.code === lang).locale,
    t: (text, vars) => translate(lang, text, vars),
  }), [lang, chosen, setLang])
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export const useI18n = () => useContext(I18nContext)

// For code outside components (formatters, alerts) -- the current choice.
export function currentLang() { return storedLang() || 'en' }
export function tNow(text, vars) { return translate(currentLang(), text, vars) }
export function localeNow() { return LANGS.find(l => l.code === currentLang()).locale }

export function rich(text) {
  return text.split(/(<b>.*?<\/b>)/g).filter(Boolean).map((part, i) =>
    part.startsWith('<b>') ? <b key={i}>{part.slice(3, -4)}</b> : part)
}

// EN | ID switch for the header / sidebar.
export function LanguageToggle({ className = '' }) {
  const { lang, setLang, t } = useI18n()
  return (
    <div role="group" aria-label={t('Language')} data-tour="language"
      className={`inline-flex shrink-0 rounded-lg border border-white/10 overflow-hidden text-[11px] font-bold ${className}`}>
      {LANGS.map(l => (
        <button key={l.code} type="button" onClick={() => setLang(l.code)} aria-pressed={lang === l.code}
          title={l.label}
          className={`px-2 py-1.5 transition-colors ${lang === l.code ? 'bg-cyan-500/20 text-cyan-300' : 'text-slate-500 hover:text-slate-200'}`}>
          {l.short}
        </button>
      ))}
    </div>
  )
}

// First-visit chooser; shown until a language has been picked.
export function LanguagePicker() {
  const { chosen, setLang } = useI18n()
  if (chosen) return null
  return (
    <div className="fixed inset-0 z-[110] flex items-center justify-center p-4" style={{ background: 'rgba(3,7,18,0.8)' }}
      role="dialog" aria-modal="true" aria-label="Choose your language / Pilih bahasa">
      <div className="w-full max-w-sm rounded-2xl border border-white/10 shadow-2xl p-6 text-center" style={{ background: '#0d1426' }}>
        <p className="text-lg font-bold text-white">Choose your language</p>
        <p className="text-sm text-slate-400 mb-5">Pilih bahasa Anda</p>
        <div className="grid gap-3">
          {LANGS.map(l => (
            <button key={l.code} type="button" onClick={() => setLang(l.code)}
              className="w-full px-4 py-3 rounded-xl text-sm font-semibold text-white border border-white/10 hover:border-cyan-400/50 hover:bg-cyan-400/10 transition-colors">
              {l.label}
            </button>
          ))}
        </div>
        <p className="text-[11px] text-slate-500 mt-4">You can change this later · Bisa diganti nanti</p>
      </div>
    </div>
  )
}
