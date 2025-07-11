package main

import (
	"fmt"
	"os"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "datadog-test-runner",
	Short: "A test runner with Datadog",
	Long:  "A command line tool for running tests with Datadog Test Optimization.",
}

var helloCmd = &cobra.Command{
	Use:   "hello",
	Short: "Say hello with Datadog tracing",
	Run: func(cmd *cobra.Command, args []string) {
		tracer.Start(tracer.WithService("datadog-test-runner"))
		defer tracer.Stop()

		span := tracer.StartSpan("greeting")
		defer span.Finish()

		span.SetTag("greeting", "Hello")

		fmt.Println("Hello, World!")
	},
}

var skippablePercentageCmd = &cobra.Command{
	Use:   "skippable-percentage",
	Short: "Calculate skippable percentage with Datadog tracing",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Calculating skippable percentage...")
	},
}

func init() {
	rootCmd.AddCommand(helloCmd)
	rootCmd.AddCommand(skippablePercentageCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
