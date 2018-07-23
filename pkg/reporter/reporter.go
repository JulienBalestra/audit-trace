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
	GarbageCollectionThreshold int
	Agent                      string
	GarbageCollectPeriod       time.Duration
}

// Reporter contains the configuration and the state
type Reporter struct {
	conf *Config

	tracer             opentracing.Tracer
	eventReceived      map[types.UID]*event.SpanEvent
	eventReceivedMutex sync.Mutex
	stopGC             chan struct{}
}

// NewReporter instantiate a New Reporter
func NewReporter(conf *Config) (*Reporter, error) {
	var err error
	var t opentracing.Tracer

	glog.V(0).Infof("Using tracer for %s", conf.Agent)
	if conf.Agent == DatadogAgent {
		t = opentracer.New(
			tracer.WithServiceName(conf.ServiceName),
			tracer.WithAgentAddr(conf.LocalAgentHostPort),
		)
	} else {
		cfg := jaeger.Configuration{
			ServiceName: conf.ServiceName,
			Sampler: &jaeger.SamplerConfig{
				Type:  "const",
				Param: 1,
			},
			Reporter: &jaeger.ReporterConfig{
				BufferFlushInterval: 1 * time.Second,
				LocalAgentHostPort:  conf.LocalAgentHostPort,
			},
		}
		t, _, err = cfg.NewTracer()
		if err != nil {
			glog.Errorf("Unexpected error: %v", err)
			return nil, err
		}
	}
	opentracing.SetGlobalTracer(t)

	conf.GarbageCollectPeriod = time.Minute // TODO conf this from cmd
	return &Reporter{
		conf:          conf,
		tracer:        t,
		eventReceived: make(map[types.UID]*event.SpanEvent),
		stopGC:        make(chan struct{}),
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
	glog.V(2).Infof("Event %s %s %s", ev.AuditID, ev.Stage, ev.RequestURI)

	r.eventReceivedMutex.Lock()
	defer r.eventReceivedMutex.Unlock()

	if ev.Stage == audit.StageRequestReceived {
		r.eventReceived[ev.AuditID] = ev
		return
	}
	r.tracer.StartSpan("http.request", ev.StartTime(), ev.Tags()).FinishWithOptions(ev.FinishTime())
	delete(r.eventReceived, ev.AuditID)
}

// Run start the garbage collector loop and tail the audit log file
func (r *Reporter) Run() error {
	sigChan := make(chan os.Signal)
	defer close(sigChan)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Reset(syscall.SIGINT, syscall.SIGTERM)

	glog.V(0).Infof("Start reading %s and reporting as service %q", r.conf.AuditLogABSPath, r.conf.ServiceName)
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
