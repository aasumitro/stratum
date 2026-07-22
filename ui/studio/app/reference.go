package app

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/query"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Country struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	PhoneCode    string `json:"phone_code"`
	CurrencyCode string `json:"currency_code"`
	TaxRateBps   int    `json:"tax_rate_bps"`
	Active       bool   `json:"active"`
}

type Currency struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	DecimalPlaces int    `json:"decimal_places"`
	Active        bool   `json:"active"`
}

type CountryInput struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	PhoneCode    string `json:"phone_code"`
	CurrencyCode string `json:"currency_code"`
	TaxRateBps   int    `json:"tax_rate_bps"`
	Active       bool   `json:"active"`
}

type CurrencyInput struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	DecimalPlaces int    `json:"decimal_places"`
	Active        bool   `json:"active"`
}

type ReferenceService struct {
	projects *ProjectService
	pool     *connect.PostgresPool
}

func NewReferenceService(projects *ProjectService, pool *connect.PostgresPool) *ReferenceService {
	return &ReferenceService{projects: projects, pool: pool}
}

func (s *ReferenceService) ListCountries(projectID string) ([]Country, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "ReferenceService.ListCountries", 15*time.Second, query.ListRefCountries)
	if err != nil {
		return nil, err
	}

	out := make([]Country, len(raw))
	for i, c := range raw {
		out[i] = Country{
			Code:         c.Code,
			Name:         c.Name,
			PhoneCode:    c.PhoneCode,
			CurrencyCode: c.CurrencyCode,
			TaxRateBps:   c.TaxRateBps,
			Active:       c.Active,
		}
	}
	return out, nil
}

func (s *ReferenceService) ListCurrencies(projectID string) ([]Currency, error) {
	raw, err := withProjectDB(s.projects, s.pool, projectID, "ReferenceService.ListCurrencies", 15*time.Second, query.ListRefCurrencies)
	if err != nil {
		return nil, err
	}

	out := make([]Currency, len(raw))
	for i, c := range raw {
		out[i] = Currency{
			Code:          c.Code,
			Name:          c.Name,
			Symbol:        c.Symbol,
			DecimalPlaces: c.DecimalPlaces,
			Active:        c.Active,
		}
	}
	return out, nil
}

func (s *ReferenceService) CreateCountry(projectID string, input CountryInput) error {
	if input.Code == "" {
		return fmt.Errorf("ReferenceService.CreateCountry: code is required")
	}
	if input.Name == "" {
		return fmt.Errorf("ReferenceService.CreateCountry: name is required")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "ReferenceService.CreateCountry", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.CreateRefCountry(ctx, db, input.Code, input.Name, input.PhoneCode, input.CurrencyCode, input.TaxRateBps, input.Active)
		})
}

func (s *ReferenceService) UpdateCountry(projectID, code string, input CountryInput) error {
	if input.Name == "" {
		return fmt.Errorf("ReferenceService.UpdateCountry: name is required")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "ReferenceService.UpdateCountry", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpdateRefCountry(ctx, db, code, input.Name, input.PhoneCode, input.CurrencyCode, input.TaxRateBps, input.Active)
		})
}

func (s *ReferenceService) DeleteCountry(projectID, code string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "ReferenceService.DeleteCountry", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeleteRefCountry(ctx, db, code)
		})
}

func (s *ReferenceService) CreateCurrency(projectID string, input CurrencyInput) error {
	if input.Code == "" {
		return fmt.Errorf("ReferenceService.CreateCurrency: code is required")
	}
	if input.Name == "" {
		return fmt.Errorf("ReferenceService.CreateCurrency: name is required")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "ReferenceService.CreateCurrency", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.CreateRefCurrency(ctx, db, input.Code, input.Name, input.Symbol, input.DecimalPlaces, input.Active)
		})
}

func (s *ReferenceService) UpdateCurrency(projectID, code string, input CurrencyInput) error {
	if input.Name == "" {
		return fmt.Errorf("ReferenceService.UpdateCurrency: name is required")
	}

	return withProjectDBErr(s.projects, s.pool, projectID, "ReferenceService.UpdateCurrency", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.UpdateRefCurrency(ctx, db, code, input.Name, input.Symbol, input.DecimalPlaces, input.Active)
		})
}

func (s *ReferenceService) DeleteCurrency(projectID, code string) error {
	return withProjectDBErr(s.projects, s.pool, projectID, "ReferenceService.DeleteCurrency", 15*time.Second,
		func(ctx context.Context, db *pgxpool.Pool) error {
			return query.DeleteRefCurrency(ctx, db, code)
		})
}
