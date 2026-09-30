import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.jsx'
import { I18nProvider, LanguagePicker } from './i18n.jsx'

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <I18nProvider>
      <App />
      <LanguagePicker />
    </I18nProvider>
  </StrictMode>,
)
