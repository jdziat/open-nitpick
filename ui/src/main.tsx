import { CSPProvider } from '@base-ui/react/csp-provider'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { App } from './app'
import { ThemeProvider } from './hooks/use-theme'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {/* The page's policy allows no inline <style>. Base UI renders one to hide native
        scrollbars; the same two rules are in index.css instead. */}
    <CSPProvider disableStyleElements>
      <ThemeProvider>
        <BrowserRouter basename="/ui">
          <App />
        </BrowserRouter>
      </ThemeProvider>
    </CSPProvider>
  </StrictMode>,
)
