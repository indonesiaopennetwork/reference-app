import { Plus, X } from 'lucide-react'
import crc from './crc.json'

// ION Catalogue Resource Categories (CRCs) for the Trade sector, and the
// resourceAttributes fields each one adds, generated from ion-specs by
// scripts/gen_crc_fields.py (docs/ION_Resource_Categories.md and
// TradeResource/v1/attributes.yaml). A catalog has one CRC; its products are
// published with resourceAttributes.crc set to it and the CRC's sub-objects
// (fashion, electronics, food, ...) filled from the fields below.

export const TRADE_CRCS = crc.categories
export const CRC_FIELD = crc.crcField

export const crcByCode = code => TRADE_CRCS.find(c => c.code === code) || null

// "HALAL_COSMETICS" -> "Halal cosmetics"
const optionLabel = o => {
  const s = String(o).replace(/_/g, ' ').toLowerCase()
  return s.charAt(0).toUpperCase() + s.slice(1)
}

// The catalog's CRC: ACTIVE categories can be chosen, INACTIVE ones too (no
// checks yet), GOVERNED ones are blocked by ION.
export function CrcSelect({ value, onChange, inputCls, t }) {
  const selected = crcByCode(value)
  return (
    <div>
      <select className={inputCls} value={value} onChange={e => onChange(e.target.value)}>
        <option value="" className="bg-slate-900">{t('— Select a category —')}</option>
        {TRADE_CRCS.map(c => (
          <option key={c.code} value={c.code} disabled={c.status === 'GOVERNED'} className="bg-slate-900">
            {c.name} ({c.code}){c.status === 'INACTIVE' ? ` · ${t('no checks yet')}` : c.status === 'GOVERNED' ? ` · ${t('blocked by ION')}` : ''}
          </option>
        ))}
      </select>
      {selected && (
        <p className="mt-1.5 text-[11px] text-slate-500 leading-relaxed">
          {selected.description}
          {selected.sections.length > 0
            ? <> <span className="text-slate-400">{t('Adds: {sections}', { sections: selected.sections.map(s => s.label).join(', ') })}</span></>
            : <> <span className="text-slate-400">{t('Common product fields only.')}</span></>}
        </p>
      )}
    </div>
  )
}

// ── Values ──────────────────────────────────────────────────────────────────
// The form keeps each section as the schema's object shape, with number and
// quantity values as strings while editing. clean() turns it into what is
// published; toForm() reverses it for editing.

const isEmpty = v => v === undefined || v === null || v === '' ||
  (Array.isArray(v) && v.length === 0) || (typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length === 0)

function cleanField(f, v) {
  switch (f.kind) {
    case 'text':
      if (f.multiple) {
        const items = String(v ?? '').split(',').map(s => s.trim()).filter(Boolean)
        return items.length ? items : undefined
      }
      return String(v ?? '').trim() || undefined
    case 'number': {
      if (v === '' || v == null) return undefined
      const n = Number(v)
      return Number.isNaN(n) ? undefined : (f.integer ? Math.trunc(n) : n)
    }
    case 'boolean':
      return v === true || v === false ? v : undefined
    case 'select':
      return v || undefined
    case 'multiselect':
      return Array.isArray(v) && v.length ? v : undefined
    case 'quantity': {
      const q = v || {}
      if (q.unitQuantity === '' || q.unitQuantity == null || Number.isNaN(Number(q.unitQuantity))) return undefined
      // unitText: the unit as written (W, mAh, GB); Quantity's unitCode is for coded units.
      return { unitQuantity: Number(q.unitQuantity), ...(String(q.unitText || '').trim() ? { unitText: String(q.unitText).trim() } : {}) }
    }
    case 'group':
      return clean(f.fields, v)
    case 'list': {
      const rows = (Array.isArray(v) ? v : []).map(r => clean(f.fields, r)).filter(r => r !== undefined)
      return rows.length ? rows : undefined
    }
    default:
      return undefined
  }
}

