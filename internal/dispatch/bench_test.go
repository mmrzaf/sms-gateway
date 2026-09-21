package dispatch

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/message"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
)

// BenchmarkDispatch (B4) measures claim, send, and completion throughput of
// one worker against a provider that answers immediately. The queue is
// filled before the timer starts; the benchmark ends when it is empty.
// Run with a fixed count, for example -benchtime 20000x.
func BenchmarkDispatch(b *testing.B) {
	pool := testutil.DB(b)
	svc := newMessages(pool, 16)
	c, _ := testutil.Customer(b, pool, int64(b.N))

	batch := make([]message.Request, 0, message.MaxBatchSize)
	for i := 0; i < b.N; i++ {
		batch = append(batch, message.Request{To: "+989121234567", Text: "bench"})
		if len(batch) == message.MaxBatchSize || i == b.N-1 {
			if _, err := svc.AcceptBatch(context.Background(), c.ID, batch); err != nil {
				b.Fatal(err)
			}
			batch = batch[:0]
		}
	}

	provider := newScripted(b, func(int, sendBody) (int, time.Duration) { return http.StatusOK, 0 })
	cfg := testConfig(provider.srv.URL)
	cfg.NormalLanes = 16
	cfg.NormalConcurrency = 256
	cfg.ClaimBatchSize = 200
	cfg.CompleterBatchSize = 200
	cfg.CompleterFlushInterval = 50 * time.Millisecond
	cfg.ProviderRateLimit = 1_000_000

	b.ResetTimer()
	start := time.Now()
	startWorker(b, pool, cfg)
	for queueRows(b, pool) > 0 {
		time.Sleep(20 * time.Millisecond)
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/time.Since(start).Seconds(), "msgs/s")
}
