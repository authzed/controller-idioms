// Code generated by counterfeiter. DO NOT EDIT.
package fake

import (
	"sync"

	"github.com/authzed/ktrllib"
)

type FakeControlDoneRequeue struct {
	DoneStub        func()
	doneMutex       sync.RWMutex
	doneArgsForCall []struct {
	}
	RequeueStub        func()
	requeueMutex       sync.RWMutex
	requeueArgsForCall []struct {
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeControlDoneRequeue) Done() {
	fake.doneMutex.Lock()
	fake.doneArgsForCall = append(fake.doneArgsForCall, struct {
	}{})
	stub := fake.DoneStub
	fake.recordInvocation("Done", []interface{}{})
	fake.doneMutex.Unlock()
	if stub != nil {
		fake.DoneStub()
	}
}

func (fake *FakeControlDoneRequeue) DoneCallCount() int {
	fake.doneMutex.RLock()
	defer fake.doneMutex.RUnlock()
	return len(fake.doneArgsForCall)
}

func (fake *FakeControlDoneRequeue) DoneCalls(stub func()) {
	fake.doneMutex.Lock()
	defer fake.doneMutex.Unlock()
	fake.DoneStub = stub
}

func (fake *FakeControlDoneRequeue) Requeue() {
	fake.requeueMutex.Lock()
	fake.requeueArgsForCall = append(fake.requeueArgsForCall, struct {
	}{})
	stub := fake.RequeueStub
	fake.recordInvocation("Requeue", []interface{}{})
	fake.requeueMutex.Unlock()
	if stub != nil {
		fake.RequeueStub()
	}
}

func (fake *FakeControlDoneRequeue) RequeueCallCount() int {
	fake.requeueMutex.RLock()
	defer fake.requeueMutex.RUnlock()
	return len(fake.requeueArgsForCall)
}

func (fake *FakeControlDoneRequeue) RequeueCalls(stub func()) {
	fake.requeueMutex.Lock()
	defer fake.requeueMutex.Unlock()
	fake.RequeueStub = stub
}

func (fake *FakeControlDoneRequeue) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.doneMutex.RLock()
	defer fake.doneMutex.RUnlock()
	fake.requeueMutex.RLock()
	defer fake.requeueMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *FakeControlDoneRequeue) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}

var _ libctrl.ControlDoneRequeue = new(FakeControlDoneRequeue)
