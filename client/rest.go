package client

import "k8s.io/client-go/rest"

func DisableClientSideRateLimiting(restConfig *rest.Config) {
	restConfig.Burst = 2000
	restConfig.QPS = -1
}
