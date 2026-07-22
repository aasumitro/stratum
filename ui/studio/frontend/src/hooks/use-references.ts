import { wailsError } from "@/lib/ui"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import type { CountryInput, CurrencyInput } from "../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { ReferenceService } from "../../bindings/github.com/aasumitro/stratum/studio/app/index.js"

export function useCountries(projectId: string) {
  return useQuery({
    queryKey: ["ref", "countries", projectId],
    queryFn: () => ReferenceService.ListCountries(projectId),
    staleTime: 300_000,
    retry: 1,
  })
}

export function useCurrencies(projectId: string) {
  return useQuery({
    queryKey: ["ref", "currencies", projectId],
    queryFn: () => ReferenceService.ListCurrencies(projectId),
    staleTime: 300_000,
    retry: 1,
  })
}

export function useCreateCountry(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: CountryInput) => ReferenceService.CreateCountry(projectId, input),
    onSuccess: () => {
      toast.success("Country created")
      qc.invalidateQueries({ queryKey: ["ref", "countries", projectId] })
    },
    onError: (err) => toast.error(`Create failed: ${wailsError(err)}`),
  })
}

export function useUpdateCountry(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ code, input }: { code: string; input: CountryInput }) =>
      ReferenceService.UpdateCountry(projectId, code, input),
    onSuccess: () => {
      toast.success("Country updated")
      qc.invalidateQueries({ queryKey: ["ref", "countries", projectId] })
    },
    onError: (err) => toast.error(`Update failed: ${wailsError(err)}`),
  })
}

export function useDeleteCountry(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => ReferenceService.DeleteCountry(projectId, code),
    onSuccess: () => {
      toast.success("Country deleted")
      qc.invalidateQueries({ queryKey: ["ref", "countries", projectId] })
    },
    onError: (err) => toast.error(`Delete failed: ${wailsError(err)}`),
  })
}

export function useCreateCurrency(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: CurrencyInput) => ReferenceService.CreateCurrency(projectId, input),
    onSuccess: () => {
      toast.success("Currency created")
      qc.invalidateQueries({ queryKey: ["ref", "currencies", projectId] })
    },
    onError: (err) => toast.error(`Create failed: ${wailsError(err)}`),
  })
}

export function useUpdateCurrency(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ code, input }: { code: string; input: CurrencyInput }) =>
      ReferenceService.UpdateCurrency(projectId, code, input),
    onSuccess: () => {
      toast.success("Currency updated")
      qc.invalidateQueries({ queryKey: ["ref", "currencies", projectId] })
    },
    onError: (err) => toast.error(`Update failed: ${wailsError(err)}`),
  })
}

export function useDeleteCurrency(projectId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => ReferenceService.DeleteCurrency(projectId, code),
    onSuccess: () => {
      toast.success("Currency deleted")
      qc.invalidateQueries({ queryKey: ["ref", "currencies", projectId] })
    },
    onError: (err) => toast.error(`Delete failed: ${wailsError(err)}`),
  })
}

