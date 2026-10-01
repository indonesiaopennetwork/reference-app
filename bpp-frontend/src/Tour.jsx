import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useI18n } from './i18n.jsx'

// Shared with bap-frontend/src/Tour.jsx: keep the two copies in step.

// A guided tour for new users: each step optionally switches the app
// to a section (onStep does the navigation), spotlights an element marked
// data-tour="<target>", and explains it in a card placed beside it. Steps
// whose target isn't on screen show the card centred without a spotlight.
//
// Hands-on steps have the user do it themselves: the highlighted element
// stays clickable and the tour moves on by itself --
//   action: 'click'       once the target is clicked
//   until: state =>       once this returns true for the app's `state` prop
//                         (checked every frame), e.g. ({ page }) => page === 'x'
// A click step whose target isn't on screen offers Next instead.
// step.skip = { label, to: <step id> } adds a link jumping past a section.

const PAD = 6
const CARD_W = 340

// The on-screen element for a data-tour target (the sidebar is rendered
// twice -- desktop and mobile drawer -- so pick the visible copy).
function findTarget(target) {
  if (!target) return null
  for (const el of document.querySelectorAll(`[data-tour="${target}"]`)) {
    const r = el.getBoundingClientRect()
    if (r.width > 0 && r.height > 0) return el
  }
  return null
}

// Where the card goes, given the target's rect and the card's own height;
// always kept inside the viewport.
function cardPosition(rect, h) {
  const vw = window.innerWidth, vh = window.innerHeight
  const w = Math.min(CARD_W, vw - 24)
  const clampTop = t => Math.min(Math.max(12, t), Math.max(12, vh - h - 12))
  if (!rect) return { left: (vw - w) / 2, top: clampTop(vh / 2 - h / 2), width: w }
  // Beside the target when there's room (sidebar items), else below / above it.
  if (rect.right + 16 + w <= vw) return { left: rect.right + 16, top: clampTop(rect.top - 8), width: w }
  const left = Math.min(Math.max(12, rect.left), vw - w - 12)
  if (rect.bottom + 12 + h <= vh - 12) return { left, top: rect.bottom + 12, width: w }
  if (rect.top - 12 - h >= 12) return { left, top: rect.top - 12 - h, width: w }
  // A large target (a whole form): float the card over its lower right.
  return { left: vw - w - 12, top: vh - h - 12, width: w }
}

const handsOn = step => !!step && (step.action === 'click' || !!step.until)

