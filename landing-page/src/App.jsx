import { useEffect, useState } from 'react'
import { LogOut } from 'lucide-react'
import { config, basePath } from './config.js'
import { startLogin, handleCallback, getValidToken, getIdToken, clearToken, logout } from './oidc.js'

async function bootstrap(token, businessName) {
  const res = await fetch(`${config.bppApiUrl}/api/v1/auth/bootstrap`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify(businessName ? { businessName } : {}),
  })
  if (res.status === 400) return { needsBusinessName: true }
  if (!res.ok) throw new Error(`bootstrap failed: ${res.status}`)
  return res.json()
}

export default function App() {
  const [state, setState] = useState('loading') // loading | needs-profile | ready | error
  const [errorMessage, setErrorMessage] = useState('')
  const [token, setToken] = useState(null)
  const [providerId, setProviderId] = useState(null)

  useEffect(() => {
    async function run() {
      try {
        if (window.location.pathname === `${basePath}logout`) {
          logout() // navigates away; nothing after this runs
          return
        }

        if (window.location.pathname === `${basePath}callback`) {
          await handleCallback()
          window.history.replaceState({}, '', basePath)
        }

        const validToken = getValidToken()
        if (!validToken) {
          await startLogin() // redirects the browser away; nothing after this runs
          return
        }
        setToken(validToken)

        const result = await bootstrap(validToken)
        if (result.needsBusinessName) {
          setState('needs-profile')
        } else {
          if (result.providerId) {
            setProviderId(result.providerId)
          }
          setState('ready')
        }
      } catch (err) {
        console.error(err)
        setErrorMessage(err.message || String(err))
        setState('error')
      }
    }
    run()
  }, [])

  async function handleProfileSubmit(businessName) {
    try {
      const result = await bootstrap(token, businessName)
      if (result.providerId) {
        setProviderId(result.providerId)
      }
      setState('ready')
    } catch (err) {
      console.error(err)
      setErrorMessage(err.message || String(err))
      setState('error')
    }
  }

  function retry() {
    clearToken()
    window.location.href = basePath
  }

  if (state === 'loading') return <CenteredMessage text="Loading..." />
  if (state === 'error') {
    return (
      <CenteredMessage text={`Something went wrong: ${errorMessage}`}>
        <button onClick={retry} className="btn mt-4 border-white/10 bg-white/5 hover:bg-white/10">
          Try again
        </button>
      </CenteredMessage>
    )
  }
  if (state === 'needs-profile') {
    return <SellerProfileForm onSubmit={handleProfileSubmit} />
  }

  return <Landing token={token} providerId={providerId} />
}

function CenteredMessage({ text, children }) {
  return (
    <div className="min-h-screen flex items-center justify-center p-6">
      <div className="text-center text-slate-300">
        <p>{text}</p>
        {children}
      </div>
    </div>
  )
}

function SellerProfileForm({ onSubmit }) {
  const [businessName, setBusinessName] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(e) {
    e.preventDefault()
    if (!businessName.trim()) return
    setSubmitting(true)
    await onSubmit(businessName.trim())
    setSubmitting(false)
  }

  return (
    <div className="min-h-screen flex items-center justify-center p-6">
      <form onSubmit={handleSubmit} className="w-full max-w-sm text-center">
        <h1 className="text-2xl font-bold text-white mb-2">Set up your seller profile</h1>
        <p className="text-slate-400 text-sm mb-6">
          One more step — what's your business called?
        </p>
        <input
          autoFocus
          value={businessName}
          onChange={(e) => setBusinessName(e.target.value)}
          placeholder="Business name"
          className="w-full rounded-xl border border-white/10 bg-white/5 px-4 py-3 text-white placeholder:text-slate-500 outline-none focus:border-white/20 mb-4"
        />
        <button
          type="submit"
          disabled={submitting || !businessName.trim()}
          className="w-full rounded-xl bg-sky-500 py-3 font-semibold text-white transition hover:bg-sky-400 disabled:opacity-50"
        >
          {submitting ? 'Saving…' : 'Continue'}
        </button>
      </form>
    </div>
  )
}

function Landing({ token, providerId }) {
  // Forwarded so bap-frontend/bpp-frontend can decode profile claims
  // (name/email) locally -- see their own auth.js/UserProfileMenu.jsx.
  const idToken = getIdToken()
  const idTokenParam = idToken ? `&id_token=${encodeURIComponent(idToken)}` : ''
  const buyerUrl = `${config.bapUrl}/?token=${encodeURIComponent(token)}${idTokenParam}${providerId ? `&providerId=${encodeURIComponent(providerId)}` : ''}`
  const sellerUrl = `${config.bppUrl}/?token=${encodeURIComponent(token)}${idTokenParam}${providerId ? `&providerId=${encodeURIComponent(providerId)}` : ''}`
  return (
    <div className="relative min-h-screen flex items-center justify-center p-6">
      <button
        onClick={logout}
        className="absolute top-6 right-6 flex items-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold text-rose-400 transition-all bg-rose-500/10 hover:bg-rose-500/20 border border-rose-500/20 hover:border-rose-500/40"
      >
        <LogOut size={13} /> Log out
      </button>
      <div className="max-w-xl w-full text-center">
        <p className="text-2xl md:text-3xl font-bold text-white tracking-tight">
          ION <span className="font-normal text-slate-400">Reference App</span>
        </p>
        <span className="inline-block mt-2.5 rounded-full bg-orange-500 px-3.5 py-1.5 text-xs text-white">
          This is the reference app for ION network managed by remiges technologies pvt. ltd.
        </span>
        <p className="mt-5 mb-9 text-sm leading-relaxed text-slate-400">
          A working demo of the Beckn Protocol — a buyer app and a seller app that
          discover, quote, and transact with each other purely through an open
          network protocol.
        </p>
        <div className="flex flex-wrap justify-center gap-4">
          <a href={buyerUrl} className="btn border-white/8 bg-white/3 hover:border-white/20 hover:bg-white/6">
            <span className="block mb-1.5 text-xs font-medium uppercase tracking-wider text-emerald-400">
              Buyer app (BAP)
            </span>
            Enter as Buyer
          </a>
          <a href={sellerUrl} className="btn border-white/8 bg-white/3 hover:border-white/20 hover:bg-white/6">
            <span className="block mb-1.5 text-xs font-medium uppercase tracking-wider text-sky-400">
              Seller app (BPP)
            </span>
            Enter as Seller
          </a>
        </div>
      </div>
    </div>
  )
}
