package workqueue

import "time"

type Interface interface {
	Add(item any)
	Get() (item any, shutdown bool)
	Done(item any)
	Len() int
	ShutDown()
	ShuttingDown() bool
}

type RateLimitingInterface interface {
	Interface
	AddRateLimited(item any)
	AddAfter(item any, duration time.Duration)
}
