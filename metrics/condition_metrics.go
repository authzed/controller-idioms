// Package metrics implements common prometheus metric collectors.
//
// For any resource that implements the standard `metav1.Conditions` array in
// its status, `ConditionStatusCollector` will report metrics on how many
// objects have been in certain conditions, and for how long.
package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/component-base/metrics"

	"github.com/authzed/controller-idioms/pause"
)

// ConditionStatusCollector reports condition metrics for any type that
// implements a metav1.Condition list in the status.
type ConditionStatusCollector[K pause.HasStatusConditions] struct {
	metrics.BaseStableCollector

	listerBuilders []func() ([]K, error)

	ObjectCount           *metrics.Desc
	ObjectConditionCount  *metrics.Desc
	ObjectTimeInCondition *metrics.Desc
	CollectorTime         *metrics.Desc
	CollectorErrors       *metrics.Desc
}

// NewConditionStatusCollector creates a new ConditionStatusCollector, with
// flags for specifying how to generate the names of the metrics.
func NewConditionStatusCollector[K pause.HasStatusConditions](namespace string, subsystem string, resourceName string) *ConditionStatusCollector[K] {
	return &ConditionStatusCollector[K]{
		ObjectCount: metrics.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "count"),
			fmt.Sprintf("Gauge showing the number of %s managed by this operator", resourceName),
			nil, nil, metrics.ALPHA, "",
		),
		ObjectConditionCount: metrics.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "condition_count"),
			fmt.Sprintf("Gauge showing the number of %s with each type of condition", resourceName),
			[]string{
				"condition",
			}, nil, metrics.ALPHA, "",
		),
		ObjectTimeInCondition: metrics.NewDesc(
			prometheus.BuildFQName(namespace, subsystem, "condition_time_seconds"),
			fmt.Sprintf("Gauge showing the amount of time %s have spent in the current condition", resourceName),
			[]string{
				"condition",
				"object",
			}, nil, metrics.ALPHA, "",
		),
		CollectorTime: metrics.NewDesc(
			prometheus.BuildFQName(namespace, subsystem+"_status_collector", "execution_seconds"),
			fmt.Sprintf("Amount of time spent on the last run of the %s status collector", resourceName),
			nil, nil, metrics.ALPHA, "",
		),
		CollectorErrors: metrics.NewDesc(
			prometheus.BuildFQName(namespace, subsystem+"_status_collector", "errors_count"),
			fmt.Sprintf("Number of errors encountered on the last run of the %s status collector", resourceName),
			nil, nil, metrics.ALPHA, "",
		),
	}
}

func (c *ConditionStatusCollector[K]) AddListerBuilder(lb func() ([]K, error)) {
	c.listerBuilders = append(c.listerBuilders, lb)
}

func (c *ConditionStatusCollector[K]) DescribeWithStability(ch chan<- *metrics.Desc) {
	ch <- c.ObjectCount
	ch <- c.ObjectConditionCount
	ch <- c.ObjectTimeInCondition
	ch <- c.CollectorTime
	ch <- c.CollectorErrors
}

func (c *ConditionStatusCollector[K]) CollectWithStability(ch chan<- metrics.Metric) {
	start := time.Now()
	totalErrors := 0
	defer func() {
		duration := time.Since(start)

		ch <- metrics.NewLazyConstMetric(c.CollectorTime, metrics.GaugeValue, duration.Seconds())
		ch <- metrics.NewLazyConstMetric(c.CollectorErrors, metrics.GaugeValue, float64(totalErrors))
	}()

	collectTime := time.Now()

	for _, lb := range c.listerBuilders {
		objs, err := lb()
		if err != nil {
			totalErrors++
			continue
		}

		ch <- metrics.NewLazyConstMetric(c.ObjectCount, metrics.GaugeValue, float64(len(objs)))

		objectsWithCondition := map[string]uint16{}
		for _, o := range objs {
			objectName := types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}.String()
			for _, condition := range o.GetStatusConditions() {
				objectsWithCondition[condition.Type]++

				timeInCondition := collectTime.Sub(condition.LastTransitionTime.Time)

				ch <- metrics.NewLazyConstMetric(
					c.ObjectTimeInCondition,
					metrics.GaugeValue,
					timeInCondition.Seconds(),
					condition.Type,
					objectName,
				)
			}
		}

		for conditionType, count := range objectsWithCondition {
			ch <- metrics.NewLazyConstMetric(c.ObjectConditionCount, metrics.GaugeValue, float64(count), conditionType)
		}
	}
}
