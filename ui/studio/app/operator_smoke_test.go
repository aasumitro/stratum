package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zalando/go-keyring"

	"github.com/aasumitro/stratum/studio/internal/connect"
	"github.com/aasumitro/stratum/studio/internal/store"
)

// The smoke test drives every mutating service method against a real database and
// broker, once as the schema owner and once as an operator login. It writes
// fixtures and audit rows, so it runs only when explicitly pointed at a database:
//
//	STUDIO_SMOKE_OWNER_URL     schema owner, used for fixtures and the refusal case
//	STUDIO_SMOKE_OPERATOR_URL  a login in the stratum_operator group, same database
//	STUDIO_SMOKE_MQ_URL        broker with its management API on the usual port
const (
	smokeActor     = "operator:stratum_operator_dev"
	smokeOrgSlug   = "studio-smoke"
	smokeQueue     = "studio.smoke.dlq"
	smokeOwnerSub  = "studio_smoke_owner"
	smokeMemberSub = "studio_smoke_member"
)

type smokeEnv struct {
	admin                 *pgxpool.Pool
	services              *Services
	ownerProject          string
	operatorProject       string
	orgID, paidInvoice    string
	voidInvoice, ownerDSN string
	payment               string
	started               time.Time
}

func newSmokeEnv(t *testing.T) *smokeEnv {
	t.Helper()
	ownerDSN := os.Getenv("STUDIO_SMOKE_OWNER_URL")
	operatorDSN := os.Getenv("STUDIO_SMOKE_OPERATOR_URL")
	mqDSN := os.Getenv("STUDIO_SMOKE_MQ_URL")
	if ownerDSN == "" || operatorDSN == "" || mqDSN == "" {
		t.Skip("STUDIO_SMOKE_OWNER_URL, STUDIO_SMOKE_OPERATOR_URL and STUDIO_SMOKE_MQ_URL required")
	}
	keyring.MockInit()

	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	pool := connect.NewPostgresPool()
	t.Cleanup(pool.CloseAll)
	services := Wire(db, pool)

	admin, err := pgxpool.New(t.Context(), ownerDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(admin.Close)

	add := func(name, dsn string) string {
		t.Helper()
		project, err := services.Project.Add(ProjectInput{Name: name, APIURL: "http://localhost:1", DBDSN: dsn, MQDSN: mqDSN, Color: "#000000"})
		if err != nil {
			t.Fatalf("add project %s: %v", name, err)
		}
		return project.ID
	}

	env := &smokeEnv{
		admin:           admin,
		services:        services,
		ownerProject:    add("owner", ownerDSN),
		operatorProject: add("operator", operatorDSN),
		ownerDSN:        ownerDSN,
		started:         time.Now().Add(-time.Second),
	}
	env.cleanup(t)
	t.Cleanup(func() { env.cleanup(t) })
	env.seed(t)
	env.seedQueue(t)
	return env
}

// cleanup also runs first, so a previous run that died halfway cannot fail this one.
func (e *smokeEnv) cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TRIGGER IF EXISTS smoke_refuse ON audit.events`,
		`DROP FUNCTION IF EXISTS audit.smoke_refuse()`,
		`DELETE FROM messaging.outbox WHERE payload->>'org_id' IN (SELECT id::text FROM organization.organizations WHERE slug = '` + smokeOrgSlug + `')`,
		`DELETE FROM billing.payment_links WHERE external_id = 'cs_studio_smoke'`,
		`DELETE FROM billing.payments WHERE external_id = 'pi_studio_smoke'`,
		`DELETE FROM billing.invoice_line_items WHERE description = 'Discount: STUDIO_SMOKE_VOID'`,
		`DELETE FROM billing.coupon_redemptions WHERE coupon_id = 'STUDIO_SMOKE_VOID'`,
		`DELETE FROM billing.coupons WHERE code = 'STUDIO_SMOKE_VOID'`,
		`DELETE FROM billing.invoices WHERE subscription_id IN (SELECT id FROM billing.subscriptions WHERE subject_id IN (SELECT id::text FROM organization.organizations WHERE slug = '` + smokeOrgSlug + `'))`,
		`DELETE FROM billing.subscriptions WHERE subject_id IN (SELECT id::text FROM organization.organizations WHERE slug = '` + smokeOrgSlug + `')`,
		`DROP TRIGGER IF EXISTS smoke_refuse ON notification.messages`,
		`DROP FUNCTION IF EXISTS notification.smoke_refuse()`,
		`DELETE FROM notification.messages WHERE auth_sub IN ('` + smokeOwnerSub + `', '` + smokeMemberSub + `')`,
		`DELETE FROM organization.organizations WHERE slug = '` + smokeOrgSlug + `'`,
		`DELETE FROM billing.coupon_targets WHERE coupon_id = 'STUDIO_SMOKE'`,
		`DELETE FROM billing.coupons WHERE code = 'STUDIO_SMOKE'`,
		`DELETE FROM billing.addon_features WHERE addon_id = 'studio_smoke_addon'`,
		`DELETE FROM billing.addons WHERE id = 'studio_smoke_addon'`,
		`DELETE FROM billing.plan_features WHERE plan_id = 'studio_smoke_plan'`,
		`DELETE FROM billing.plans WHERE id = 'studio_smoke_plan'`,
		`DELETE FROM billing.features WHERE id = 'studio_smoke_feature'`,
		`DELETE FROM ref.countries WHERE code = 'ZS'`,
		`DELETE FROM ref.currencies WHERE code = 'ZZS'`,
	} {
		if _, err := e.admin.Exec(ctx, stmt); err != nil {
			t.Logf("smoke cleanup: %v", err)
		}
	}
	if _, err := e.admin.Exec(ctx, `DELETE FROM audit.events WHERE actor = $1 AND created_at >= $2`, smokeActor, e.started); err != nil {
		t.Logf("smoke cleanup: %v", err)
	}
	if cfg, err := e.services.DLQ.getConfig(e.ownerProject); err == nil {
		_, _ = e.services.DLQ.request(ctx, cfg, http.MethodDelete, fmt.Sprintf("/queues/%s/%s", cfg.vhost, smokeQueue), nil)
	}
}

func (e *smokeEnv) seed(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	must(e.admin.QueryRow(ctx, `
		INSERT INTO organization.organizations (slug, name, owner_id) VALUES ($1, 'Studio Smoke', $2) RETURNING id`,
		smokeOrgSlug, smokeOwnerSub).Scan(&e.orgID), "organization")
	_, err := e.admin.Exec(ctx, `
		INSERT INTO organization.memberships (organization_id, auth_sub, role) VALUES ($1, $2, 'owner'), ($1, $3, 'member')`,
		e.orgID, smokeOwnerSub, smokeMemberSub)
	must(err, "memberships")

	var subscription string
	must(e.admin.QueryRow(ctx, `
		INSERT INTO billing.subscriptions (subject_type, subject_id, status, trial_end, period_end)
		VALUES ('organization', $1, 'trialing', now() + interval '1 day', now() + interval '1 day') RETURNING id`,
		e.orgID).Scan(&subscription), "subscription")
	for _, target := range []*string{&e.paidInvoice, &e.voidInvoice} {
		must(e.admin.QueryRow(ctx, `
			INSERT INTO billing.invoices (subscription_id, amount_cents) VALUES ($1, 100) RETURNING id`,
			subscription).Scan(target), "invoice")
	}
	_, err = e.admin.Exec(ctx, `
		INSERT INTO billing.payment_links (invoice_id, provider, currency, amount_cents, external_id, url)
		VALUES ($1, 'stripe', 'USD', 100, 'cs_studio_smoke', 'https://checkout.example/smoke-secret')`, e.voidInvoice)
	must(err, "payment link")

	// The invoice to void carries a coupon's discount, so voiding it gives the
	// application back, and a payment that settled nothing waits for review.
	_, err = e.admin.Exec(ctx, `
		INSERT INTO billing.coupons (code, name, discount_type, percent_off) VALUES ('STUDIO_SMOKE_VOID', 'Smoke void', 'percent', 10)`)
	must(err, "coupon")
	_, err = e.admin.Exec(ctx, `
		INSERT INTO billing.coupon_redemptions (coupon_id, subscription_id, applied_count) VALUES ('STUDIO_SMOKE_VOID', $1, 1)`, subscription)
	must(err, "coupon redemption")
	_, err = e.admin.Exec(ctx, `
		INSERT INTO billing.invoice_line_items (invoice_id, description, unit_price_cents, total_cents, currency)
		VALUES ($1, 'Discount: STUDIO_SMOKE_VOID', -10, -10, 'USD')`, e.voidInvoice)
	must(err, "discount line")
	must(e.admin.QueryRow(ctx, `
		INSERT INTO billing.payments (invoice_id, amount_cents, currency, provider, external_id)
		VALUES ($1, 100, 'USD', 'stripe', 'pi_studio_smoke') RETURNING id`, e.voidInvoice).Scan(&e.payment), "payment")
}

func (e *smokeEnv) broker(t *testing.T, method, path string, body any) {
	t.Helper()
	cfg, err := e.services.DLQ.getConfig(e.ownerProject)
	if err != nil {
		t.Fatalf("broker config: %v", err)
	}
	if _, err := e.services.DLQ.request(t.Context(), cfg, method, fmt.Sprintf(path, cfg.vhost), body); err != nil {
		t.Fatalf("broker %s %s: %v", method, path, err)
	}
}

func (e *smokeEnv) seedQueue(t *testing.T) {
	t.Helper()
	e.broker(t, http.MethodPut, "/queues/%s/"+smokeQueue, map[string]any{"durable": true})
	e.broker(t, http.MethodPost, "/bindings/%s/e/amq.topic/q/"+smokeQueue, map[string]any{"routing_key": smokeQueue})
	e.publish(t)
}

func (e *smokeEnv) publish(t *testing.T) {
	t.Helper()
	e.broker(t, http.MethodPost, "/exchanges/%s/amq.topic/publish", map[string]any{
		"routing_key": smokeQueue, "payload": "{}", "payload_encoding": "string", "properties": map[string]any{},
	})
}

func (e *smokeEnv) queuedMessages(t *testing.T) int {
	t.Helper()
	messages, err := e.services.DLQ.GetMessages(e.ownerProject, smokeQueue, 20)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	return len(messages)
}

// smokeMutation is one call of a mutating method, in an order that works as a
// real session: a thing is created before it is changed or deleted.
type smokeMutation struct {
	method string
	action string // the audit action this call must add
	run    func() error
}

func (e *smokeEnv) mutations(projectID string) []smokeMutation {
	catalog, reference, support := e.services.Catalog, e.services.Reference, e.services.Support
	limit := int64(5)
	percent := 10
	const (
		plan    = "studio_smoke_plan"
		feature = "studio_smoke_feature"
		addon   = "studio_smoke_addon"
		coupon  = "STUDIO_SMOKE"
	)
	prices := `{"USD":{"monthly":100,"yearly":1000}}`
	couponInput := func(name string) CouponInput {
		return CouponInput{Code: coupon, Name: name, DiscountType: "percent", PercentOff: &percent, Cadence: "once", Active: true}
	}
	return []smokeMutation{
		{"CreateCurrency", "studio.ref.currencies.insert", func() error {
			return reference.CreateCurrency(projectID, CurrencyInput{Code: "ZZS", Name: "Smoke", Symbol: "Z", DecimalPlaces: 2, Active: true})
		}},
		{"UpdateCurrency", "studio.ref.currencies.update", func() error {
			return reference.UpdateCurrency(projectID, "ZZS", CurrencyInput{Name: "Smoke 2", Symbol: "Z", DecimalPlaces: 2, Active: true})
		}},
		{"CreateCountry", "studio.ref.countries.insert", func() error {
			return reference.CreateCountry(projectID, CountryInput{Code: "ZS", Name: "Smoke", PhoneCode: "+0", CurrencyCode: "ZZS", Active: true})
		}},
		{"UpdateCountry", "studio.ref.countries.update", func() error {
			return reference.UpdateCountry(projectID, "ZS", CountryInput{Name: "Smoke 2", PhoneCode: "+0", CurrencyCode: "ZZS", Active: true})
		}},
		{"DeleteCountry", "studio.ref.countries.delete", func() error { return reference.DeleteCountry(projectID, "ZS") }},
		{"DeleteCurrency", "studio.ref.currencies.delete", func() error { return reference.DeleteCurrency(projectID, "ZZS") }},

		{"CreateFeature", "studio.billing.features.insert", func() error {
			return catalog.CreateFeature(projectID, FeatureInput{ID: feature, Name: "Smoke", Type: "boolean", Active: true})
		}},
		{"UpdateFeature", "studio.billing.features.update", func() error {
			return catalog.UpdateFeature(projectID, feature, FeatureInput{Name: "Smoke 2", Type: "boolean", Active: true})
		}},
		{"CreatePlan", "studio.billing.plans.insert", func() error {
			return catalog.CreatePlan(projectID, PlanInput{ID: plan, Name: "Smoke", Prices: prices, Active: true})
		}},
		{"UpdatePlan", "studio.billing.plans.update", func() error {
			return catalog.UpdatePlan(projectID, plan, PlanInput{Name: "Smoke 2", Prices: prices, Active: true})
		}},
		{"UpsertPlanFeature", "studio.billing.plan_features.insert", func() error {
			return catalog.UpsertPlanFeature(projectID, plan, PlanFeatureInput{FeatureID: feature, LimitValue: &limit})
		}},
		{"DeletePlanFeature", "studio.billing.plan_features.delete", func() error {
			return catalog.DeletePlanFeature(projectID, plan, feature)
		}},
		{"CreateAddon", "studio.billing.addons.insert", func() error {
			return catalog.CreateAddon(projectID, AddonInput{ID: addon, Name: "Smoke", Prices: prices, Active: true})
		}},
		{"UpdateAddon", "studio.billing.addons.update", func() error {
			return catalog.UpdateAddon(projectID, addon, AddonInput{Name: "Smoke 2", Prices: prices, Active: true})
		}},
		{"UpsertAddonFeature", "studio.billing.addon_features.insert", func() error {
			return catalog.UpsertAddonFeature(projectID, addon, AddonFeatureInput{FeatureID: feature, LimitValue: &limit})
		}},
		{"DeleteAddonFeature", "studio.billing.addon_features.delete", func() error {
			return catalog.DeleteAddonFeature(projectID, addon, feature)
		}},
		{"DeleteAddon", "studio.billing.addons.delete", func() error { return catalog.DeleteAddon(projectID, addon) }},
		{"CreateCoupon", "studio.billing.coupons.insert", func() error { return catalog.CreateCoupon(projectID, couponInput("Smoke")) }},
		{"UpdateCoupon", "studio.billing.coupons.update", func() error { return catalog.UpdateCoupon(projectID, coupon, couponInput("Smoke 2")) }},
		{"AddCouponTarget", "studio.billing.coupon_targets.insert", func() error {
			return catalog.AddCouponTarget(projectID, coupon, "organization", e.orgID)
		}},
		{"RemoveCouponTarget", "studio.billing.coupon_targets.delete", func() error {
			return catalog.RemoveCouponTarget(projectID, coupon, "organization", e.orgID)
		}},
		{"DeleteCoupon", "studio.billing.coupons.delete", func() error { return catalog.DeleteCoupon(projectID, coupon) }},
		{"DeletePlan", "studio.billing.plans.delete", func() error { return catalog.DeletePlan(projectID, plan) }},
		{"DeleteFeature", "studio.billing.features.delete", func() error { return catalog.DeleteFeature(projectID, feature) }},

		{"ExtendTrial", "studio.billing.subscriptions.update", func() error { return support.ExtendTrial(projectID, e.orgID, 3) }},
		{"ChangePlan", "studio.billing.subscriptions.update", func() error { return support.ChangePlan(projectID, e.orgID, "growth", "yearly") }},
		{"ActivateSubscription", "studio.billing.subscriptions.update", func() error { return support.ActivateSubscription(projectID, e.orgID) }},
		{"MarkInvoicePaid", "studio.messaging.outbox.insert", func() error { return support.MarkInvoicePaid(projectID, e.paidInvoice) }},
		{"VoidInvoice", "studio.billing.invoices.update", func() error { return support.VoidInvoice(projectID, e.voidInvoice) }},
		{"MarkPaymentRefunded", "studio.billing.payments.update", func() error { return support.MarkPaymentRefunded(projectID, e.payment) }},

		{"SuspendOrganization", "studio.organization.organizations.update", func() error {
			return e.services.OrganizationOps.SuspendOrganization(projectID, e.orgID, "smoke")
		}},
		{"UnsuspendOrganization", "studio.organization.organizations.update", func() error {
			return e.services.OrganizationOps.UnsuspendOrganization(projectID, e.orgID)
		}},

		{"Send", "studio.notification.broadcast", func() error {
			_, err := e.services.Broadcast.Send(projectID, BroadcastInput{Title: "Smoke", Body: "Smoke", Target: "organization:" + e.orgID})
			return err
		}},

		{"RequeueMessage", "studio.dlq.requeue", func() error {
			result, err := e.services.DLQ.RequeueMessage(projectID, smokeQueue)
			if err != nil {
				return err
			}
			if !result.Requeued {
				return errors.New(result.Error)
			}
			return nil
		}},
		{"PurgeQueue", "studio.dlq.purge", func() error {
			_, err := e.services.DLQ.PurgeQueue(projectID, smokeQueue)
			return err
		}},
	}
}

func (e *smokeEnv) auditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := e.admin.QueryRow(t.Context(), `
		SELECT count(*) FROM audit.events WHERE actor = $1 AND action = $2 AND created_at >= $3`,
		smokeActor, action, e.started).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func TestOperatorSmoke(t *testing.T) {
	e := newSmokeEnv(t)

	// A method added without a smoke case fails here rather than going untested.
	t.Run("the smoke list covers every mutating method", func(t *testing.T) {
		var listed []string
		for _, m := range e.mutations(e.operatorProject) {
			listed = append(listed, m.method)
		}
		found := mutatingMethods(t)
		slices.Sort(listed)
		slices.Sort(found)
		if !slices.Equal(listed, found) {
			t.Errorf("smoke list %v\nmutating methods %v", listed, found)
		}
	})

	t.Run("IsOperator tells the two connections apart", func(t *testing.T) {
		if isOperator, err := e.services.Audit.IsOperator(e.ownerProject); err != nil || isOperator {
			t.Errorf("schema owner: isOperator=%v err=%v, want false and no error", isOperator, err)
		}
		if isOperator, err := e.services.Audit.IsOperator(e.operatorProject); err != nil || !isOperator {
			t.Errorf("operator login: isOperator=%v err=%v, want true and no error", isOperator, err)
		}
	})

	t.Run("a database without the audit functions counts as not an operator", func(t *testing.T) {
		parsed, err := url.Parse(e.ownerDSN)
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		parsed.Path = "/postgres"
		project, err := e.services.Project.Add(ProjectInput{Name: "no audit", APIURL: "http://localhost:1", DBDSN: parsed.String(), MQDSN: "amqp://localhost/", Color: "#000000"})
		if err != nil {
			t.Fatalf("add project: %v", err)
		}
		if isOperator, err := e.services.Audit.IsOperator(project.ID); err != nil || isOperator {
			t.Errorf("isOperator=%v err=%v, want a definite no", isOperator, err)
		}
		err = e.services.Reference.DeleteCountry(project.ID, "ZS")
		if !errors.Is(err, ErrNotOperator) {
			t.Errorf("want ErrNotOperator, got %v", err)
		}
	})

	// An outage or a bad password says nothing about the role. The change is still
	// refused, but the operator must see the real cause, not "wrong kind of login".
	t.Run("a failed connection is reported as itself", func(t *testing.T) {
		parsed, err := url.Parse(os.Getenv("STUDIO_SMOKE_OPERATOR_URL"))
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		parsed.User = url.UserPassword(parsed.User.Username(), "wrong-password")
		for name, dsn := range map[string]string{
			"wrong password": parsed.String(),
			"unreachable":    "postgres://nobody@127.0.0.1:1/none?connect_timeout=1",
		} {
			project, err := e.services.Project.Add(ProjectInput{Name: name, APIURL: "http://localhost:1", DBDSN: dsn, MQDSN: "amqp://localhost/", Color: "#000000"})
			if err != nil {
				t.Fatalf("add project: %v", err)
			}
			if _, err := e.services.Audit.IsOperator(project.ID); err == nil {
				t.Errorf("%s: IsOperator should return the error", name)
			}
			err = e.services.Reference.DeleteCountry(project.ID, "ZS")
			if err == nil || errors.Is(err, ErrNotOperator) {
				t.Errorf("%s: want the connection error, got %v", name, err)
			}
		}
	})

	// The quarterly review works by letting a login expire. An expired login reads as
	// a failed connection, and renewing it restores access with no change in Studio.
	// Without BYPASSRLS the login sees no rows on the RLS tables, so a broadcast would
	// reach nobody and report success.
	t.Run("an operator login without BYPASSRLS is refused", func(t *testing.T) {
		const role = "studio_smoke_no_bypass"
		ctx := t.Context()
		if _, err := e.admin.Exec(ctx, `DROP ROLE IF EXISTS `+role); err != nil {
			t.Fatalf("drop leftover role: %v", err)
		}
		if _, err := e.admin.Exec(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD 'smoke' IN ROLE stratum_operator`); err != nil {
			t.Fatalf("create role: %v", err)
		}
		parsed, err := url.Parse(os.Getenv("STUDIO_SMOKE_OPERATOR_URL"))
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		parsed.User = url.UserPassword(role, "smoke")
		project, err := e.services.Project.Add(ProjectInput{Name: "no bypass", APIURL: "http://localhost:1", DBDSN: parsed.String(), MQDSN: "amqp://localhost/", Color: "#000000"})
		if err != nil {
			t.Fatalf("add project: %v", err)
		}
		// The project's pool must be gone before its role can be dropped.
		t.Cleanup(func() {
			if err := e.services.Project.Delete(project.ID); err != nil {
				t.Logf("delete project: %v", err)
			}
			if _, err := e.admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+role); err != nil {
				t.Logf("drop role: %v", err)
			}
		})
		if isOperator, err := e.services.Audit.IsOperator(project.ID); err != nil || isOperator {
			t.Errorf("isOperator=%v err=%v, want a definite no", isOperator, err)
		}
		if err := e.services.Reference.DeleteCountry(project.ID, "ZS"); !errors.Is(err, ErrNotOperator) {
			t.Errorf("want ErrNotOperator, got %v", err)
		}
	})

	t.Run("an expired operator login is refused until it is renewed", func(t *testing.T) {
		const role = "studio_smoke_expired"
		ctx := t.Context()
		if _, err := e.admin.Exec(ctx, `DROP ROLE IF EXISTS `+role); err != nil {
			t.Fatalf("drop leftover role: %v", err)
		}
		if _, err := e.admin.Exec(ctx, `
			CREATE ROLE `+role+` LOGIN BYPASSRLS PASSWORD 'smoke' VALID UNTIL '2000-01-01' IN ROLE stratum_operator`); err != nil {
			t.Fatalf("create role: %v", err)
		}
		parsed, err := url.Parse(os.Getenv("STUDIO_SMOKE_OPERATOR_URL"))
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		parsed.User = url.UserPassword(role, "smoke")
		project, err := e.services.Project.Add(ProjectInput{Name: "expired", APIURL: "http://localhost:1", DBDSN: parsed.String(), MQDSN: "amqp://localhost/", Color: "#000000"})
		if err != nil {
			t.Fatalf("add project: %v", err)
		}
		// The project's pool must be gone before its role can be dropped.
		t.Cleanup(func() {
			if err := e.services.Project.Delete(project.ID); err != nil {
				t.Logf("delete project: %v", err)
			}
			if _, err := e.admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+role); err != nil {
				t.Logf("drop role: %v", err)
			}
		})

		if isOperator, err := e.services.Audit.IsOperator(project.ID); err == nil {
			t.Errorf("expired login: IsOperator = %v with no error, want the login failure", isOperator)
		}
		if err := e.services.Reference.DeleteCountry(project.ID, "ZS"); err == nil || errors.Is(err, ErrNotOperator) {
			t.Errorf("expired login: want the login failure, got %v", err)
		}

		if _, err := e.admin.Exec(ctx, `ALTER ROLE `+role+` VALID UNTIL 'infinity'`); err != nil {
			t.Fatalf("renew role: %v", err)
		}
		if isOperator, err := e.services.Audit.IsOperator(project.ID); err != nil || !isOperator {
			t.Errorf("renewed login: isOperator=%v err=%v, want true", isOperator, err)
		}
	})

	t.Run("an owner connection is refused everywhere and changes nothing", func(t *testing.T) {
		for _, m := range e.mutations(e.ownerProject) {
			if err := m.run(); !errors.Is(err, ErrNotOperator) {
				t.Errorf("%s: want ErrNotOperator, got %v", m.method, err)
			}
		}
		var rows int
		if err := e.admin.QueryRow(t.Context(),
			`SELECT count(*) FROM audit.events WHERE actor LIKE 'operator:%' AND created_at >= $1`, e.started).Scan(&rows); err != nil {
			t.Fatalf("count audit rows: %v", err)
		}
		if rows != 0 {
			t.Errorf("%d audit rows written by a refused connection", rows)
		}
		var status string
		if err := e.admin.QueryRow(t.Context(),
			`SELECT status FROM billing.subscriptions WHERE subject_id = $1`, e.orgID).Scan(&status); err != nil {
			t.Fatalf("read subscription: %v", err)
		}
		if status != "trialing" {
			t.Errorf("subscription status = %q, want it untouched", status)
		}
		if got := e.queuedMessages(t); got != 1 {
			t.Errorf("queue holds %d messages, want the seeded one untouched", got)
		}
	})

	t.Run("a failed audit record stops the action", func(t *testing.T) {
		ctx := t.Context()
		if _, err := e.admin.Exec(ctx, `
			CREATE FUNCTION audit.smoke_refuse() RETURNS trigger LANGUAGE plpgsql AS $f$
			BEGIN
				IF NEW.action IN ('studio.dlq.purge', 'studio.dlq.requeue', 'studio.notification.broadcast') THEN
					RAISE EXCEPTION 'injected audit failure';
				END IF;
				RETURN NEW;
			END;
			$f$`); err != nil {
			t.Fatalf("create function: %v", err)
		}
		if _, err := e.admin.Exec(ctx, `
			CREATE TRIGGER smoke_refuse BEFORE INSERT ON audit.events
			FOR EACH ROW EXECUTE FUNCTION audit.smoke_refuse()`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		dropTrigger := func() {
			background := context.Background()
			_, _ = e.admin.Exec(background, `DROP TRIGGER IF EXISTS smoke_refuse ON audit.events`)
			_, _ = e.admin.Exec(background, `DROP FUNCTION IF EXISTS audit.smoke_refuse()`)
		}
		defer dropTrigger()

		if _, err := e.services.DLQ.PurgeQueue(e.operatorProject, smokeQueue); err == nil {
			t.Error("purge must fail when its audit record fails")
		}
		if _, err := e.services.DLQ.RequeueMessage(e.operatorProject, smokeQueue); err == nil {
			t.Error("requeue must fail when its audit record fails")
		}
		if got := e.queuedMessages(t); got != 1 {
			t.Errorf("queue holds %d messages, want the seeded one untouched", got)
		}
		if _, err := e.services.Broadcast.Send(e.operatorProject, BroadcastInput{Title: "Refused", Body: "Refused", Target: "organization:" + e.orgID}); err == nil {
			t.Error("broadcast must fail when its audit record fails")
		}
		var messages int
		if err := e.admin.QueryRow(ctx,
			`SELECT count(*) FROM notification.messages WHERE subject = 'Refused' AND auth_sub IN ($1, $2)`,
			smokeOwnerSub, smokeMemberSub).Scan(&messages); err != nil {
			t.Fatalf("count messages: %v", err)
		}
		if messages != 0 {
			t.Errorf("%d messages sent although the audit record failed", messages)
		}
	})

	t.Run("an operator can run every mutation and each one is attributed", func(t *testing.T) {
		for _, m := range e.mutations(e.operatorProject) {
			before := e.auditCount(t, m.action)
			if err := m.run(); err != nil {
				t.Errorf("%s: %v", m.method, err)
				continue
			}
			if after := e.auditCount(t, m.action); after <= before {
				t.Errorf("%s: no %s row added (%d before, %d after)", m.method, m.action, before, after)
			}
		}
	})

	t.Run("Void gives the coupon application back and the refund is recorded", func(t *testing.T) {
		var applied int
		var paymentStatus string
		if err := e.admin.QueryRow(t.Context(),
			`SELECT applied_count FROM billing.coupon_redemptions WHERE coupon_id = 'STUDIO_SMOKE_VOID'`).Scan(&applied); err != nil {
			t.Fatalf("read redemption: %v", err)
		}
		if err := e.admin.QueryRow(t.Context(),
			`SELECT status FROM billing.payments WHERE id = $1`, e.payment).Scan(&paymentStatus); err != nil {
			t.Fatalf("read payment: %v", err)
		}
		if applied != 0 || paymentStatus != "refunded" {
			t.Errorf("want applied_count 0 and payment refunded, got %d and %q", applied, paymentStatus)
		}
		if n := e.auditCount(t, "studio.billing.coupon_redemptions.update"); n != 1 {
			t.Errorf("want one coupon_redemptions audit row, got %d", n)
		}
	})

	t.Run("a broadcast writes one audit row and one message per recipient", func(t *testing.T) {
		var metadata []byte
		var rows int
		if err := e.admin.QueryRow(t.Context(), `
			SELECT count(*), max(metadata::text) FROM audit.events
			WHERE actor = $1 AND action = 'studio.notification.broadcast' AND created_at >= $2`,
			smokeActor, e.started).Scan(&rows, &metadata); err != nil {
			t.Fatalf("read audit row: %v", err)
		}
		if rows != 1 {
			t.Fatalf("want one audit row for the broadcast, got %d", rows)
		}
		var recorded struct {
			Subject        string `json:"subject"`
			RecipientCount int    `json:"recipient_count"`
			OrganizationID string `json:"organization_id"`
		}
		if err := json.Unmarshal(metadata, &recorded); err != nil {
			t.Fatalf("decode metadata: %v", err)
		}
		if recorded.Subject != "Smoke" || recorded.RecipientCount != 2 || recorded.OrganizationID != e.orgID {
			t.Errorf("metadata = %+v", recorded)
		}
		var messages, messageRows int
		if err := e.admin.QueryRow(t.Context(),
			`SELECT count(*) FROM notification.messages WHERE subject = 'Smoke' AND auth_sub IN ($1, $2)`,
			smokeOwnerSub, smokeMemberSub).Scan(&messages); err != nil {
			t.Fatalf("count messages: %v", err)
		}
		if messages != 2 {
			t.Errorf("want 2 messages, got %d", messages)
		}
		if err := e.admin.QueryRow(t.Context(), `
			SELECT count(*) FROM audit.events WHERE action LIKE 'studio.notification.messages%' AND created_at >= $1`,
			e.started).Scan(&messageRows); err != nil {
			t.Fatalf("count per-message rows: %v", err)
		}
		if messageRows != 0 {
			t.Errorf("want no audit row per message, got %d", messageRows)
		}
	})

	t.Run("composite-key tables name their parent in the audit resource", func(t *testing.T) {
		var rows int
		if err := e.admin.QueryRow(t.Context(), `
			SELECT count(*) FROM audit.events
			WHERE actor = $1 AND action = 'studio.billing.plan_features.insert'
			  AND resource = 'billing.plan_features/studio_smoke_plan' AND created_at >= $2`,
			smokeActor, e.started).Scan(&rows); err != nil {
			t.Fatalf("read audit rows: %v", err)
		}
		if rows != 1 {
			t.Errorf("want one row naming the plan, got %d", rows)
		}
	})

	t.Run("a broadcast that reaches only some recipients says so", func(t *testing.T) {
		ctx := t.Context()
		if _, err := e.admin.Exec(ctx, `
			CREATE FUNCTION notification.smoke_refuse() RETURNS trigger LANGUAGE plpgsql AS $f$
			BEGIN
				IF NEW.subject = 'Partial' AND NEW.auth_sub = '`+smokeMemberSub+`' THEN
					RAISE EXCEPTION 'injected delivery failure';
				END IF;
				RETURN NEW;
			END;
			$f$`); err != nil {
			t.Fatalf("create function: %v", err)
		}
		if _, err := e.admin.Exec(ctx, `
			CREATE TRIGGER smoke_refuse BEFORE INSERT ON notification.messages
			FOR EACH ROW EXECUTE FUNCTION notification.smoke_refuse()`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		defer func() {
			background := context.Background()
			_, _ = e.admin.Exec(background, `DROP TRIGGER IF EXISTS smoke_refuse ON notification.messages`)
			_, _ = e.admin.Exec(background, `DROP FUNCTION IF EXISTS notification.smoke_refuse()`)
		}()

		sent, err := e.services.Broadcast.Send(e.operatorProject, BroadcastInput{Title: "Partial", Body: "Partial", Target: "organization:" + e.orgID})
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if sent != 1 {
			t.Errorf("sent = %d, want 1", sent)
		}
		var recordedSent, recipients int
		if err := e.admin.QueryRow(ctx, `
			SELECT (metadata->>'sent')::int, (metadata->>'recipient_count')::int FROM audit.events
			WHERE actor = $1 AND action = 'studio.notification.broadcast.failed' AND created_at >= $2`,
			smokeActor, e.started).Scan(&recordedSent, &recipients); err != nil {
			t.Fatalf("read shortfall row: %v", err)
		}
		if recordedSent != 1 || recipients != 2 {
			t.Errorf("shortfall row says sent=%d of %d, want 1 of 2", recordedSent, recipients)
		}
	})

	t.Run("a checkout URL stays out of the audit trail", func(t *testing.T) {
		var rows int
		var leaked bool
		if err := e.admin.QueryRow(t.Context(), `
			SELECT count(*), COALESCE(bool_or(metadata::text LIKE '%smoke-secret%'), false)
			FROM audit.events
			WHERE actor = $1 AND action = 'studio.billing.payment_links.update' AND created_at >= $2`,
			smokeActor, e.started).Scan(&rows, &leaked); err != nil {
			t.Fatalf("read audit rows: %v", err)
		}
		if rows == 0 {
			t.Fatal("voiding the invoice should have captured its payment link")
		}
		if leaked {
			t.Error("a payment link's url reached audit metadata")
		}
	})

	t.Run("a failed broker action is recorded as failed", func(t *testing.T) {
		// The purge above emptied the queue.
		if _, err := e.services.DLQ.RequeueMessage(e.operatorProject, smokeQueue); err == nil {
			t.Fatal("requeue on an empty queue should fail")
		}
		var failure string
		if err := e.admin.QueryRow(t.Context(), `
			SELECT metadata->>'error' FROM audit.events
			WHERE actor = $1 AND action = 'studio.dlq.requeue.failed' AND resource = $2 AND created_at >= $3`,
			smokeActor, smokeQueue, e.started).Scan(&failure); err != nil {
			t.Fatalf("read failure row: %v", err)
		}
		if failure == "" {
			t.Error("the failure row should say why")
		}
	})

	t.Run("application_name identifies Studio on the operator's rows", func(t *testing.T) {
		var agents int
		if err := e.admin.QueryRow(t.Context(), `
			SELECT count(*) FROM audit.events
			WHERE actor = $1 AND created_at >= $2 AND user_agent <> 'stratum-studio'`,
			smokeActor, e.started).Scan(&agents); err != nil {
			t.Fatalf("count: %v", err)
		}
		if agents != 0 {
			t.Errorf("%d operator rows without Studio's application_name", agents)
		}
	})

	// Without this a project switched to an operator login stays on its old role
	// until the app restarts, and the operator keeps being refused.
	t.Run("changing a project's DSN takes effect without a restart", func(t *testing.T) {
		if isOperator, err := e.services.Audit.IsOperator(e.ownerProject); err != nil || isOperator {
			t.Fatalf("precondition: the owner project starts as not an operator (got %v, %v)", isOperator, err)
		}
		operatorDSN := os.Getenv("STUDIO_SMOKE_OPERATOR_URL")
		if _, err := e.services.Project.Update(e.ownerProject, ProjectInput{
			Name: "owner", APIURL: "http://localhost:1", DBDSN: operatorDSN, MQDSN: os.Getenv("STUDIO_SMOKE_MQ_URL"), Color: "#000000",
		}); err != nil {
			t.Fatalf("update project: %v", err)
		}
		if isOperator, err := e.services.Audit.IsOperator(e.ownerProject); err != nil || !isOperator {
			t.Errorf("the project still uses its old connection after the DSN changed (got %v, %v)", isOperator, err)
		}
	})
}
