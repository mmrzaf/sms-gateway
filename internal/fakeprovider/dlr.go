package fakeprovider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mmrzaf/sms-gatway/internal/httpx"
	"github.com/mmrzaf/sms-gatway/internal/metrics"
)

// SecretHeader carries the shared secret on delivery report callbacks.
const SecretHeader = "X-Provider-Secret"

// report is the delivery report body sent to the gateway.
type report struct {
	MessageID   string     `json:"message_id"`
	Provider    string     `json:"provider"`
	ProviderRef string     `json:"provider_ref"`
	Status      string     `json:"status"`
	ReportedAt  httpx.Time `json:"reported_at"`
}

// scheduleDLR sends a delivery report for an accepted message after the
// configured delay.
func (p *Provider) scheduleDLR(messageID string, rec *record, s Settings) {
	status := dlrDelivered
	if strings.HasPrefix(rec.to, alwaysUndeliveredPrefix) || !p.chance(s.DeliveryRatio) {
		status = dlrUndelivered
	}
	delay := p.jittered(s.DLRDelayMS, s.DLRJitterMS)

	metrics.ProviderDLRPending.Set(float64(p.stats.dlrPending.Add(1)))
	go func() {
		defer func() { metrics.ProviderDLRPending.Set(float64(p.stats.dlrPending.Add(-1))) }()
		if !sleep(p.ctx, delay) {
			return
		}
		rep := report{
			MessageID:   messageID,
			Provider:    p.name,
			ProviderRef: rec.providerRef,
			Status:      status,
			ReportedAt:  httpx.Time(time.Now()),
		}
		if p.deliver(rep) {
			p.stats.dlrSent.Add(1)
			metrics.ProviderDLRSent.With(status).Inc()
			p.recent.update(rec, func(r *record) {
				r.dlrStatus = status
				r.dlrSentAt = time.Now()
			})
		} else {
			p.stats.dlrFailed.Add(1)
		}
	}()
}

// deliver posts a report until the gateway acknowledges it, retrying on
// unavailability with exponential backoff. It gives up on 400 and 401, which
// retrying cannot fix, and after the attempt limit.
func (p *Provider) deliver(rep report) bool {
	body, err := json.Marshal(rep)
	if err != nil {
		return false
	}
	delay := p.retryInitial
	for attempt := 1; attempt <= p.retryLimit; attempt++ {
		status, err := p.post(body)
		switch {
		case err == nil && status == http.StatusOK:
			return true
		case err == nil && (status == http.StatusBadRequest || status == http.StatusUnauthorized):
			p.logger.Warn("delivery report refused", "message_id", rep.MessageID, "status", status)
			return false
		}
		p.logger.Debug("delivery report not acknowledged", "message_id", rep.MessageID,
			"attempt", attempt, "status", status, "error", err)
		if attempt == p.retryLimit || !sleep(p.ctx, delay) {
			break
		}
		delay = min(2*delay, p.retryMax)
	}
	p.logger.Warn("delivery report dropped", "message_id", rep.MessageID)
	return false
}

func (p *Provider) post(body []byte) (int, error) {
	req, err := http.NewRequestWithContext(p.ctx, http.MethodPost, p.dlrURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SecretHeader, p.secret)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
