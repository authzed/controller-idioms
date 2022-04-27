// Code generated by counterfeiter. DO NOT EDIT.
package fake

import (
	"sync"
	"time"

	"github.com/authzed/ktrllib"
)

type FakeControlDoneRequeueAfter struct {
	DoneStub        func()
	doneMutex       sync.RWMutex
	doneArgsForCall []struct {
	}
	RequeueAfterStub        func(time.Duration)
	requeueAfterMutex       sync.RWMutex
	requeueAfterArgsForCall []struct {
		arg1 time.Duration
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeControlDoneRequeueAfter) Done() {
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

func (fake *FakeControlDoneRequeueAfter) DoneCallCount() int {
	fake.doneMutex.RLock()
	defer fake.doneMutex.RUnlock()
	return len(fake.doneArgsForCall)
}

func (fake *FakeControlDoneRequeueAfter) DoneCalls(stub func()) {
	fake.doneMutex.Lock()
	defer fake.doneMutex.Unlock()
	fake.DoneStub = stub
}

func (fake *FakeControlDoneRequeueAfter) RequeueAfter(arg1 time.Duration) {
	fake.requeueAfterMutex.Lock()
	fake.requeueAfterArgsForCall = append(fake.requeueAfterArgsForCall, struct {
		arg1 time.Duration
	}{arg1})
	stub := fake.RequeueAfterStub
	fake.recordInvocation("RequeueAfter", []interface{}{arg1})
	fake.requeueAfterMutex.Unlock()
	if stub != nil {
		fake.RequeueAfterStub(arg1)
	}
}

func (fake *FakeControlDoneRequeueAfter) RequeueAfterCallCount() int {
	fake.requeueAfterMutex.RLock()
	defer fake.requeueAfterMutex.RUnlock()
	return len(fake.requeueAfterArgsForCall)
}

func (fake *FakeControlDoneRequeueAfter) RequeueAfterCalls(stub func(time.Duration)) {
	fake.requeueAfterMutex.Lock()
	defer fake.requeueAfterMutex.Unlock()
	fake.RequeueAfterStub = stub
}

func (fake *FakeControlDoneRequeueAfter) RequeueAfterArgsForCall(i int) time.Duration {
	fake.requeueAfterMutex.RLock()
	defer fake.requeueAfterMutex.RUnlock()
	argsForCall := fake.requeueAfterArgsForCall[i]
	return argsForCall.arg1
}

func (fake *FakeControlDoneRequeueAfter) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.doneMutex.RLock()
	defer fake.doneMutex.RUnlock()
	fake.requeueAfterMutex.RLock()
	defer fake.requeueAfterMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *FakeControlDoneRequeueAfter) recordInvocation(key string, args []interface{}) {
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

var _ libctrl.ControlDoneRequeueAfter = new(FakeControlDoneRequeueAfter)