function clean(fields, obj) {
  const out = {}
  for (const f of fields) {
    const v = cleanField(f, obj?.[f.key])
    if (!isEmpty(v)) out[f.key] = v
  }
  return Object.keys(out).length ? out : undefined
}

function fieldToForm(f, v) {
  if (v === undefined || v === null) return undefined
  switch (f.kind) {
    case 'text': return f.multiple && Array.isArray(v) ? v.join(', ') : String(v)
    case 'number': return String(v)
    case 'quantity': return { unitQuantity: v.unitQuantity == null ? '' : String(v.unitQuantity), unitText: v.unitText || v.unitCode || '' }
    case 'group': return toForm(f.fields, v)
    case 'list': return Array.isArray(v) ? v.map(r => toForm(f.fields, r)) : []
    default: return v
  }
}

function toForm(fields, obj) {
  const out = {}
  for (const f of fields) {
    const v = fieldToForm(f, obj?.[f.key])
    if (v !== undefined) out[f.key] = v
  }
  return out
}

// missingRequired lists required fields left empty inside groups and list
// rows that are otherwise filled in (a group that isn't used is fine).
function missingRequired(fields, obj, path) {
  const missing = []
  for (const f of fields) {
    const v = obj?.[f.key]
    if (f.kind === 'group' && clean(f.fields, v)) {
      missing.push(...requiredIn(f.fields, v, `${path}${f.label} › `))
    } else if (f.kind === 'list') {
      (Array.isArray(v) ? v : []).forEach((row, i) => {
        if (clean(f.fields, row)) missing.push(...requiredIn(f.fields, row, `${path}${f.label} ${i + 1} › `))
      })
    }
  }
  return missing
}

function requiredIn(fields, obj, path) {
  const missing = fields.filter(f => f.required && isEmpty(cleanField(f, obj?.[f.key]))).map(f => path + f.label)
  return [...missing, ...missingRequired(fields, obj, path)]
}

// crcResourceAttributes returns the resourceAttributes the CRC adds -- {crc,
// <section>: {...}} -- from the form values, or throws listing the required
// fields left empty.
export function crcResourceAttributes(code, values) {
  const c = crcByCode(code)
  if (!c) return {}
  const missing = c.sections.flatMap(s => missingRequired(s.fields, values?.[s.key], `${s.label} › `))
  if (missing.length) {
    const err = new Error(missing.join(', '))
    err.missing = missing
    throw err
  }
  const out = { [CRC_FIELD]: c.code }
  for (const s of c.sections) {
    const v = clean(s.fields, values?.[s.key])
    if (v) out[s.key] = v
  }
  return out
}

// crcFormValues reads a stored resourceAttributes back into form values.
export function crcFormValues(code, attrs) {
  const c = crcByCode(code)
  if (!c || !attrs) return {}
  const out = {}
  for (const s of c.sections) {
    if (attrs[s.key]) out[s.key] = toForm(s.fields, attrs[s.key])
  }
  return out
}

// ── Form ────────────────────────────────────────────────────────────────────

