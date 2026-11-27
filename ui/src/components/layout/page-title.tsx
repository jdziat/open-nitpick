import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

const Ctx = createContext<{ title?: string; setTitle: (t?: string) => void }>({ setTitle: () => {} })

/** Lets a page name itself in the breadcrumb and the tab title, for a page that shows one thing among many. */
export function PageTitleProvider({ children }: { children: ReactNode }) {
  const [title, setTitle] = useState<string>()
  return <Ctx.Provider value={{ title, setTitle }}>{children}</Ctx.Provider>
}

export const usePageTitleValue = () => useContext(Ctx).title

export function usePageTitle(title: string | undefined) {
  const { setTitle } = useContext(Ctx)
  useEffect(() => {
    setTitle(title)
    return () => setTitle(undefined)
  }, [title, setTitle])
}
