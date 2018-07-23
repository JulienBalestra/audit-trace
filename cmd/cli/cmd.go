package cli

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/JulienBalestra/audit-trace/pkg/reporter"
)

const programName = "audit-trace"

var viperConfig = viper.New()

// NewCommand creates a cobra command line with viper bindings
func NewCommand() (*cobra.Command, *int) {
	var verbose int
	var exitCode int

	rootCommand := &cobra.Command{
		Use:   fmt.Sprintf("%s command line", programName),
		Short: "Use this command to parse and submit kubernetes apiserver audit logs as traces",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			flag.Lookup("alsologtostderr").Value.Set("true")
			flag.Lookup("v").Value.Set(strconv.Itoa(verbose))
		},
		Args: cobra.ExactArgs(0),
		Example: fmt.Sprintf(`
%s --audit-log-path /var/log/kubernetes/audit.log 
`, programName),
		Run: func(cmd *cobra.Command, args []string) {
			r, err := newReporter()
			if err != nil {
				glog.Errorf("Cannot start: %v", err)
				exitCode = 1
				cmd.Help()
				return
			}
			err = r.Run()
			if err != nil {
				glog.Errorf("Program returns error: %v", err)
				exitCode = 2
				return
			}
		},
	}

	rootCommand.PersistentFlags().IntVarP(&verbose, "verbose", "v", 0, "verbose level")

	viperConfig.SetDefault("audit-log-path", "")
	rootCommand.PersistentFlags().String("audit-log-path", viperConfig.GetString("audit-log-path"), "audit-log file path")
	viperConfig.BindPFlag("audit-log-path", rootCommand.PersistentFlags().Lookup("audit-log-path"))

	viperConfig.SetDefault("trace-agent-endpoint", "127.0.0.1:8126")
	rootCommand.PersistentFlags().String("trace-agent-endpoint", viperConfig.GetString("trace-agent-endpoint"), "trace agent endpoint host:port")
	viperConfig.BindPFlag("trace-agent-endpoint", rootCommand.PersistentFlags().Lookup("trace-agent-endpoint"))

	viperConfig.SetDefault("service-name", "kubernetes-audit")
	rootCommand.PersistentFlags().String("service-name", viperConfig.GetString("service-name"), "service name")
	viperConfig.BindPFlag("service-name", rootCommand.PersistentFlags().Lookup("service-name"))

	viperConfig.SetDefault("gc-threshold", 100)
	rootCommand.PersistentFlags().String("gc-threshold", viperConfig.GetString("gc-threshold"), "garbage collection threshold for map references, lower use less memory but slower")
	viperConfig.BindPFlag("gc-threshold", rootCommand.PersistentFlags().Lookup("gc-threshold"))

	viperConfig.SetDefault("agent", reporter.DatadogAgent)
	rootCommand.PersistentFlags().String("agent", viperConfig.GetString("agent"), fmt.Sprintf("agent to use (%s, %s)", reporter.JaegerAgent, reporter.DatadogAgent))
	viperConfig.BindPFlag("agent", rootCommand.PersistentFlags().Lookup("agent"))

	return rootCommand, &exitCode
}

func newReporter() (*reporter.Reporter, error) {
	auditLogABSPath := viperConfig.GetString("audit-log-path")
	if auditLogABSPath == "" {
		return nil, fmt.Errorf("empty audit-log-path")
	}
	localAgentHostPort := viperConfig.GetString("trace-agent-endpoint")
	if localAgentHostPort == "" {
		return nil, fmt.Errorf("empty trace-agent-endpoint")
	}
	serviceName := viperConfig.GetString("service-name")
	if localAgentHostPort == "" {
		return nil, fmt.Errorf("empty service-name")
	}
	gcThreshold := viperConfig.GetInt("gc-threshold")
	if gcThreshold < 1 {
		return nil, fmt.Errorf("invalid gc-threshold: %d", gcThreshold)
	}
	agent := viperConfig.GetString("agent")
	if agent != reporter.DatadogAgent && agent != reporter.JaegerAgent {
		return nil, fmt.Errorf("invalid agent: %s", agent)
	}
	return reporter.NewReporter(
		&reporter.Config{
			AuditLogABSPath:            auditLogABSPath,
			LocalAgentHostPort:         localAgentHostPort,
			ServiceName:                serviceName,
			GarbageCollectionThreshold: gcThreshold,
			Agent: agent,
		},
	)
}
