package event

import (
	"strings"

	"github.com/opentracing/opentracing-go"
	"k8s.io/apiserver/pkg/apis/audit"
	"strconv"
)

// SpanEvent is a kubernetes audit Event generated from the audit logs
type SpanEvent struct {
	*audit.Event
}

func addTag(tags opentracing.Tags, key, value string) {
	if value == "" {
		return
	}
	tags[key] = value
}

func (e *SpanEvent) baseTags() opentracing.Tags {
	baseTags := opentracing.Tags{
		// This is required by the datadog APM
		"http.url":      e.RequestURI,
		"http.method":   e.Verb,
		"resource.name": e.ObjectRef.APIVersion + "/" + e.ObjectRef.Resource,
		"span.type":     "web",

		"k8s.level":         e.Level,
		"k8s.audit_id":      e.AuditID,
		"k8s.stage":         e.Stage,
		"k8s.request_uri":   e.RequestURI,
		"k8s.verb":          e.Verb,
		"k8s.user.username": e.User.Username,
		"k8s.user.uid":      e.User.UID,
		// TODO remove join
		"k8s.user.groups": strings.Join(e.User.Groups, ","),
		"k8s.source_ips":  strings.Join(e.SourceIPs, ","),
	}
	addTag(baseTags, "k8s.object_ref.resource", e.ObjectRef.Resource)
	addTag(baseTags, "k8s.object_ref.namespace", e.ObjectRef.Namespace)
	addTag(baseTags, "k8s.object_ref.name", e.ObjectRef.Name)
	addTag(baseTags, "k8s.object_ref.api_version", e.ObjectRef.APIVersion)
	addTag(baseTags, "k8s.object_ref.sub_resource", e.ObjectRef.Subresource)
	addTag(baseTags, "k8s.object_ref.api_group", e.ObjectRef.APIGroup)
	addTag(baseTags, "k8s.object_ref.uid", string(e.ObjectRef.UID))
	addTag(baseTags, "k8s.object_ref.resource_version", e.ObjectRef.ResourceVersion)
	return baseTags
}

func (e *SpanEvent) statusTags() opentracing.Tags {
	baseTags := e.baseTags()

	// This creates an issue on the datadog UI
	//baseTags["k8s.response_status.code"] = e.ResponseStatus.Code

	baseTags["http.status_code"] = strconv.Itoa(int(e.ResponseStatus.Code))
	addTag(baseTags, "k8s.response_status.status", e.ResponseStatus.Status)
	addTag(baseTags, "k8s.response_status.message", e.ResponseStatus.Message)
	addTag(baseTags, "k8s.response_status.reason", string(e.ResponseStatus.Reason))
	return baseTags
}

// Tags returns the opentracing tags
func (e *SpanEvent) Tags() opentracing.Tags {
	if e.Stage == audit.StageRequestReceived {
		return e.baseTags()
	}
	return e.statusTags()
}

// StartTime returns the time when the event was generated
func (e *SpanEvent) StartTime() opentracing.StartTime {
	return opentracing.StartTime(e.RequestReceivedTimestamp.Time)
}

// FinishTime returns the time when the event was finished
func (e *SpanEvent) FinishTime() opentracing.FinishOptions {
	return opentracing.FinishOptions{
		FinishTime: e.StageTimestamp.Time,
	}
}
