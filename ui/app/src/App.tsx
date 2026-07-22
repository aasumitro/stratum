import { RouterProvider } from "@tanstack/react-router"
import { QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "@/components/ui/sonner"
import { useAuth } from "@/components/auth-provider"
import { queryClient } from "@/lib/query"
import { router } from "@/lib/router"

function App() {
  const auth = useAuth()

  // Wait until Supabase has restored the session from localStorage before
  // allowing the router to run beforeLoad guards. Without this gate, the
  // first render sees session=null and redirects to /login on every refresh.
  if (auth.loading) return null

  return (
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} context={{ queryClient, auth }} />
      <Toaster richColors position="top-right" />
    </QueryClientProvider>
  )
}

export default App
