package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jrivets/log4g"
	"github.com/kplr-io/geyser"
	"github.com/kplr-io/kplr/ingestor"
	"gopkg.in/alecthomas/kingpin.v2"
)

type (
	args struct {
		config      string
		debug       bool
		printStatus bool
	}
)

const (
	Version            = "0.0.1"
	cDefaultConfigPath = "/opt/kplr/agent/config.json"
)

var (
	logger = log4g.GetLogger("kplr.agent")
)

func main() {
	defer log4g.Shutdown()
	args := parseCLP()
	if args.debug {
		log4g.SetLogLevel("", log4g.TRACE)
	}

	cfg := &ingestor.AgentConfig{}
	err := cfg.LoadFromFile(args.config)
	if err != nil {
		logger.Warn("Unable to load config file=", args.config, "; cause: ", err, " will use default one")
		cfg = ingestor.NewDefaultAgentConfig()
	}

	if args.printStatus {
		su := ingestor.NewStatusFileUpdater(cfg, nil, nil)
		su.PrintStatusFile()
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		select {
		case s := <-sigChan:
			logger.Warn("Handling signal=", s)
			cancel()
		}
	}()

	var (
		gsr *geyser.Collector
		ing *ingestor.Ingestor
	)

	gsr, err = newCollector(cfg)
	if err != nil {
		logger.Fatal("Unable to create collector; cause: ", err)
		return
	}
	defer gsr.Stop()

	ing, err = ingestor.NewIngestor(cfg.Ingestor, ctx)
	if err != nil {
		logger.Fatal("Unable to create ingestor; cause: ", err)
		return
	}

	sfu := ingestor.NewStatusFileUpdater(cfg, gsr, ing)
	sfu.Run(ctx)

	done := ing.Run(ctx, gsr.Events())
	<-done
}

func parseCLP() *args {
	var (
		config = kingpin.Flag("config-file", "The kplr-agent configuration file name").Default(cDefaultConfigPath).String()
		debug  = kingpin.Flag("debug", "Enable debug log level").Bool()
		status = kingpin.Flag("print-status", "Prints status of the agent, if it is already run").Bool()
	)
	kingpin.Version(Version)
	kingpin.Parse()

	res := new(args)
	res.config = *config
	res.debug = *debug
	res.printStatus = *status
	return res
}

func newCollector(cfg *ingestor.AgentConfig) (*geyser.Collector, error) {
	gsr, err := geyser.NewCollector(cfg.Collector, geyser.NewFileStatusStorage(cfg.GeyserStateFile))
	if err == nil {
		err = gsr.Start()
	}
	return gsr, err
}
