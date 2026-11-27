package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s-log-analyzer/internal/agent"
	"k8s-log-analyzer/internal/creds"
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/serve"
	"k8s-log-analyzer/internal/toolbox"
)

const serveHelp = `log-analyzer serve runs the investigator as a small web service (REST API), so a script,
a dashboard or Prometheus Alertmanager can ask for a report.

USAGE
  log-analyzer serve [options]

FIRST TRY (two terminals)
  log-analyzer serve
  log-analyzer --server 127.0.0.1:8080 --namespace shop

From a script or any other program (the same thing over HTTP):
  curl -X POST "http://127.0.0.1:8080/v1/investigations?wait=true" -d '{"namespace":"shop"}'

It listens on this computer only, so it needs no token. To open it to the network, give a token:
  openssl rand -hex 24 > token && chmod 600 token
  log-analyzer serve --listen :8080 --token-file token

OPTIONS
  --listen ADDR        where to listen (default 127.0.0.1:8080)
  --token-file FILE    the token clients must send as "Authorization: Bearer <token>".
                       Required when --listen is not this computer only
  --namespaces A,B     only allow these namespaces (default: any the cluster account can read)
  --queue N            how many investigations may wait behind the running one (default 5)
  --timeout DURATION   longest time one investigation may run (default 10m)
  --result-ttl DUR     how long a finished report can be fetched (default 1h)

The AI model options are the same as for the command line: --provider, --model, --api-key-file,
--base-url, --llm. With no API key, the built-in rules are used and nothing leaves your computer.
Test options: --dry-run, --llm replay, --recordings-dir.

More: docs/API.md
`

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), serveHelp) }
	listen := fs.String("listen", "127.0.0.1:8080", "where to listen")
	tokenFile := fs.String("token-file", "", "file holding the token clients must send")
	namespaces := fs.String("namespaces", "", "comma-separated namespaces that may be investigated")
	queue := fs.Int("queue", 5, "investigations that may wait behind the running one")
	timeout := fs.Duration("timeout", 10*time.Minute, "longest time one investigation may run")
	resultTTL := fs.Duration("result-ttl", time.Hour, "how long a finished report can be fetched")
	mf := registerModelFlags(fs)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(2)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	token := loadToken(*tokenFile)
	loopback := serve.IsLoopbackAddr(*listen)
	if !loopback && token == "" {
		log.Fatalf(`Refusing to listen on %s without a token. Anyone who can reach that port could make your cluster
read data and spend your model credits. Fix it in two lines:
  openssl rand -hex 24 > token && chmod 600 token
  log-analyzer serve --listen %s --token-file token
Or keep the default (--listen 127.0.0.1:8080), which only this computer can reach.`, *listen, *listen)
	}

	rs := mf.resolve()
	mode := *mf.llmMode
	if !set["llm"] && rs.APIKey == "" {
		mode = "heuristic" // nothing is needed to try it; the start message says so
	}
	if (mode == "live" || mode == "record") && rs.APIKey == "" {
		log.Fatalf("No API key found for provider %s. Required for --llm=%s. Use --api-key-file, or use --llm=heuristic.", *mf.provider, mode)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	registry := toolbox.NewToolRegistry()
	executor := toolbox.NewSandboxedExecutor(registry, 30*time.Second, *mf.dryRun)
	bus := protocol.NewAgentBus(100)
	model, closeModel := buildModel(ctx, mode, rs.Cfg, registry, *mf.recordDir)
	defer closeModel()

	policy := toolbox.NewPolicy(registry, toolbox.PolicyConfig{})
	fetcher := agent.NewFetcherAgent(bus, executor, policy)
	producer := agent.NewProducerAgent(bus, registry, make(chan string, 1), fetcher)
	producer.SetGitPersistence(false, "")
	analyzer := agent.NewAnalyzerAgent(bus, model)
	producer.SetCleanup(func(id string) {
		fetcher.Forget(id)
		analyzer.Forget(id)
		policy.ResetInvestigation(id)
	})
	go producer.Run(ctx)
	go fetcher.Run(ctx)
	go analyzer.Run(ctx)
	service := agent.NewService(bus, producer)

	var allowed []string
	for _, n := range strings.Split(*namespaces, ",") {
		if n = strings.TrimSpace(n); n != "" {
			allowed = append(allowed, n)
		}
	}
	modelLine := "rule-based (no AI model, nothing leaves this computer)"
	if mode != "heuristic" {
		modelLine = fmt.Sprintf("%s (%s). Logs are masked before they are sent", rs.Cfg.Model, rs.Cfg.Provider)
	}
	srv := serve.New(serve.Config{
		Token: token, Namespaces: allowed, QueueMax: *queue, Timeout: *timeout, ResultTTL: *resultTTL,
		LoopbackOnly: loopback, Mode: modelLine,
	}, service)

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("Cannot listen on %s: %v. Pick another port with --listen 127.0.0.1:9090", *listen, err)
	}
	printServeBanner(ln.Addr().String(), loopback, token != "", modelLine, mode, rs.APIKey == "" && !set["llm"], allowed)
	if err := srv.Serve(ctx, ln); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
	log.Println("[Serve] stopped")
}

// loadToken reads the token from --token-file, LOG_ANALYZER_TOKEN_FILE or LOG_ANALYZER_TOKEN.
// A token that is set but unusable stops the program.
func loadToken(file string) string {
	warn := func(m string) { log.Printf("[Creds] WARNING: %s", m) }
	tok, _, err := creds.Resolve(
		creds.FileProvider("--token-file", file, warn),
		creds.FileProvider("LOG_ANALYZER_TOKEN_FILE", os.Getenv("LOG_ANALYZER_TOKEN_FILE"), warn),
		creds.EnvProvider("LOG_ANALYZER_TOKEN", os.Getenv),
	)
	if err != nil {
		log.Fatalf("Cannot load the token: %v", err)
	}
	if tok != "" && len(tok) < 16 {
		log.Fatalf("The token is too short (%d characters). Use at least 16: openssl rand -hex 24 > token && chmod 600 token", len(tok))
	}
	return tok
}

func printServeBanner(addr string, loopback, hasToken bool, modelLine, mode string, noKey bool, allowed []string) {
	base := "http://" + addr
	if strings.HasPrefix(addr, "[::]") || strings.HasPrefix(addr, "0.0.0.0") {
		base = "http://localhost" + addr[strings.LastIndex(addr, ":"):]
	}
	auth, tokenFlag := "", ""
	hostport := strings.TrimPrefix(base, "http://")
	access := "this computer only, no token needed"
	if hasToken {
		auth, tokenFlag = ` -H "Authorization: Bearer $TOKEN"`, " --token-file TOKENFILE"
		access = "token required"
		if loopback {
			access = "this computer only, token required"
		}
	}
	fmt.Fprintf(os.Stderr, "\nlog-analyzer serve is ready: %s\n  Model:  %s\n  Access: %s\n", base, modelLine, access)
	if len(allowed) > 0 {
		fmt.Fprintf(os.Stderr, "  Only these namespaces: %s\n", strings.Join(allowed, ", "))
	}
	if noKey && mode == "heuristic" {
		fmt.Fprintln(os.Stderr, "  No API key was found, so the built-in rules are used. Add --api-key-file FILE to use an AI model.")
	}
	fmt.Fprintf(os.Stderr, "\nTry it from another terminal:\n  log-analyzer --server %s%s --namespace shop\n\nFrom a script:\n  curl -X POST \"%s/v1/investigations?wait=true\"%s -d '{\"namespace\":\"shop\"}'\n\nCtrl+C stops it.\n\n", hostport, tokenFlag, base, auth)
}
