// ingestor package contains a code which helps to build a data ingestor for the
// aggregator
package ingestor

import (
	"context"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"time"

	"github.com/jrivets/log4g"
	"github.com/kplr-io/container"
	"github.com/kplr-io/geyser"
	"github.com/kplr-io/kplr/model"
	"github.com/kplr-io/zebra"
	"github.com/mohae/deepcopy"
	"github.com/pkg/errors"
)

type (
	// schema struct contains a schema descriptor, which includes the SchemaConfig
	// and corresponding reg-exp matcher, which is going to be used for matching
	// the schema. This structure is used internally for identifying the schema.
	schema struct {
		cfg     *SchemaConfig
		matcher *regexp.Regexp
	}

	// Ingestor struct is used for sending data received from geyser to an log
	// aggregator.
	Ingestor struct {
		cfg        *IngestorConfig
		zClient    zebra.Writer
		pktEncoder *encoder
		schemas    []*schema
		logger     log4g.Logger
		ctx        context.Context

		lock sync.Mutex
		// hdrsCache allows to cache headers by file name
		hdrsCache *container.Lru
	}

	// hdrsCacheRec struct is used by Ingestor to cache association between file
	// name and the message data header.
	hdrsCacheRec struct {
		srcId string
		tags  model.TagLine
	}
)

func (hcr *hdrsCacheRec) String() string {
	return fmt.Sprint("{srcId=", hcr.srcId, ", tags=", hcr.tags, "}")
}

func NewIngestor(cfg *IngestorConfig, ctx context.Context) (*Ingestor, error) {
	if err := checkConfig(cfg); err != nil {
		return nil, err
	}

	logger := log4g.GetLogger("kplr.ingestor")
	logger.Info("Creating, config=", geyser.ToJsonStr(cfg))

	ing := new(Ingestor)
	ing.hdrsCache = container.NewLru(10000, 5*time.Minute, nil)
	ing.cfg = deepcopy.Copy(cfg).(*IngestorConfig)
	ing.pktEncoder = newEncoder()
	ing.logger = logger
	ing.ctx = ctx

	ing.schemas = make([]*schema, 0, len(cfg.Schemas))
	for _, s := range cfg.Schemas {
		ing.schemas = append(ing.schemas, newSchema(s))
	}

	logger.Info("Created!")
	return ing, nil
}

func (i *Ingestor) Run(ctx context.Context, events <-chan *geyser.Event) chan bool {
	i.connect()
	done := make(chan bool)
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case ev := <-events:
				var err error
				for ctx.Err() == nil {
					if err == nil {
						err = i.ingest(ev)
						if err == nil {
							ev.Confirm()
							break
						}
					}
					i.logger.Info("Ingestor error, recovering; cause: ", err)
					err = i.connect()
				}
			}
		}
	}()
	return done
}

func (i *Ingestor) connect() error {
	i.logger.Info("Connecting to ", i.cfg.Server)
	var (
		zcl zebra.Writer
		err error
	)

	retry := time.Duration(i.cfg.RetrySec) * time.Second
	for {
		zcl, err = zebra.NewClient(i.cfg.Server,
			&zebra.ClientConfig{HeartBeatMs: i.cfg.HeartBeatMs, AccessKey: i.cfg.AccessKey, SecretKey: i.cfg.SecretKey})
		if err == nil {
			break
		}
		i.logger.Warn("Could not connect to the server, err=", err, " will try in ", retry)

		select {
		case <-i.ctx.Done():
			return fmt.Errorf("Interrupted")
		case <-time.After(retry):
		}
		i.logger.Warn("after 5 sec")
	}

	i.zClient = zcl
	i.logger.Info("connected")
	return nil
}

func (i *Ingestor) ingest(ev *geyser.Event) error {
	if i.zClient == nil {
		return fmt.Errorf("Not initialized")
	}

	header, err := i.getHeaderByFilename(ev.File)
	if err != nil {
		i.zClient = nil
		return err
	}

	i.logger.Trace("Ingest header=", header, ", ev.File=", ev.File, ", len(ev.Records)=", len(ev.Records))

	buf, err := i.pktEncoder.encode(header, ev)
	if err != nil {
		i.zClient = nil
		return err
	}
	_, err = i.zClient.Write(buf, nil)
	if err != nil {
		i.zClient = nil
		return err
	}
	return nil
}

