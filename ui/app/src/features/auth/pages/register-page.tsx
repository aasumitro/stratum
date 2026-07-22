import { AuthLayout } from "@/features/auth/components/auth-layout"
import { RegisterForm } from "@/features/auth/components/register-form"

export function RegisterPage() {
  return (
    <AuthLayout>
      <RegisterForm />
    </AuthLayout>
  )
}
