/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handler

import (
	"context"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MapFunc is the signature required for enqueueing requests from a generic function.
// This type is usually used with EnqueueRequestsFromMapFunc when registering an event handler.
type MapFunc = TypedMapFunc[client.Object, reconcile.Request]

// TypedMapFunc is the signature required for enqueueing requests from a generic function.
// This type is usually used with EnqueueRequestsFromTypedMapFunc when registering an event handler.
//
// TypedMapFunc is experimental and subject to future change.
type TypedMapFunc[object any, request comparable] func(context.Context, object) []request

// EnqueueRequestsFromMapFunc enqueues Requests by running a transformation function that outputs a collection
// of reconcile.Requests on each Event.  The reconcile.Requests may be for an arbitrary set of objects
// defined by some user specified transformation of the source Event.  (e.g. trigger Reconciler for a set of objects
// in response to a cluster resize event caused by adding or deleting a Node)
//
// EnqueueRequestsFromMapFunc is frequently used to fan-out updates from one object to one or more other
// objects of a differing type.
//
// For UpdateEvents which contain both a new and old object, the transformation function is run on both
// objects and both sets of Requests are enqueue.
func EnqueueRequestsFromMapFunc(fn MapFunc) EventHandler {
	return TypedEnqueueRequestsFromMapFunc(fn)
}

// TypedEnqueueRequestsFromMapFunc enqueues Requests by running a transformation function that outputs a collection
// of reconcile.Requests on each Event.  The reconcile.Requests may be for an arbitrary set of objects
// defined by some user specified transformation of the source Event.  (e.g. trigger Reconciler for a set of objects
// in response to a cluster resize event caused by adding or deleting a Node)
//
// TypedEnqueueRequestsFromMapFunc is frequently used to fan-out updates from one object to one or more other
// objects of a differing type.
//
// For TypedUpdateEvents which contain both a new and old object, the transformation function is run on both
// objects and both sets of Requests are enqueue.
//
// TypedEnqueueRequestsFromMapFunc is experimental and subject to future change.
func TypedEnqueueRequestsFromMapFunc[object any, request comparable](fn TypedMapFunc[object, request]) TypedEventHandler[object, request] {
	return &enqueueRequestsFromMapFunc[object, request]{
		toRequests: fn,
	}
}

var _ EventHandler = &enqueueRequestsFromMapFunc[client.Object, reconcile.Request]{}

type enqueueRequestsFromMapFunc[object any, request comparable] struct {
	// Mapper transforms the argument into a slice of keys to be reconciled
	toRequests TypedMapFunc[object, request]
}

// Create implements EventHandler.
func (e *enqueueRequestsFromMapFunc[object, request]) Create(
	ctx context.Context,
	evt event.TypedCreateEvent[object],
	q workqueue.TypedRateLimitingInterface[request],
) {
	reqs := map[request]empty{}
	priorityQueue, isPriorityQueue := q.(priorityqueue.PriorityQueue[request])
	if !isPriorityQueue {
		e.mapAndEnqueue(ctx, q, evt.Object, reqs)
		return
	}
	e.mapAndEnqueuePriorityCreate(ctx, priorityQueue, evt.Object, reqs)
}

// Update implements EventHandler.
func (e *enqueueRequestsFromMapFunc[object, request]) Update(
	ctx context.Context,
	evt event.TypedUpdateEvent[object],
	q workqueue.TypedRateLimitingInterface[request],
) {
	priorityQueue, isPriorityQueue := q.(priorityqueue.PriorityQueue[request])
	reqs := map[request]empty{}
	if !isPriorityQueue {
		e.mapAndEnqueue(ctx, q, evt.ObjectOld, reqs)
		e.mapAndEnqueue(ctx, q, evt.ObjectNew, reqs)
		return
	}
	e.mapAndEnqueuePriorityUpdate(ctx, priorityQueue, evt.ObjectOld, evt.ObjectNew, reqs)
}

// Delete implements EventHandler.
func (e *enqueueRequestsFromMapFunc[object, request]) Delete(
	ctx context.Context,
	evt event.TypedDeleteEvent[object],
	q workqueue.TypedRateLimitingInterface[request],
) {
	reqs := map[request]empty{}
	e.mapAndEnqueue(ctx, q, evt.Object, reqs)
}

// Generic implements EventHandler.
func (e *enqueueRequestsFromMapFunc[object, request]) Generic(
	ctx context.Context,
	evt event.TypedGenericEvent[object],
	q workqueue.TypedRateLimitingInterface[request],
) {
	reqs := map[request]empty{}
	e.mapAndEnqueue(ctx, q, evt.Object, reqs)
}

func (e *enqueueRequestsFromMapFunc[object, request]) mapAndEnqueue(ctx context.Context, q workqueue.TypedRateLimitingInterface[request], o object, reqs map[request]empty) {
	for _, req := range e.toRequests(ctx, o) {
		_, ok := reqs[req]
		if !ok {
			q.Add(req)
			reqs[req] = empty{}
		}
	}
}

func (e *enqueueRequestsFromMapFunc[object, request]) mapAndEnqueuePriorityCreate(ctx context.Context, q priorityqueue.PriorityQueue[request], o object, reqs map[request]empty) {
	for _, req := range e.toRequests(ctx, o) {
		var priority int
		obj := any(o).(client.Object)
		evtObj := event.TypedCreateEvent[client.Object]{
			Object: obj,
		}
		if isObjectUnchanged(evtObj) {
			priority = LowPriority
		}

		q.AddWithOpts(priorityqueue.AddOpts{Priority: priority}, req)
		reqs[req] = empty{}
	}
}

func (e *enqueueRequestsFromMapFunc[object, request]) mapAndEnqueuePriorityUpdate(ctx context.Context, q priorityqueue.PriorityQueue[request], oldobj object, newobj object, reqs map[request]empty) {
	list := e.toRequests(ctx, oldobj)
	list = append(list, e.toRequests(ctx, newobj)...)
	for _, req := range list {
		var priority int
		objnew := any(oldobj).(client.Object)
		objold := any(newobj).(client.Object)
		if objnew.GetResourceVersion() == objold.GetResourceVersion() {
			priority = LowPriority
		}
		q.AddWithOpts(priorityqueue.AddOpts{Priority: priority}, req)
		reqs[req] = empty{}
	}
}
