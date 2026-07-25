import { useState } from "react"
import { useTranslation } from "react-i18next"
import { IconCheck } from "@tabler/icons-react"
import { StepProfile } from "./steps/step-profile"
import { StepOrganization } from "./steps/step-organization"
import { BrandPanel } from "@/components/layout/brand-panel"
import { cn } from "@/lib/ui"

type Step = 1 | 2

function StepIndicator({
  current,
  steps,
}: {
  current: Step
  steps: { label: string }[]
}) {
  return (
    <div className="flex flex-col">
      {steps.map((s, i) => {
        const n = (i + 1) as Step
        const done = n < current
        const active = n === current
        return (
          <div key={n} className="flex items-start gap-3">
            <div className="flex flex-col items-center">
              <div
                className={cn(
                  "flex size-8 shrink-0 items-center justify-center rounded-full text-xs font-semibold transition-colors",
                  (done || active) && "bg-white text-neutral-900",
                  !done && !active && "bg-white/10 text-white/50"
                )}
              >
                {done ? <IconCheck className="size-4" /> : n}
              </div>
              {i < steps.length - 1 && (
                <div
                  className={cn(
                    "my-1 h-6 w-px",
                    n < current ? "bg-white" : "bg-white/20"
                  )}
                />
              )}
            </div>
            <span
              className={cn(
                "mt-1.5 text-sm",
                active ? "font-semibold text-white" : "text-white/60"
              )}
            >
              {s.label}
            </span>
          </div>
        )
      })}
    </div>
  )
}

export function OnboardingWizard({ initialStep }: { initialStep: 1 | 2 | 3 }) {
  const { t } = useTranslation()
  // Step 3 ("profile done, organization already exists") collapses onto step 2
  // — the organization step completes onboarding directly
  // on success (1c).
  const [step, setStep] = useState<Step>(initialStep === 3 ? 2 : initialStep)

  const STEPS = [
    { label: t("onboarding.steps.profile") },
    { label: t("onboarding.steps.organization") },
  ]

  return (
    <div className="flex min-h-svh">
      <BrandPanel className="lg:w-2/7">
        <div className="relative z-10">
          <span className="text-xl font-extrabold tracking-widest text-white/90 uppercase">
            Stratum
          </span>
          <p className="mt-1 text-xs font-medium tracking-widest text-white/50 uppercase">
            {t("onboarding.hero.eyebrow")}
          </p>
        </div>

        <div className="relative z-10 flex flex-1 flex-col justify-center">
          <StepIndicator current={step} steps={STEPS} />
        </div>

        <div className="relative z-10">
          <p className="mb-1 text-xs font-medium tracking-widest text-white/50 uppercase">
            {t("onboarding.steps.stepOf", {
              current: step,
              total: STEPS.length,
              label: STEPS[step - 1].label,
            })}
          </p>
          <h2 className="text-3xl leading-tight font-extrabold text-white">
            {t(
              step === 1
                ? "onboarding.hero.profileTitle"
                : "onboarding.hero.organizationTitle"
            )}
          </h2>
        </div>
      </BrandPanel>

      <div className="flex flex-1 items-center justify-center bg-background p-6 lg:p-16">
        {/* max-w moved into each step (step-profile.tsx / step-organization.tsx)
            so CreateOrganizationForm's Review step — the only one that needs
            a two-column layout — can be wider than the rest of onboarding. */}
        <div className="w-full">
          {step === 1 && <StepProfile onNext={() => setStep(2)} />}
          {step === 2 && <StepOrganization />}
        </div>
      </div>
    </div>
  )
}
