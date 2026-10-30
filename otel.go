package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// OTel instruments. Nil until initOTel succeeds, so recording is a no-op when
// telemetry is disabled. In VictoriaMetrics (through the collector's
// prometheusremotewrite exporter) dots become underscores and counters get a
// _total suffix: tally.logins -> tally_logins_total{method,result}.
var (
	reqDuration  metric.Float64Histogram
	activeReqs   metric.Int64UpDownCounter
	usersCreated metric.Int64Counter // {method: local|aio}
	talliesMade  metric.Int64Counter // {type: preset id}
	logins       metric.Int64Counter // {method: local|aio, result: ok|failed|<aio error>}
	playDays     metric.Int64Counter
	matchEvents  metric.Int64Counter // {action: scheduled|scored|added|edited|deleted|cancelled}
	awardsMade   metric.Int64Counter
	scoresMade   metric.Int64Counter
	playersAdded metric.Int64Counter
	adminChecks  metric.Int64Counter // {result: admin|not_admin|local_account|error}
)

// initOTel wires up OTLP metric export (push) to the collector. Endpoint,
// protocol, and TLS come from the standard OTEL_EXPORTER_OTLP_* env vars; when
// none is set it's a no-op so local runs don't need a collector.
func initOTel(ctx context.Context) (shutdown func(context.Context) error, enabled bool) {
	noop := func(context.Context) error { return nil }
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "" {
		return noop, false
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(env("OTEL_SERVICE_NAME", "tally"))),
	)
	if err != nil {
		log.Printf("otel resource: %v", err)
	}

	exp, err := otlpmetrichttp.New(ctx)
	if err != nil {
		log.Printf("otel exporter: %v (metrics disabled)", err)
		return noop, false
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(15*time.Second))),
	)
	otel.SetMeterProvider(mp)

	meter := mp.Meter("tally")
	if reqDuration, err = meter.Float64Histogram(
		"http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of inbound HTTP requests."),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10),
	); err != nil {
		log.Printf("otel histogram: %v", err)
	}
	if activeReqs, err = meter.Int64UpDownCounter(
		"http.server.active_requests",
		metric.WithDescription("In-flight HTTP requests."),
	); err != nil {
		log.Printf("otel counter: %v", err)
	}
	counters := []struct {
		dst  *metric.Int64Counter
		name string
		desc string
	}{
		{&usersCreated, "tally.users.created", "Accounts created, by login method."},
		{&talliesMade, "tally.tallies.created", "Tallies created, by type."},
		{&logins, "tally.logins", "Login attempts, by method and result."},
		{&playDays, "tally.play_days.started", "Play days started."},
		{&matchEvents, "tally.match.events", "Match changes, by action."},
		{&awardsMade, "tally.awards.recorded", "Extra-point entries recorded (one per player)."},
		{&scoresMade, "tally.scores.recorded", "Points entered per player without match details (one per play)."},
		{&playersAdded, "tally.players.added", "Players added to tallies."},
		{&adminChecks, "tally.admin.checks", "Site admin page requests, by the result of asking all-in-one."},
	}
	for _, c := range counters {
		if *c.dst, err = meter.Int64Counter(c.name, metric.WithDescription(c.desc)); err != nil {
			log.Printf("otel counter %s: %v", c.name, err)
		}
	}

	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		log.Printf("otel runtime: %v", err)
	}
	return mp.Shutdown, true
}

// otelMiddleware records the HTTP server duration histogram per matched route.
// It must wrap the mux directly so r.Pattern is populated.
func otelMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqDuration == nil || activeReqs == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		activeReqs.Add(ctx, 1)
		defer activeReqs.Add(ctx, -1)

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		reqDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
			semconv.HTTPRequestMethodKey.String(r.Method),
			semconv.HTTPRoute(route),
			semconv.HTTPResponseStatusCodeKey.Int(sw.status),
		))
	})
}

// add increments c by n with the given attributes; a no-op when telemetry is
// disabled (c is nil) or there is nothing to count.
func add(ctx context.Context, c metric.Int64Counter, n int, attrs ...attribute.KeyValue) {
	if c == nil || n <= 0 {
		return
	}
	c.Add(ctx, int64(n), metric.WithAttributes(attrs...))
}

func recordUserCreated(ctx context.Context, method string) {
	add(ctx, usersCreated, 1, attribute.String("method", method))
}

func recordTallyCreated(ctx context.Context, preset string) {
	add(ctx, talliesMade, 1, attribute.String("type", preset))
}

// recordLogin counts a login attempt. result is "ok", "failed" (wrong local
// password), or a short aio error reason; keep it to a small fixed set.
func recordLogin(ctx context.Context, method, result string) {
	add(ctx, logins, 1, attribute.String("method", method), attribute.String("result", result))
}

func recordPlayDayStarted(ctx context.Context) { add(ctx, playDays, 1) }

func recordMatchEvent(ctx context.Context, action string, n int) {
	add(ctx, matchEvents, n, attribute.String("action", action))
}

func recordAwards(ctx context.Context, n int) { add(ctx, awardsMade, n) }

func recordScores(ctx context.Context, n int) { add(ctx, scoresMade, n) }

func recordPlayersAdded(ctx context.Context, n int) { add(ctx, playersAdded, n) }

// registerBusinessMetrics adds gauges sampled from the database on each export
// (every 15s): totals for the dashboard, and the connection pool. Call it only
// when telemetry is enabled.
func registerBusinessMetrics(store *Store) {
	meter := otel.Meter("tally")
	gauge := func(name, desc string) metric.Int64ObservableGauge {
		g, err := meter.Int64ObservableGauge(name, metric.WithDescription(desc))
		if err != nil {
			log.Printf("otel gauge %s: %v", name, err)
		}
		return g
	}
	users := gauge("tally.users", "Accounts.")
	tallies := gauge("tally.tallies", "Tallies.")
	active := gauge("tally.tallies.active", "Tallies with a play day in the last 30 days.")
	players := gauge("tally.players", "Active players across all tallies.")
	matches := gauge("tally.matches", "Finished matches.")
	days := gauge("tally.play_days", "Play days.")
	conns := gauge("tally.db.connections", "Database pool connections, by state.")

	_, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		s := store.pool.Stat()
		o.ObserveInt64(conns, int64(s.AcquiredConns()), metric.WithAttributes(attribute.String("state", "acquired")))
		o.ObserveInt64(conns, int64(s.IdleConns()), metric.WithAttributes(attribute.String("state", "idle")))
		o.ObserveInt64(conns, int64(s.MaxConns()), metric.WithAttributes(attribute.String("state", "max")))

		t, err := store.Totals(ctx)
		if err != nil {
			return err
		}
		o.ObserveInt64(users, t.Users)
		o.ObserveInt64(tallies, t.Tallies)
		o.ObserveInt64(active, t.ActiveTallies)
		o.ObserveInt64(players, t.Players)
		o.ObserveInt64(matches, t.Matches)
		o.ObserveInt64(days, t.PlayDays)
		return nil
	}, users, tallies, active, players, matches, days, conns)
	if err != nil {
		log.Printf("otel business metrics: %v", err)
	}
}

func recordAdminCheck(ctx context.Context, result string) {
	add(ctx, adminChecks, 1, attribute.String("result", result))
}