export default function Tour({ steps, open, onClose, onStep, state }) {
  const { t } = useI18n()
  const [index, setIndex] = useState(0)
  const [rect, setRect] = useState(null)
  const [cardH, setCardH] = useState(220)
  const cardRef = useRef(null)
  const stateRef = useRef(state)
  stateRef.current = state
  const step = steps[index]
  // An until step waits even while its target is gone (a request in flight).
  const waiting = handsOn(step) && (!!rect || !!step.until)

  // The card's real height, for placing it (content varies per step).
  useLayoutEffect(() => {
    const h = cardRef.current?.offsetHeight
    if (h && h !== cardH) setCardH(h)
  })

  // Start from the beginning each time the tour opens.
  useEffect(() => { if (open) setIndex(0) }, [open])

  const last = index === steps.length - 1
  const next = useCallback(() => (last ? onClose() : setIndex(i => i + 1)), [last, onClose])
  const back = useCallback(() => setIndex(i => Math.max(0, i - 1)), [])
  const nextRef = useRef(next)
  nextRef.current = next

  // Navigate for the step, then follow its target for as long as the step is
  // shown: pages animate in and forms grow, so the target keeps moving.
  // State only changes when the rect does.
  useLayoutEffect(() => {
    if (!open || !step) return
    onStep?.(step)
    let frame, last = '', done = false
    const scrollUntil = performance.now() + 700
    const measure = () => {
      if (!done && step.until?.(stateRef.current || {})) { done = true; nextRef.current(); return }
      const el = findTarget(step.target)
      if (el && performance.now() < scrollUntil) el.scrollIntoView({ block: 'nearest' })
      const r = el?.getBoundingClientRect()
      const key = r ? `${r.top}|${r.left}|${r.width}|${r.height}` : ''
      if (key !== last) {
        last = key
        setRect(r ? { top: r.top, left: r.left, width: r.width, height: r.height, right: r.right, bottom: r.bottom } : null)
      }
      frame = requestAnimationFrame(measure)
    }
    measure()
    return () => cancelAnimationFrame(frame)
  }, [open, index]) // eslint-disable-line react-hooks/exhaustive-deps

  // action: 'click' -- move on once the page has handled the click.
  useEffect(() => {
    if (!open || step?.action !== 'click') return
    const onClick = e => {
      if (findTarget(step.target)?.contains(e.target)) setTimeout(() => nextRef.current(), 0)
    }
    document.addEventListener('click', onClick, true)
    return () => document.removeEventListener('click', onClick, true)
  }, [open, index]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!open) return
    const onKey = e => {
      if (e.key === 'Escape') onClose()
      else if (waiting) return // the user does this step themselves
      else if (e.key === 'ArrowRight' || e.key === 'Enter') { e.preventDefault(); next() }
      else if (e.key === 'ArrowLeft') back()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, next, back, onClose, waiting])

  if (!open || !step) return null
  const pos = cardPosition(rect, cardH)
  // After a hands-on step the app has moved on, so going back would point at
  // something that's gone.
  const canBack = index > 0 && !handsOn(step) && !handsOn(steps[index - 1])
  const skipTo = step.skip ? steps.findIndex(s => s.id === step.skip.to) : -1

  // Clicks outside the card don't reach the page, except on the target of a
  // hands-on step: the blocker then leaves a hole around it.
  const hole = waiting && rect ? {
    top: rect.top - PAD, left: rect.left - PAD, bottom: rect.bottom + PAD, right: rect.right + PAD,
  } : null
  const block = e => e.stopPropagation()

  return (
    <div className="fixed inset-0 z-[100] pointer-events-none" role="dialog" aria-modal="true" aria-label={t('Dashboard tour')}>
      {rect ? (
        // Spotlight: a transparent box over the target whose huge shadow dims the rest.
        <div className="absolute rounded-xl pointer-events-none transition-all duration-200"
          style={{
            top: rect.top - PAD, left: rect.left - PAD, width: rect.width + PAD * 2, height: rect.height + PAD * 2,
            boxShadow: `0 0 0 9999px rgba(3,7,18,0.72), 0 0 0 2px rgba(0,184,230,0.9)${waiting ? ', 0 0 18px 4px rgba(0,184,230,0.55)' : ''}`,
          }} />
      ) : (
        <div className="absolute inset-0" style={{ background: 'rgba(3,7,18,0.72)' }} />
      )}
      {hole ? (
        <>
          <div className="absolute inset-x-0 top-0 pointer-events-auto" style={{ height: Math.max(0, hole.top) }} onClick={block} />
          <div className="absolute inset-x-0 bottom-0 pointer-events-auto" style={{ top: hole.bottom }} onClick={block} />
          <div className="absolute left-0 pointer-events-auto" style={{ top: hole.top, height: hole.bottom - hole.top, width: Math.max(0, hole.left) }} onClick={block} />
          <div className="absolute right-0 pointer-events-auto" style={{ top: hole.top, height: hole.bottom - hole.top, left: hole.right }} onClick={block} />
        </>
      ) : (
        <div className="absolute inset-0 pointer-events-auto" onClick={block} />
      )}

      <div ref={cardRef} className="absolute rounded-2xl border border-white/10 shadow-2xl p-5 transition-all duration-200 pointer-events-auto"
        style={{ ...pos, background: '#0d1426' }}>
        <div className="flex items-center justify-between mb-2">
          <span className="text-[10px] font-bold uppercase tracking-widest text-cyan-400">
            {step.eyebrow || 'Tour'} · {index + 1}/{steps.length}
          </span>
          <button onClick={onClose} className="text-[11px] text-slate-500 hover:text-white">{t('Skip tour')}</button>
        </div>
        <p className="text-base font-bold text-white mb-1.5">{step.title}</p>
        <div className="text-sm text-slate-300 leading-relaxed space-y-2">{step.body}</div>

        <div className="mt-4 flex items-center gap-1.5">
          {steps.map((_, i) => (
            <span key={i} className={`h-1.5 rounded-full transition-all ${i === index ? 'w-5 bg-cyan-400' : i < index ? 'w-1.5 bg-cyan-400/40' : 'w-1.5 bg-white/20'}`} />
          ))}
          <div className="ml-auto flex items-center gap-2">
            {skipTo > 0 && (
              <button onClick={() => setIndex(skipTo)} className="text-[11px] text-slate-400 hover:text-white whitespace-nowrap">
                {step.skip.label}
              </button>
            )}
            {canBack && (
              <button onClick={back} className="px-3 py-1.5 rounded-lg text-xs font-semibold text-slate-300 border border-white/10 hover:text-white">
                {t('Back')}
              </button>
            )}
            {waiting ? (
              <span className="px-3 py-1.5 rounded-lg text-xs font-semibold text-cyan-300 border border-cyan-400/30 bg-cyan-400/10 whitespace-nowrap">
                {step.waitLabel || t('Your turn')}
              </span>
            ) : (
              <button onClick={next} className="px-4 py-1.5 rounded-lg text-xs font-bold text-white"
                style={{ background: 'linear-gradient(135deg,#00b8e6 0%,#1e2fa0 100%)' }}>
                {last ? t('Finish') : t('Next')}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
