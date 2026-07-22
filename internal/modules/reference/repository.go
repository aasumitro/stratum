package reference

import (
	"context"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

type countryRecord struct {
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	PhoneCode    string    `json:"phone_code"`
	CurrencyCode string    `json:"currency_code"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
}

type currencyRecord struct {
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Symbol        string    `json:"symbol"`
	DecimalPlaces int       `json:"decimal_places"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
}

type repository struct{}

func (r *repository) listCountries(ctx context.Context, q db.Querier) ([]countryRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT code, name, phone_code, currency_code, active, created_at
		FROM ref.countries
		WHERE active = true
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []countryRecord
	for rows.Next() {
		var c countryRecord
		if err := rows.Scan(&c.Code, &c.Name, &c.PhoneCode, &c.CurrencyCode, &c.Active, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *repository) listCurrencies(ctx context.Context, q db.Querier) ([]currencyRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT code, name, symbol, decimal_places, active, created_at
		FROM ref.currencies
		WHERE active = true
		ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []currencyRecord
	for rows.Next() {
		var c currencyRecord
		if err := rows.Scan(&c.Code, &c.Name, &c.Symbol, &c.DecimalPlaces, &c.Active, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *repository) getCountryTaxRate(ctx context.Context, q db.Querier, code string) (int, error) {
	var rate int
	err := q.QueryRow(ctx, `SELECT tax_rate_bps FROM ref.countries WHERE code = $1`, code).Scan(&rate)
	return rate, err
}
