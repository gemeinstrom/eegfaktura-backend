package configtest_test

import (
	"testing"

	"at.ourproject/vfeeg-backend/config/configtest"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestSet_restoresThePreviousValue(t *testing.T) {
	configtest.Set(t, "configtest.key", "outer")
	t.Run("inner", func(t *testing.T) {
		configtest.Set(t, "configtest.key", "inner")
		assert.Equal(t, "inner", viper.GetString("configtest.key"))
	})
	assert.Equal(t, "outer", viper.GetString("configtest.key"))
}

func TestSet_unsetKeyIsUnsetAfterwards(t *testing.T) {
	t.Run("inner", func(t *testing.T) {
		configtest.Set(t, "configtest.unset", "x")
		assert.True(t, viper.IsSet("configtest.unset"))
	})
	assert.False(t, viper.IsSet("configtest.unset"))
}
