package reporter

import (
	"encoding/json"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/hpcloud/tail"
	"github.com/opentracing/opentracing-go"
	jaeger "github.com/uber/jaeger-client-go/config"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/opentracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/apis/audit"

	"github.com/JulienBalestra/audit-trace/pkg/event"
)

const (
	// DatadogAgent https://github.com/DataDog/datadog-trace-agent
	DatadogAgent = "datadog"
	// JaegerAgent https://github.com/jaegertracing/jaeger
	JaegerAgent = "jaeger"
)

// Config struct to instantiate a NewReporter
type Config struct {
	AuditLogABSPath            string
	LocalAgentHostPort         string
	ServiceName                string
	ServiceNameWatch           string
	GarbageCollectionThreshold int
	Agent                      string
	GarbageCollectPeriod       time.Duration
	BacklogLimit               time.Duration
}

// Reporter contains the configuration and the state
type Reporter struct {
	conf *Config

	tracer             opentracing.Tracer
	tracerWatch        opentracing.Tracer
	eventReceived      map[types.UID]*event.SpanEvent
	eventReceivedMutex sync.Mutex
	stopGC             chan struct{}
	processedEvent     int
	skippedEvent       int
	getClientTracer    func(serviceName string, agentAddress *string) (opentracing.Tracer, error)
}

func getJaegerClientTracer(serviceName string, agentAddress *string) (opentracing.Tracer, error) {
	jConfig := jaeger.Configuration{
		ServiceName: serviceName,
		Sampler: &jaeger.SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: &jaeger.ReporterConfig{
			BufferFlushInterval: 1 * time.Second,
			LocalAgentHostPort:  *agentAddress,
		},
	}
	t, _, err := jConfig.NewTracer()
	if err != nil {
		glog.Errorf("Unexpected error: %v", err)
		return nil, err
	}
	return t, nil
}

func getDatadogClientTracer(serviceName string, agentAddress *string) (opentracing.Tracer, error) {
	return opentracer.New(tracer.WithServiceName(serviceName), tracer.WithAgentAddr(*agentAddress)), nil
}

// NewReporter instantiate a New Reporter
func NewReporter(conf *Config) (*Reporter, error) {
	var err error
	var t, tw opentracing.Tracer
	var getTracer func(serviceName string, agentAddress *string) (opentracing.Tracer, error)

	glog.V(0).Infof("Using tracer for %s", conf.Agent)
	if conf.Agent == DatadogAgent {
		getTracer = getDatadogClientTracer
	} else {
		getTracer = getJaegerClientTracer
	}

	t, err = getTracer(conf.ServiceName, &conf.Agent)
	if err != nil {
		return nil, err
	}
	tw, err = getTracer(conf.ServiceNameWatch, &conf.Agent)
	if err != nil {
		return nil, err
	}
	opentracing.SetGlobalTracer(t)

	conf.GarbageCollectPeriod = time.Minute // TODO conf this from cmd
	return &Reporter{
		conf:            conf,
		tracer:          t,
		tracerWatch:     tw,
		eventReceived:   make(map[types.UID]*event.SpanEvent),
		stopGC:          make(chan struct{}),
		getClientTracer: getTracer,
	}, nil
}

func (r *Reporter) processGC() {
	// TODO use a better GC logic
	r.eventReceivedMutex.Lock()
	defer r.eventReceivedMutex.Unlock()

	if len(r.eventReceived) < r.conf.GarbageCollectionThreshold {
		glog.V(0).Infof("Nothing to garbage collect: %d/%d", len(r.eventReceived), r.conf.GarbageCollectionThreshold)
		return
	}
	glog.V(0).Infof("Garbage collection triggered: %d/%d", len(r.eventReceived), r.conf.GarbageCollectionThreshold)
	for key, val := range r.eventReceived {
		if !val.RequestReceivedTimestamp.Time.Before(time.Now().Add(-time.Minute * 2)) {
			glog.V(1).Infof("Grace period %s %s %s", val.AuditID, val.Stage, val.RequestURI)
			return
		}
		glog.V(0).Infof("Dropping %s %s %s", val.AuditID, val.Stage, val.RequestURI)
		delete(r.eventReceived, key)
	}
}

