import { AuthLayout } from "@/features/auth/components/auth-layout"
import { ResetPasswordForm } from "@/features/auth/components/reset-password-form"

export function ResetPasswordPage() {
  return (
    <AuthLayout>
      <ResetPasswordForm />
    </AuthLayout>
  )
}
