package telemetry

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type Metrics struct {
	Registry    *prometheus.Registry
	Requests    *prometheus.CounterVec
	Duration    *prometheus.HistogramVec
	InFlight    prometheus.Gauge
	llmRequests *prometheus.CounterVec
	llmDuration *prometheus.HistogramVec
	llmErrors   *prometheus.CounterVec
}

func New() *Metrics {
	m := &Metrics{
		Registry:    prometheus.NewRegistry(),
		Requests:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "tesseract_http_requests_total", Help: "Completed HTTP requests."}, []string{"route", "status_group"}),
		Duration:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "tesseract_http_request_duration_seconds", Help: "HTTP request duration."}, []string{"route"}),
		InFlight:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "tesseract_http_requests_in_flight", Help: "Active HTTP requests."}),
		llmRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "tesseract_llm_requests_total", Help: "Completed LLM requests."}, []string{"provider", "model"}),
		llmDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "tesseract_llm_request_duration_seconds", Help: "LLM request duration."}, []string{"provider", "model"}),
		llmErrors:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "tesseract_llm_errors_total", Help: "Failed LLM requests."}, []string{"provider", "model"}),
	}
	m.Registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), m.Requests, m.Duration, m.InFlight, m.llmRequests, m.llmDuration, m.llmErrors)
	// No provider is configured yet. Keep a visible, fixed zero-valued series.
	m.llmRequests.WithLabelValues("unconfigured", "unconfigured")
	m.llmDuration.WithLabelValues("unconfigured", "unconfigured")
	m.llmErrors.WithLabelValues("unconfigured", "unconfigured")
	return m
}

// ObserveLLM uses fixed labels; endpoint, model configuration and credentials never become labels.
func (m *Metrics) ObserveLLM(duration time.Duration, failed bool) {
	m.llmRequests.WithLabelValues("openai_compatible", "configured").Inc()
	m.llmDuration.WithLabelValues("openai_compatible", "configured").Observe(duration.Seconds())
	if failed {
		m.llmErrors.WithLabelValues("openai_compatible", "configured").Inc()
	}
}