// StartGarbageCollectionLoop periodically delete staling resources
func (r *Reporter) StartGarbageCollectionLoop() {
	ticker := time.NewTicker(r.conf.GarbageCollectPeriod)
	defer ticker.Stop()

	glog.V(0).Infof("Starting garbage collection loop with a tick every %s", r.conf.GarbageCollectPeriod)
	for {
		select {
		case <-r.stopGC:
			glog.V(0).Infof("Stopping garbage collection loop")
			return

		case <-ticker.C:
			r.processGC()
		}
	}
}

func (r *Reporter) processLine(line *string) {
	ev := &event.SpanEvent{}
	// TODO use a static byte buffer
	err := json.Unmarshal([]byte(*line), ev)
	if err != nil {
		glog.Errorf("Cannot convert to audit: %v", err)
		return
	}
	glog.V(2).Infof("New event %s %s %s", ev.AuditID, ev.Stage, ev.RequestURI)
	if ev.RequestReceivedTimestamp.Time.Before(time.Now().Add(-r.conf.BacklogLimit)) {
		// TODO use prometheus for this
		r.skippedEvent++
		if r.skippedEvent%100 == 0 {
			glog.V(0).Infof("Skipped %d audit events", r.skippedEvent)
		}
		glog.V(1).Infof("Ignoring event %s %s %s %s", ev.AuditID, ev.Stage, ev.RequestURI, ev.RequestReceivedTimestamp.Format("2006-01-02T15:04:05Z"))
		return
	}

	r.eventReceivedMutex.Lock()
	defer r.eventReceivedMutex.Unlock()

	if ev.Stage == audit.StageRequestReceived {
		r.eventReceived[ev.AuditID] = ev
		return
	}
	delete(r.eventReceived, ev.AuditID)
	if ev.Verb == "watch" && ev.Stage == audit.StageResponseComplete {
		// long running query
		clientTracer, err := r.getClientTracer(ev.ClientServiceName(), &r.conf.Agent)
		if err != nil {
			return
		}
		rootSpan := clientTracer.StartSpan("http.request", ev.StartTime(), ev.Tags())
		r.tracerWatch.StartSpan("apiserver", opentracing.ChildOf(rootSpan.Context()), ev.StartTime(), ev.Tags()).FinishWithOptions(ev.FinishTime())
		rootSpan.FinishWithOptions(ev.FinishTime())
		return
	}
	clientTracer, err := r.getClientTracer(ev.ClientServiceName(), &r.conf.Agent)
	if err != nil {
		return
	}
	rootSpan := clientTracer.StartSpan("http.request", ev.StartTime(), ev.Tags())
	r.tracer.StartSpan("apiserver", opentracing.ChildOf(rootSpan.Context()), ev.StartTime(), ev.Tags()).FinishWithOptions(ev.FinishTime())
	rootSpan.FinishWithOptions(ev.FinishTime())
	// TODO use prometheus for this
	r.processedEvent++
	if r.processedEvent%1000 == 0 {
		glog.V(0).Infof("Processed %d audit events", r.processedEvent)
	}
}

// Run start the garbage collector loop and tail the audit log file
func (r *Reporter) Run() error {
	sigChan := make(chan os.Signal)
	defer close(sigChan)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Reset(syscall.SIGINT, syscall.SIGTERM)

	glog.V(0).Infof("Start reading %s and reporting as service %q and %q", r.conf.AuditLogABSPath, r.conf.ServiceName, r.conf.ServiceNameWatch)
	t, err := tail.TailFile(r.conf.AuditLogABSPath, tail.Config{
		Follow: true,
	})
	if err != nil {
		glog.Errorf("Unexpected error while starting tailer: %v", err)
		return err
	}
	go r.StartGarbageCollectionLoop()

	for {
		select {
		case <-sigChan:
			glog.V(0).Infof("Interrupt received, stopping ...")
			r.stopGC <- struct{}{}
			glog.V(0).Infof("Successfully stopped all components")
			return nil

		case line := <-t.Lines:
			if line.Err != nil {
				glog.Warningf("Unexpected error while reading line: %v", err)
				continue
			}
			r.processLine(&line.Text)
		}
	}
}
