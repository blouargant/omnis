package sandbox

import "time"

// retryWithBackoff runs fn up to maxAttempts times, doubling the delay each
// failure starting from base. It returns the last error if all attempts fail.
func retryWithBackoff(maxAttempts int, base time.Duration, fn func() error) error {
	var err error
	delay := base
	for i := 0; i < maxAttempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(delay)
		delay *= 2
	}
	return err
}