function Field({ f, value, onChange, labelCls, inputCls, t }) {
  const label = (
    <label className={labelCls} title={f.help}>
      {t(f.label)}{f.required ? ' *' : ''}
      {f.help && <span className="ml-1 normal-case tracking-normal font-normal text-slate-600 cursor-help">ⓘ</span>}
    </label>
  )
  switch (f.kind) {
    case 'select':
      return (
        <div>{label}
          <select className={inputCls} value={value || ''} onChange={e => onChange(e.target.value)}>
            <option value="" className="bg-slate-900">—</option>
            {f.options.map(o => <option key={o} value={o} className="bg-slate-900">{optionLabel(o)}</option>)}
          </select>
        </div>
      )
    case 'multiselect': {
      const sel = Array.isArray(value) ? value : []
      const toggle = o => onChange(sel.includes(o) ? sel.filter(x => x !== o) : [...sel, o])
      return (
        <div className="col-span-full">{label}
          <div className="flex flex-wrap gap-1.5">
            {f.options.map(o => (
              <button type="button" key={o} onClick={() => toggle(o)}
                className={`px-2.5 py-1 rounded-full text-[11px] font-medium border transition-colors ${sel.includes(o)
                  ? 'bg-blue-500/20 border-blue-500/50 text-blue-200' : 'border-white/[0.08] text-slate-400 hover:text-slate-200'}`}>
                {optionLabel(o)}
              </button>
            ))}
          </div>
        </div>
      )
    }
    case 'boolean':
      return (
        <div>{label}
          <select className={inputCls} value={value === true ? 'true' : value === false ? 'false' : ''}
            onChange={e => onChange(e.target.value === '' ? undefined : e.target.value === 'true')}>
            <option value="" className="bg-slate-900">—</option>
            <option value="true" className="bg-slate-900">{t('Yes')}</option>
            <option value="false" className="bg-slate-900">{t('No')}</option>
          </select>
        </div>
      )
    case 'number':
      return (
        <div>{label}
          <input className={inputCls} type="number" step={f.integer ? '1' : 'any'} value={value ?? ''}
            onChange={e => onChange(e.target.value)} placeholder={f.example != null ? String(f.example) : ''} />
        </div>
      )
    case 'quantity': {
      const q = value || {}
      return (
        <div>{label}
          <div className="flex gap-2">
            <input className={inputCls} type="number" step="any" value={q.unitQuantity ?? ''}
              onChange={e => onChange({ ...q, unitQuantity: e.target.value })}
              placeholder={f.example?.unitQuantity != null ? String(f.example.unitQuantity) : t('Value')} />
            <input className={`${inputCls.replace('w-full', 'w-28')} shrink-0`} value={q.unitText ?? ''}
              onChange={e => onChange({ ...q, unitText: e.target.value })} placeholder={f.example?.unitText || t('Unit')} />
          </div>
        </div>
      )
    }
    case 'group':
      return (
        <div className="col-span-full rounded-lg border border-white/[0.06] p-3">
          <p className="text-[11px] font-semibold text-slate-400 mb-2" title={f.help}>{t(f.label)}</p>
          <Fields fields={f.fields} value={value} onChange={onChange} labelCls={labelCls} inputCls={inputCls} t={t} />
        </div>
      )
    case 'list': {
      const rows = Array.isArray(value) ? value : []
      const setRow = (i, r) => onChange(rows.map((x, j) => (j === i ? r : x)))
      return (
        <div className="col-span-full rounded-lg border border-white/[0.06] p-3 space-y-2">
          <div className="flex items-center justify-between">
            <p className="text-[11px] font-semibold text-slate-400" title={f.help}>{t(f.label)}</p>
            <button type="button" onClick={() => onChange([...rows, {}])}
              className="flex items-center gap-1 text-[11px] font-semibold text-blue-300 hover:text-blue-200">
              <Plus size={12} /> {t('Add')}
            </button>
          </div>
          {rows.length === 0 && <p className="text-[11px] text-slate-600">{t('None added.')}</p>}
          {rows.map((r, i) => (
            <div key={i} className="relative rounded-md bg-white/[0.02] p-2 pr-8">
              <button type="button" onClick={() => onChange(rows.filter((_, j) => j !== i))} title={t('Remove')}
                className="absolute right-2 top-2 text-slate-500 hover:text-rose-300"><X size={13} /></button>
              <Fields fields={f.fields} value={r} onChange={v => setRow(i, v)} labelCls={labelCls} inputCls={inputCls} t={t} />
            </div>
          ))}
        </div>
      )
    }
    default: // text
      return (
        <div>{label}
          <input className={inputCls} value={value ?? ''} onChange={e => onChange(e.target.value)}
            placeholder={f.multiple ? t('Comma-separated') : (f.example != null ? String(f.example) : '')} />
        </div>
      )
  }
}

function Fields({ fields, value, onChange, labelCls, inputCls, t }) {
  const v = value || {}
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
      {fields.map(f => (
        <Field key={f.key} f={f} value={v[f.key]} labelCls={labelCls} inputCls={inputCls} t={t}
          onChange={x => onChange({ ...v, [f.key]: x })} />
      ))}
    </div>
  )
}

