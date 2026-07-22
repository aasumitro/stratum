import type { ReactNode } from "react"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { FieldGroup } from "@/components/ui/field"

interface FormSectionProps {
  title: string
  description?: string
  children: ReactNode
  actions?: ReactNode
}

/**
 * Card-wrapped form section: title + description + FieldGroup body.
 * Composes settings/account pages from one consistent shape instead of each
 * page hand-rolling its own Card + heading + spacing.
 */
export function FormSection({
  title,
  description,
  children,
  actions,
}: FormSectionProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
        {actions && <CardAction>{actions}</CardAction>}
      </CardHeader>
      <CardContent>
        <FieldGroup>{children}</FieldGroup>
      </CardContent>
    </Card>
  )
}
