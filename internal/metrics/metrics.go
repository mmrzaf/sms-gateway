package metrics

// Default is the registry every gateway component records into.
var Default = NewRegistry()

var (
	latencyBuckets = ExponentialBuckets(0.005, 2, 14) // 5 ms to about 41 s
	sizeBuckets    = ExponentialBuckets(1, 2, 11)     // 1 to 1024
)

// API role.
var (
	APIRequests        = Default.NewCounter("sms_api_requests_total", "HTTP requests by route pattern and status code.", "route", "code")
	APIRequestDuration = Default.NewHistogram("sms_api_request_duration_seconds", "HTTP request latency by route pattern.", latencyBuckets, "route")
	MessagesAccepted   = Default.NewCounter("sms_messages_accepted_total", "Messages accepted.", "type")
	RateLimited        = Default.NewCounter("sms_rate_limited_total", "Messages refused by the rate limiter.")
	CreditsDebited     = Default.NewCounter("sms_credits_debited_total", "Credits debited at acceptance.")
	CreditsCharged     = Default.NewCounter("sms_credits_charged_total", "Credits added by charges.")
	DLRReceived        = Default.NewCounter("sms_dlr_received_total", "Delivery reports by outcome.", "outcome")
	DLRBatchSize       = Default.NewHistogram("sms_dlr_batch_size", "Delivery reports per commit.", sizeBuckets)
)

// Worker role.
var (
	DispatchAttempts        = Default.NewCounter("sms_dispatch_attempts_total", "Provider requests by provider, class, and outcome.", "provider", "type", "outcome")
	MessagesCompleted       = Default.NewCounter("sms_messages_completed_total", "Messages that reached sent, failed, or expired through a worker or the sweeper.", "type", "status")
	Deferrals               = Default.NewCounter("sms_deferrals_total", "Messages deferred because no provider was usable.", "type")
	CreditsRefunded         = Default.NewCounter("sms_credits_refunded_total", "Credits refunded.")
	ProviderRequestDuration = Default.NewHistogram("sms_provider_request_duration_seconds", "Provider request latency.", latencyBuckets, "provider")
	CircuitState            = Default.NewGauge("sms_circuit_state", "Circuit state per provider: 0 closed, 1 half-open, 2 open.", "provider")
	PoolInFlight            = Default.NewGauge("sms_pool_in_flight", "Sends in progress per pool.", "pool")
	ClaimSize               = Default.NewHistogram("sms_claim_size", "Rows per claim.", sizeBuckets, "pool")
	CompleterBatchSize      = Default.NewHistogram("sms_completer_batch_size", "Outcomes per completer commit.", sizeBuckets)
	MessageLatency          = Default.NewHistogram("sms_message_latency_seconds", "Time from acceptance to sent (stage=sent) and to a terminal status (stage=completed).", latencyBuckets, "type", "stage")
	ExpressSLABreaches      = Default.NewCounter("sms_express_sla_breaches_total", "Express messages that ended dispatch with an SLA breach.")
	QueueDepth              = Default.NewGauge("sms_queue_depth", "Queue rows per lane and state, sampled by the sweeper.", "lane", "state")
	QueueOldestReady        = Default.NewGauge("sms_queue_oldest_ready_seconds", "Age of the oldest ready row per lane, sampled by the sweeper.", "lane")
)

// Fake provider.
var (
	ProviderRequests   = Default.NewCounter("provider_requests_total", "Send requests by result.", "result")
	ProviderDLRSent    = Default.NewCounter("provider_dlr_sent_total", "Delivery reports acknowledged by the gateway.", "status")
	ProviderDLRPending = Default.NewGauge("provider_dlr_pending", "Delivery reports waiting to be sent.")
)