// CrcAttributes shows the fields the CRC adds, one block per section.
export function CrcAttributes({ code, values, onChange, labelCls, inputCls, t }) {
  const c = crcByCode(code)
  if (!c || c.sections.length === 0) return null
  return (
    <div className="space-y-4">
      {c.sections.map(s => (
        <div key={s.key}>
          <p className="text-xs font-semibold text-slate-300 mb-1">{t(s.label)}</p>
          {s.help && <p className="text-[11px] text-slate-500 mb-3 leading-relaxed">{s.help}</p>}
          <Fields fields={s.fields} value={values?.[s.key]} labelCls={labelCls} inputCls={inputCls} t={t}
            onChange={v => onChange({ ...(values || {}), [s.key]: v })} />
        </div>
      ))}
    </div>
  )
}

// ── Seller-defined attributes ───────────────────────────────────────────────
// resourceAttributes.additionalAttributes (IONTradeResource): attributes the
// schema has no field for, as name / value pairs with an optional unit. Shown
// on the product page; not used for filtering.

export const CUSTOM_ATTRIBUTES_FIELD = 'additionalAttributes'
const MAX_CUSTOM = 30

// customAttributes returns the rows to publish (empty ones dropped), or
// throws listing rows with a value but no name or the other way round.
export function customAttributes(rows) {
  const out = []
  const incomplete = []
  ;(rows || []).forEach((r, i) => {
    const name = String(r.name || '').trim(), value = String(r.value || '').trim(), unit = String(r.unit || '').trim()
    if (!name && !value && !unit) return
    if (!name || !value) { incomplete.push(i + 1); return }
    out.push({ name, value, ...(unit ? { unit } : {}) })
  })
  if (incomplete.length) {
    const err = new Error(incomplete.join(', '))
    err.incomplete = incomplete
    throw err
  }
  return out.slice(0, MAX_CUSTOM)
}

export const customFormRows = attrs =>
  (Array.isArray(attrs?.[CUSTOM_ATTRIBUTES_FIELD]) ? attrs[CUSTOM_ATTRIBUTES_FIELD] : [])
    .map(r => ({ name: r.name || '', value: r.value == null ? '' : String(r.value), unit: r.unit || '' }))

export function CustomAttributes({ rows, onChange, inputCls, t }) {
  const list = rows || []
  const set = (i, k, v) => onChange(list.map((r, j) => (j === i ? { ...r, [k]: v } : r)))
  return (
    <div className="space-y-2">
      {list.length === 0 && <p className="text-[11px] text-slate-600">{t('None added. Add anything the fields above don\u2019t cover, e.g. the artisan or the making time.')}</p>}
      {list.map((r, i) => (
        <div key={i} className="flex gap-2 items-start">
          <input className={inputCls} value={r.name} maxLength={60} onChange={e => set(i, 'name', e.target.value)} placeholder={t('Name, e.g. Pengrajin')} />
          <input className={inputCls} value={r.value} maxLength={500} onChange={e => set(i, 'value', e.target.value)} placeholder={t('Value')} />
          <input className={`${inputCls.replace('w-full', 'w-24')} shrink-0`} value={r.unit} maxLength={20} onChange={e => set(i, 'unit', e.target.value)} placeholder={t('Unit')} />
          <button type="button" onClick={() => onChange(list.filter((_, j) => j !== i))} title={t('Remove')}
            className="mt-2 text-slate-500 hover:text-rose-300 shrink-0"><X size={14} /></button>
        </div>
      ))}
      {list.length < MAX_CUSTOM && (
        <button type="button" onClick={() => onChange([...list, { name: '', value: '', unit: '' }])}
          className="flex items-center gap-1 text-[11px] font-semibold text-blue-300 hover:text-blue-200">
          <Plus size={12} /> {t('Add attribute')}
        </button>
      )}
    </div>
  )
}
