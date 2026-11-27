import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes } from 'react-router'
import { AppSidebar } from '@/components/layout/app-sidebar'
import { PageTitleProvider } from '@/components/layout/page-title'
import { SiteHeader } from '@/components/layout/site-header'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { hasComponent, hasRole } from '@/lib/session'
import NotFound, { home, NoAccess } from '@/pages/not-found'
import { Skeleton } from '@/components/ui/skeleton'

// Each page loads when its route is first visited, so the charting library
// reaches the browser only with the pages that draw charts.
const Findings = lazy(() => import('@/pages/findings'))
const Hotspots = lazy(() => import('@/pages/hotspots'))
const Overview = lazy(() => import('@/pages/overview'))
const RecordDetail = lazy(() => import('@/pages/record-detail'))
const Records = lazy(() => import('@/pages/records'))
const Repositories = lazy(() => import('@/pages/repos'))
const ReviewDetail = lazy(() => import('@/pages/review-detail'))
const Reviews = lazy(() => import('@/pages/reviews'))

function Loading() {
  return (
    <div className="flex flex-col gap-4" role="status" aria-label="Loading the page">
      <Skeleton className="h-8 w-48" />
      <Skeleton className="h-64 w-full" />
    </div>
  )
}

export function App() {
  const insights = hasComponent('insights') && hasRole('insights')
  const notary = hasComponent('notary') && hasRole('authoritative')
  const start = home()
  return (
    <TooltipProvider>
      <SidebarProvider>
        <PageTitleProvider>
          <AppSidebar />
          <SidebarInset className="min-w-0">
            <SiteHeader />
            <main className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-6 p-4 md:p-6">
              <Suspense fallback={<Loading />}>
              <Routes>
                <Route index element={start ? <Navigate to={start} replace /> : <NoAccess />} />
                {insights && (
                  <>
                    <Route path="insights" element={<Overview />} />
                    <Route path="insights/hotspots" element={<Hotspots />} />
                    <Route path="insights/findings" element={<Findings />} />
                    <Route path="insights/reviews" element={<Reviews />} />
                    <Route path="insights/reviews/:id" element={<ReviewDetail />} />
                    <Route path="insights/repos" element={<Repositories />} />
                  </>
                )}
                {notary && (
                  <>
                    <Route path="notary" element={<Records />} />
                    <Route path="notary/records/:hash" element={<RecordDetail />} />
                  </>
                )}
                <Route path="*" element={<NotFound />} />
              </Routes>
              </Suspense>
            </main>
          </SidebarInset>
        </PageTitleProvider>
      </SidebarProvider>
    </TooltipProvider>
  )
}