// getHeaderByFilename get filename and forms header using schema and configuration
// it can cache already calculated headers, so will work quickly this case
func (i *Ingestor) getHeaderByFilename(filename string) (*hdrsCacheRec, error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	val := i.hdrsCache.Get(filename)
	if val != nil {
		hdr := val.Val().(*hdrsCacheRec)
		return hdr, nil
	}

	schm := i.getSchema(filename)
	if schm == nil {
		return nil, errors.New("no schema found!")
	}

	vars := schm.getVars(filename)
	tags := make(map[string]string, len(schm.cfg.Tags))

	for k, v := range schm.cfg.Tags {
		tags[k] = schm.subsVars(v, vars)
	}

	srcId := schm.subsVars(schm.cfg.SourceId, vars)
	tm, err := model.NewTagMap(tags)
	if err != nil {
		return nil, err
	}
	hdr := &hdrsCacheRec{srcId, tm.BuildTagLine()}

	i.hdrsCache.Put(filename, hdr, 1)
	return hdr, nil
}

func (i *Ingestor) getKnownTags() map[interface{}]interface{} {
	i.lock.Lock()
	res := i.hdrsCache.GetData()
	i.lock.Unlock()
	return res
}

func (i *Ingestor) getSchema(filename string) *schema {
	for _, s := range i.schemas {
		if s.matcher.MatchString(filename) {
			return s
		}
	}
	return nil
}

func (i *Ingestor) close() {
	i.logger.Info("Closing...")
	if i.zClient != nil {
		i.zClient.Close()
	}
	i.logger.Info("Closed.")
}

//=== schemaConfig

func (s *SchemaConfig) String() string {
	return geyser.ToJsonStr(s)
}

//=== schema

func newSchema(cfg *SchemaConfig) *schema {
	return &schema{
		cfg:     cfg,
		matcher: regexp.MustCompile(cfg.PathMatcher),
	}
}

func (s *schema) getVars(l string) map[string]string {
	names := s.matcher.SubexpNames()
	match := s.matcher.FindStringSubmatch(l)

	if len(names) > 1 {
		names = names[1:] //skip ""
	}
	if len(match) > 1 {
		match = match[1:] //skip "" value
	}

	vars := make(map[string]string, len(names))
	for i, n := range names {
		if len(match) > i {
			vars[n] = match[i]
		} else {
			vars[n] = ""
		}
	}
	return vars
}

func (s *schema) subsVars(l string, vars map[string]string) string {
	for k, v := range vars {
		l = strings.Replace(l, "{"+k+"}", v, -1)
	}
	return l
}

func (s *schema) String() string {
	return geyser.ToJsonStr(s.cfg)
}

//=== helpers

func checkConfig(cfg *IngestorConfig) error {
	if cfg == nil {
		return fmt.Errorf("invalid config=%v", cfg)
	}

	if cfg.RetrySec < 1 {
		return fmt.Errorf("invalid config; retry Zebra onnect timeout=%s, expecting 1 second or more", cfg.RetrySec)
	}

	if strings.TrimSpace(cfg.Server) == "" {
		return fmt.Errorf("invalid config; server=%v, must be non-empty", cfg.Server)
	}

	if cfg.HeartBeatMs < 100 {
		return fmt.Errorf("invalid config; heartBeatMs=%v, must be >= 100ms", cfg.HeartBeatMs)
	}

	if cfg.PacketMaxRecords <= 0 {
		return fmt.Errorf("invalid config; packetMaxRecords=%v, must be > 0", cfg.PacketMaxRecords)
	}

	if len(cfg.Schemas) == 0 {
		return errors.New("invalid config; at least 1 schema must be defined")
	}

	for _, s := range cfg.Schemas {
		if err := checkSchema(s); err != nil {
			return fmt.Errorf("invalid config; invalid schema=%v, %v", s, err)
		}
	}
	return nil
}

func checkSchema(s *SchemaConfig) error {
	if strings.TrimSpace(s.PathMatcher) == "" {
		return errors.New("patchMatcher must be non-empty")
	}
	_, err := syntax.Parse(s.PathMatcher, syntax.Perl)
	if err != nil {
		return fmt.Errorf("pathMatcher is invalid; %v", err)
	}
	if strings.TrimSpace(s.SourceId) == "" {
		return errors.New("sourceId must be non-empty")
	}
	return nil
}
