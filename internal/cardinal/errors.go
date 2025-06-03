package cardinal

import (
	"errors"
	"fmt"
)

// UsageError identifies invalid command-line input.
type UsageError struct{ Message string }

func (e *UsageError) Error() string {
	return e.Message
}

// NewUsageError creates a command-line validation error.
func NewUsageError(format string, args ...interface{}) error {
	return &UsageError{Message: fmt.Sprintf(format, args...)}
}

// IsUsageError reports whether an error should produce the usage exit code.
func IsUsageError(err error) bool {
	var usageError *UsageError
	return errors.As(err, &usageError)
}

// ConfigError identifies invalid configuration.
type ConfigError struct{ Err error }

func (e *ConfigError) Error() string {
	return fmt.Sprintf("configuration: %v", e.Err)
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}
