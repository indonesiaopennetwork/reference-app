import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.jsx'
import Reconciliation from './Reconciliation.jsx'

// /reconciliation is the operator view (guarded by its own operator key), kept
// apart from the shop and its buyer login.
const Root = window.location.pathname.startsWith('/reconciliation') ? Reconciliation : App

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
