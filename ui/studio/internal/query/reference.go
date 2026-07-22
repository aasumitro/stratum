package query

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type RefCountry struct {
	Code         string
	Name         string
	PhoneCode    string
	CurrencyCode string
	TaxRateBps   int
	Active       bool
}

type RefCurrency struct {
	Code          string
	Name          string
	Symbol        string
	DecimalPlaces int
	Active        bool
}

func ListRefCountries(ctx context.Context, pool *pgxpool.Pool) ([]RefCountry, error) {
	rows, err := pool.Query(ctx, `
		SELECT code, name, phone_code, currency_code, tax_rate_bps, active
		FROM ref.countries
		ORDER BY active DESC, name
	`)
	if err != nil {
		return nil, fmt.Errorf("query.ListRefCountries: %w", err)
	}
	defer rows.Close()

	var out []RefCountry
	for rows.Next() {
		var c RefCountry
		if err := rows.Scan(&c.Code, &c.Name, &c.PhoneCode, &c.CurrencyCode, &c.TaxRateBps, &c.Active); err != nil {
			return nil, fmt.Errorf("query.ListRefCountries: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

func ListRefCurrencies(ctx context.Context, pool *pgxpool.Pool) ([]RefCurrency, error) {
	rows, err := pool.Query(ctx, `
		SELECT code, name, symbol, decimal_places, active
		FROM ref.currencies
		ORDER BY active DESC, code
	`)
	if err != nil {
		return nil, fmt.Errorf("query.ListRefCurrencies: %w", err)
	}
	defer rows.Close()

	var out []RefCurrency
	for rows.Next() {
		var c RefCurrency
		if err := rows.Scan(&c.Code, &c.Name, &c.Symbol, &c.DecimalPlaces, &c.Active); err != nil {
			return nil, fmt.Errorf("query.ListRefCurrencies: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

func CreateRefCountry(ctx context.Context, pool *pgxpool.Pool,
	code, name, phoneCode, currencyCode string, taxRateBps int, active bool,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO ref.countries (code, name, phone_code, currency_code, tax_rate_bps, active)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, code, name, phoneCode, currencyCode, taxRateBps, active)
	if err != nil {
		return fmt.Errorf("query.CreateRefCountry: %w", err)
	}
	return nil
}

func UpdateRefCountry(ctx context.Context, pool *pgxpool.Pool,
	code, name, phoneCode, currencyCode string, taxRateBps int, active bool,
) error {
	tag, err := pool.Exec(ctx, `
		UPDATE ref.countries
		SET name=$2, phone_code=$3, currency_code=$4, tax_rate_bps=$5, active=$6
		WHERE code=$1
	`, code, name, phoneCode, currencyCode, taxRateBps, active)
	if err != nil {
		return fmt.Errorf("query.UpdateRefCountry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("query.UpdateRefCountry: country not found")
	}
	return nil
}

func DeleteRefCountry(ctx context.Context, pool *pgxpool.Pool, code string) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM ref.countries
		WHERE code = $1
		  AND NOT EXISTS (SELECT 1 FROM organization.organizations WHERE country_code = $1)
	`, code)
	if err != nil {
		return fmt.Errorf("query.DeleteRefCountry: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var count int64
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM organization.organizations WHERE country_code = $1
	`, code).Scan(&count); err != nil {
		return fmt.Errorf("query.DeleteRefCountry: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("country %q is used by %d organization(s) — deactivate it instead of deleting", code, count)
	}
	return fmt.Errorf("query.DeleteRefCountry: country not found")
}

func CreateRefCurrency(ctx context.Context, pool *pgxpool.Pool,
	code, name, symbol string, decimalPlaces int, active bool,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO ref.currencies (code, name, symbol, decimal_places, active)
		VALUES ($1, $2, $3, $4, $5)
	`, code, name, symbol, decimalPlaces, active)
	if err != nil {
		return fmt.Errorf("query.CreateRefCurrency: %w", err)
	}
	return nil
}

func UpdateRefCurrency(ctx context.Context, pool *pgxpool.Pool,
	code, name, symbol string, decimalPlaces int, active bool,
) error {
	tag, err := pool.Exec(ctx, `
		UPDATE ref.currencies
		SET name=$2, symbol=$3, decimal_places=$4, active=$5
		WHERE code=$1
	`, code, name, symbol, decimalPlaces, active)
	if err != nil {
		return fmt.Errorf("query.UpdateRefCurrency: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("query.UpdateRefCurrency: currency not found")
	}
	return nil
}

func DeleteRefCurrency(ctx context.Context, pool *pgxpool.Pool, code string) error {
	tag, err := pool.Exec(ctx, `
		DELETE FROM ref.currencies
		WHERE code = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM billing.subscriptions
		      WHERE currency = $1 AND status IN ('active', 'trialing')
		  )
		  AND NOT EXISTS (SELECT 1 FROM ref.countries WHERE currency_code = $1)
	`, code)
	if err != nil {
		return fmt.Errorf("query.DeleteRefCurrency: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var subCount, countryCount int64
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM billing.subscriptions WHERE currency = $1 AND status IN ('active', 'trialing')),
		       (SELECT COUNT(*) FROM ref.countries WHERE currency_code = $1)
	`, code).Scan(&subCount, &countryCount); err != nil {
		return fmt.Errorf("query.DeleteRefCurrency: %w", err)
	}
	if subCount > 0 {
		return fmt.Errorf("currency %q is used by %d active subscription(s) — deactivate it instead of deleting", code, subCount)
	}
	if countryCount > 0 {
		return fmt.Errorf("currency %q is assigned to %d country record(s) — update those countries first", code, countryCount)
	}
	return fmt.Errorf("query.DeleteRefCurrency: currency not found")
}
