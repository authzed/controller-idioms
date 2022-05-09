package libctrl

import (
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilnet "k8s.io/apimachinery/pkg/util/net"
)

// ShouldRetry returns true if the error is transient.
// It returns a delay if the server suggested one.
func ShouldRetry(err error) (bool, time.Duration) {
	if seconds, shouldRetry := apierrors.SuggestsClientDelay(err); shouldRetry {
		return true, time.Duration(seconds) * time.Second
	}
	if utilnet.IsConnectionReset(err) ||
		apierrors.IsInternalError(err) ||
		apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsUnexpectedServerError(err) {
		return true, 0
	}
	return false, 0
}
