package cmd

import (
	"context"
	"os"

	"github.com/nhalm/canonlog"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "myapp",
	Short: "My application",
	Long:  `My application is a service for...`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		ctx := canonlog.NewContext(context.Background())
		canonlog.ErrorAdd(ctx, err)
		canonlog.Flush(ctx)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is .env)")
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigFile(".env")
		viper.SetConfigType("env")
	}

	viper.AutomaticEnv()

	configLoaded := viper.ReadInConfig() == nil

	logLevel := viper.GetString("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	logFormat := viper.GetString("LOG_FORMAT")
	if logFormat == "" {
		logFormat = "text"
	}
	canonlog.SetupGlobalLogger(logLevel, logFormat)

	if configLoaded {
		ctx := canonlog.NewContext(context.Background())
		canonlog.InfoAddMany(ctx, map[string]any{
			"event":       "config_loaded",
			"config_file": viper.ConfigFileUsed(),
		})
		canonlog.Flush(ctx)
	}
}
