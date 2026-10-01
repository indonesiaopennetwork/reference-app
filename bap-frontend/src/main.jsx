import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.jsx'
import Reconciliation from './Reconciliation.jsx'
import { I18nProvider, LanguagePicker } from './i18n.jsx'
import { basePath } from './auth.js'

// /reconciliation is the settlement reconciliation view, kept apart from the
// shop and its buyer login.
const Root = window.location.pathname.startsWith(`${basePath}reconciliation`) ? Reconciliation : App

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <I18nProvider>
      <Root />
      <LanguagePicker />
    </I18nProvider>
  </StrictMode>,
)
