// Package configtest sets configuration keys for one test. Only tests import it.
package configtest

import (
	"testing"

	"github.com/spf13/viper"
)

// Set sets the viper key for the running test and puts the previous state back when the test
// ends, so no test leaves its value to the next (the order of the tests does not matter): the old
// value if the key was set, otherwise no value.
func Set(t testing.TB, key string, value interface{}) {
	t.Helper()
	old, wasSet := viper.Get(key), viper.IsSet(key)
	viper.Set(key, value)
	t.Cleanup(func() {
		if wasSet {
			viper.Set(key, old)
		} else {
			viper.Set(key, nil) // viper cannot delete an override; nil reads as unset (IsSet false)
		}
	})
}
