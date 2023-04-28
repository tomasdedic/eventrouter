package sink

import (
	"encoding/json"

	"github.com/golang/glog"
	v1 "k8s.io/api/core/v1"
)

type NsLabelsEventData struct {
	Verb            string            `json:"verb"`
	Event           *v1.Event         `json:"event"`
	NamespaceLabels map[string]string `json:"namespacelabels"`
	OldEvent        *v1.Event         `json:"old_event,omitempty"`
}

func NewEventData(eventNew *v1.Event, eventOld *v1.Event, nsLabels map[string]string) NsLabelsEventData {
	var eventData NsLabelsEventData
	if eventOld == nil {
		eventData = NsLabelsEventData{
			Verb:            "ADDED",
			Event:           eventNew,
			NamespaceLabels: nsLabels,
		}
	} else {
		eventData = NsLabelsEventData{
			Verb:            "UPDATED",
			Event:           eventNew,
			OldEvent:        eventOld,
			NamespaceLabels: nsLabels,
		}
	}
	return eventData
}

type Sink struct {
	// TODO: create a channel and buffer for scaling
}

// UpdateEvents implements the EventSinkInterface
func (gs *Sink) UpdateEvents(eventNew *v1.Event, eventOld *v1.Event, nsLabels map[string]string) {
	eventData := NewEventData(eventNew, eventOld, nsLabels)

	if eJSONBytes, err := json.Marshal(eventData); err == nil {
		glog.Info(string(eJSONBytes))
	} else {
		glog.Warningf("Failed to json serialize event: %v", err)
	}
}
