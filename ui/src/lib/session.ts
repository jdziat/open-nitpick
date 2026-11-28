// The server renders who is signed in into a meta tag. The page holds nothing
// else about them: what they may see is decided again by the API on every request.
export type Session = {
  name: string
  provider: string
  roles: string[]
  components: string[]
  csrf: string
}

function read(): Session {
  const raw = document.querySelector<HTMLMetaElement>('meta[name="nitpick-session"]')?.content
  try {
    const s = JSON.parse(raw ?? '') as Session
    return { name: s.name, provider: s.provider, roles: s.roles ?? [], components: s.components ?? [], csrf: s.csrf }
  } catch {
    return { name: '', provider: '', roles: [], components: [], csrf: '' }
  }
}

export const session: Session = read()

export const hasComponent = (name: string) => session.components.includes(name)
export const hasRole = (role: string) => session.roles.includes(role)

/** Signs out through the server's form endpoint, then goes to the sign-in page. */
export async function signOut() {
  await fetch('/auth/logout', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ csrf_token: session.csrf }),
    redirect: 'manual',
  })
  window.location.assign('/auth/login')
}
