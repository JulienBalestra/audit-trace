## audit-trace

Use this command to parse and submit kubernetes apiserver audit logs as traces

### Synopsis

Use this command to parse and submit kubernetes apiserver audit logs as traces

```
audit-trace command line [flags]
```

### Examples

```

audit-trace --audit-log-path /var/log/kubernetes/audit.log 

```

### Options

```
      --agent string                  agent to use (jaeger, datadog) (default "datadog")
      --audit-log-path string         audit-log file path
      --backlog-limit string          backlog-limit to process (default "15m0s")
      --gc-threshold string           garbage collection threshold for map references, lower use less memory but slower (default "100")
  -h, --help                          help for audit-trace
      --service-name string           service name (default "kubernetes-audit")
      --service-name-watch string     service name for watch verb (default "kubernetes-audit-watch")
      --trace-agent-endpoint string   trace agent endpoint host:port (default "127.0.0.1:8126")
  -v, --verbose int                   verbose level
```

