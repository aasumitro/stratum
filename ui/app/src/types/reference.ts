export interface Country {
  code: string
  name: string
  phone_code: string
  currency_code: string
  active: boolean
  created_at: string
}

export interface Currency {
  code: string
  name: string
  symbol: string
  decimal_places: number
  active: boolean
  created_at: string
}

export interface PlanPrices {
  monthly: number
  yearly: number
}

export interface Plan {
  id: string
  name: string
  description: string
  prices: Record<string, PlanPrices>
  limits: Record<string, number>
  // Kept nullable in the type so a future regression in API serialization
  // (e.g. nil-slice-serializes-to-null for plans with zero features) fails
  // typecheck instead of production, even though the API typically sends [].
  features: string[] | null
  sort_order: number
  // Raw config_value JSON for this plan's type="config" features
  // (e.g. api_rate_limit), keyed by feature id. Added alongside sort_order
  // in the catalog integration rework — see CATALOG.md.
  config_values: Record<string, unknown>
  active: boolean
  created_at: string
  updated_at: string
}

export interface Feature {
  id: string
  name: string
  description: string
  type: "metered" | "boolean" | "static" | "config"
  metric_key?: string
  active: boolean
  created_at: string
}

export interface Addon {
  id: string
  name: string
  description: string
  prices: Record<string, PlanPrices>
  features: Record<string, number> // feature_id -> limit_value delta
  active: boolean
  created_at: string
}
