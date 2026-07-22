import { getRouteApi } from "@tanstack/react-router"
import { OnboardingWizard } from "@/features/onboarding/components/onboarding-wizard"

const routeApi = getRouteApi("/onboarding")

export function OnboardingPage() {
  const { initialStep } = routeApi.useLoaderData()
  return <OnboardingWizard initialStep={initialStep} />
}
