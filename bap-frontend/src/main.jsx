import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.jsx'
import Reconciliation from './Reconciliation.jsx'
import QrBuyer from './qr/QrBuyer.jsx'
import { I18nProvider, LanguagePicker } from './i18n.jsx'
import { basePath } from './auth.js'

// /reconciliation is the settlement reconciliation view, kept apart from the
// shop and its buyer login. /qr-admin (the QR packet console) is a view of App. A scanned
// packet QR lands on /?packet_id=…&scan_id=… (qr-service's redirect) or
// /qr/<packet_id>: the QR checkout, which needs no login.
function pickRoot() {
  const path = window.location.pathname
  if (path.startsWith(`${basePath}reconciliation`)) return Reconciliation
  if (path.startsWith(`${basePath}qr/`) || new URLSearchParams(window.location.search).has('packet_id')) return QrBuyer
  return App
}
const Root = pickRoot()

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <I18nProvider>
      <Root />
      <LanguagePicker />
    </I18nProvider>
  </StrictMode>,
)
