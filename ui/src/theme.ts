// Runs before the page paints, so a dark theme does not flash light. It is a
// separate file because the page's policy allows no inline script.
const stored = localStorage.getItem('nitpick-theme')
const dark = stored === 'dark' || ((stored === null || stored === 'system') && matchMedia('(prefers-color-scheme: dark)').matches)
document.documentElement.classList.toggle('dark', dark)
