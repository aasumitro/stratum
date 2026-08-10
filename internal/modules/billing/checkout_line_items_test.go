package billing

import "testing"

func TestBuildCheckoutLineItems(t *testing.T) {
	t.Run("no lines falls back to nil", func(t *testing.T) {
		if got := buildCheckoutLineItems(nil, 0); got != nil {
			t.Errorf("want nil, got %v", got)
		}
	})

	t.Run("plan + addon lines itemize, no tax", func(t *testing.T) {
		lines := []lineItemRecord{
			{Description: "Growth Plan (monthly)", Quantity: 1, UnitPriceCents: 2900, TotalCents: 2900},
			{Description: "Extra Seat", Quantity: 3, UnitPriceCents: 500, TotalCents: 1500},
		}
		got := buildCheckoutLineItems(lines, 0)
		if len(got) != 2 {
			t.Fatalf("want 2 items, got %d: %v", len(got), got)
		}
		if got[0].Name != "Growth Plan (monthly)" || got[0].UnitAmountCents != 2900 || got[0].Quantity != 1 {
			t.Errorf("unexpected first item: %+v", got[0])
		}
		if got[1].Name != "Extra Seat" || got[1].UnitAmountCents != 500 || got[1].Quantity != 3 {
			t.Errorf("unexpected second item: %+v", got[1])
		}
	})

	t.Run("positive tax appends its own trailing line", func(t *testing.T) {
		lines := []lineItemRecord{{Description: "Solo Plan (monthly)", Quantity: 1, UnitPriceCents: 900, TotalCents: 900}}
		got := buildCheckoutLineItems(lines, 99)
		if len(got) != 2 {
			t.Fatalf("want 2 items (plan + tax), got %d: %v", len(got), got)
		}
		if got[1].Name != "Tax" || got[1].UnitAmountCents != 99 || got[1].Quantity != 1 {
			t.Errorf("unexpected tax line: %+v", got[1])
		}
	})

	t.Run("zero tax adds no tax line", func(t *testing.T) {
		lines := []lineItemRecord{{Description: "Solo Plan (monthly)", Quantity: 1, UnitPriceCents: 900, TotalCents: 900}}
		got := buildCheckoutLineItems(lines, 0)
		if len(got) != 1 {
			t.Errorf("want 1 item, no tax line, got %d: %v", len(got), got)
		}
	})

	t.Run("a discount (negative total) line falls back to nil, never itemized", func(t *testing.T) {
		lines := []lineItemRecord{
			{Description: "Growth Plan (monthly)", Quantity: 1, UnitPriceCents: 2900, TotalCents: 2900},
			{Description: "Discount: WELCOME10", Quantity: 1, UnitPriceCents: -290, TotalCents: -290},
		}
		if got := buildCheckoutLineItems(lines, 0); got != nil {
			t.Errorf("want nil (unsafe to itemize with a negative line), got %v", got)
		}
	})

	t.Run("a zero-total line also falls back to nil", func(t *testing.T) {
		lines := []lineItemRecord{{Description: "Free add-on", Quantity: 1, UnitPriceCents: 0, TotalCents: 0}}
		if got := buildCheckoutLineItems(lines, 0); got != nil {
			t.Errorf("want nil, got %v", got)
		}
	})
}
