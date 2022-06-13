package metrics

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/component-base/metrics"

	"github.com/authzed/controller-idioms"
)

type ConditionStatusCollector[K libctrl.HasStatusConditions] struct {
	metrics.BaseStableCollector

	listerBuilders []func() ([]K, error)

	ObjectCount           *metrics.Desc
	ObjectConditionCount  *metrics.Desc
	ObjectTimeInCondition *metrics.Desc
	CollectorTime         *metrics.Desc
	CollectorErrors       *metrics.Desc
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

		clustersWithCondition := map[string]uint16{}
		for _, o := range objs {
			clusterName := types.NamespacedName{Name: o.GetName(), Namespace: o.GetNamespace()}.String()
			for _, condition := range o.GetStatusConditions() {
				clustersWithCondition[condition.Type]++

				timeInCondition := collectTime.Sub(condition.LastTransitionTime.Time)

				ch <- metrics.NewLazyConstMetric(
					c.ObjectTimeInCondition,
					metrics.GaugeValue,
					timeInCondition.Seconds(),
					condition.Type,
					clusterName,
				)
			}
		}

		for conditionType, count := range clustersWithCondition {
			ch <- metrics.NewLazyConstMetric(c.ObjectConditionCount, metrics.GaugeValue, float64(count), conditionType)
		}
	}
}
