// Code generated by counterfeiter. DO NOT EDIT.
package fake

import (
	"sync"

	"github.com/authzed/ktrllib"
)

type FakeControlDoneRequeueErr struct {
	DoneStub        func()
	doneMutex       sync.RWMutex
	doneArgsForCall []struct {
	}
	RequeueStub        func()
	requeueMutex       sync.RWMutex
	requeueArgsForCall []struct {
	}
	RequeueErrStub        func(error)
	requeueErrMutex       sync.RWMutex
	requeueErrArgsForCall []struct {
		arg1 error
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeControlDoneRequeueErr) Done() {
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

func (fake *FakeControlDoneRequeueErr) DoneCallCount() int {
	fake.doneMutex.RLock()
	defer fake.doneMutex.RUnlock()
	return len(fake.doneArgsForCall)
}

func (fake *FakeControlDoneRequeueErr) DoneCalls(stub func()) {
	fake.doneMutex.Lock()
	defer fake.doneMutex.Unlock()
	fake.DoneStub = stub
}

func (fake *FakeControlDoneRequeueErr) Requeue() {
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

func (fake *FakeControlDoneRequeueErr) RequeueCallCount() int {
	fake.requeueMutex.RLock()
	defer fake.requeueMutex.RUnlock()
	return len(fake.requeueArgsForCall)
}

func (fake *FakeControlDoneRequeueErr) RequeueCalls(stub func()) {
	fake.requeueMutex.Lock()
	defer fake.requeueMutex.Unlock()
	fake.RequeueStub = stub
}

func (fake *FakeControlDoneRequeueErr) RequeueErr(arg1 error) {
	fake.requeueErrMutex.Lock()
	fake.requeueErrArgsForCall = append(fake.requeueErrArgsForCall, struct {
		arg1 error
	}{arg1})
	stub := fake.RequeueErrStub
	fake.recordInvocation("RequeueErr", []interface{}{arg1})
	fake.requeueErrMutex.Unlock()
	if stub != nil {
		fake.RequeueErrStub(arg1)
	}
}

func (fake *FakeControlDoneRequeueErr) RequeueErrCallCount() int {
	fake.requeueErrMutex.RLock()
	defer fake.requeueErrMutex.RUnlock()
	return len(fake.requeueErrArgsForCall)
}

func (fake *FakeControlDoneRequeueErr) RequeueErrCalls(stub func(error)) {
	fake.requeueErrMutex.Lock()
	defer fake.requeueErrMutex.Unlock()
	fake.RequeueErrStub = stub
}

func (fake *FakeControlDoneRequeueErr) RequeueErrArgsForCall(i int) error {
	fake.requeueErrMutex.RLock()
	defer fake.requeueErrMutex.RUnlock()
	argsForCall := fake.requeueErrArgsForCall[i]
	return argsForCall.arg1
}

func (fake *FakeControlDoneRequeueErr) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.doneMutex.RLock()
	defer fake.doneMutex.RUnlock()
	fake.requeueMutex.RLock()
	defer fake.requeueMutex.RUnlock()
	fake.requeueErrMutex.RLock()
	defer fake.requeueErrMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *FakeControlDoneRequeueErr) recordInvocation(key string, args []interface{}) {
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

var _ libctrl.ControlDoneRequeueErr = new(FakeControlDoneRequeueErr)
