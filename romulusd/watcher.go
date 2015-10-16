package main

import (
	"fmt"
	"time"

	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/watch"
)

type watchFunc func() (watch.Interface, error)

type event struct {
	watch.Event
}

func (e event) String() string {
	m, er := getMeta(e.Object)
	if er != nil {
		return fmt.Sprintf("Event: type=%v object=Unknown", e.Type)
	}
	return fmt.Sprintf(
		"Event: type=%v object={Kind: %q, Name: %q, Namespace: %q} registerable=%v",
		e.Type, m.kind, m.name, m.ns, registerable(e.Object),
	)
}

func acquireWatch(fn watchFunc, out chan<- watch.Interface, c context.Context) {
	retry := 2 * time.Second
	t := time.NewTicker(retry)
	defer t.Stop()

	w, e := fn()
	if e == nil {
		out <- w
		return
	}

	for {
		debugf("Setting watch failed, retry in (%v): %v", retry, e)
		select {
		case <-c.Done():
			return
		case <-t.C:
			w, e := fn()
			if e == nil {
				out <- w
				return
			}
		}
	}
}

func startWatches(c context.Context) (chan event, error) {
	out := make(chan event, 100)
	kc, er := kubeClient()
	if er != nil {
		return out, er
	}
	sv := func() (watch.Interface, error) {
		debugf("Attempting to set watch on Services")
		return kc.Services(api.NamespaceAll).Watch(labels.Everything(), fields.Everything(), "")
	}
	en := func() (watch.Interface, error) {
		debugf("Attempting to set watch on Endpoints")
		return kc.Endpoints(api.NamespaceAll).Watch(labels.Everything(), fields.Everything(), "")
	}

	go watcher("Services", sv, out, c)
	go watcher("Endpoints", en, out, c)
	return out, nil
}

func watcher(name string, fn watchFunc, out chan<- event, c context.Context) {
	var w watch.Interface
	var wc = make(chan watch.Interface, 1)
	defer close(wc)

Acquire:
	go acquireWatch(fn, wc, c)
	select {
	case <-c.Done():
		infof("Closing %s watch channel", name)
		return
	case w = <-wc:
		debugf("%s watch set", name)
	}

	for {
		select {
		case <-c.Done():
			infof("Closing %s watch channel", name)
			return
		case e := <-w.ResultChan():
			if isClosed(e) {
				warnf("%s watch closed: %+v", name, e)
				goto Acquire
			}
			out <- event{e}
		}
	}
}

func isClosed(e watch.Event) bool {
	return e.Type == watch.Error || e == watch.Event{}
}