package config

import (
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func ReadConfig(path string) {
	log.Infof("Read Config: %s", path)

	viper.SetConfigName("config")
	viper.AddConfigPath(path)
	viper.SetConfigType("yml")

	viper.SetEnvPrefix("VFEEG_BACKEND")
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}
}
