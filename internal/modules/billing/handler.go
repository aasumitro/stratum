package billing

type handler struct {
	svc          *service
	stripeSecret string // STRIPE_WEBHOOK_SECRET; empty = skip verification
	xenditToken  string // XENDIT_CALLBACK_TOKEN; empty = skip verification
}
