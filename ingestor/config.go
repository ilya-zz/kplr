package ingestor

import (
	"encoding/json"
	"io/ioutil"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/kplr-io/geyser"
	"github.com/mohae/deepcopy"
)

type (
	// IngestorConfig struct bears the ingestor configuration information. The
	// ingestor will use it for sending data to the aggragator. The structure
	// is used for configuring the ingestor
	IngestorConfig struct {
		Server           string          `json:"server"`
		RetrySec         int             `json:"retrySec"`
		HeartBeatMs      int             `json:"heartBeatMs"`
		PacketMaxRecords int             `json:"packetMaxRecords"`
		AccessKey        string          `json:"accessKey"`
		SecretKey        string          `json:"secretKey"`
		Schemas          []*SchemaConfig `json:"schemas"`
	}

	// SchemaConfig struct contains information by matching a file (PathMatcher)
	// to the journal id (SourceId) it also contains tags that will be used
	// for the match.
	SchemaConfig struct {
		PathMatcher string            `json:"pathMatcher"`
		SourceId    string            `json:"sourceId"`
		Tags        map[string]string `json:"tags"`
	}

	// AgentConfig struct just aggregate different types of configs in one place
	AgentConfig struct {
		Ingestor   *IngestorConfig `json:"ingestor"`
		Collector  *geyser.Config  `json:"collector"`
		StatusFile string          `json:"statusFile"`

		// GeyserStateFile contains a path to file where geyser will keep its
		// scan status.
		GeyserStateFile string `json:"stateFile"`
	}
)

const (
	cDefaultConfigStatus    = "/tmp/kplr/agent/status"
	cDefaultGeyserStateFile = "/opt/kplr/agent/collector.state"
)

func NewDefaultIngestorConfig() *IngestorConfig {
	ic := new(IngestorConfig)
	ic.Server = "127.0.0.1:9966"
	ic.RetrySec = 5
	ic.PacketMaxRecords = 1000
	ic.HeartBeatMs = 15000
	ic.AccessKey = ""
	ic.SecretKey = ""
	ic.Schemas = NewDefaultSchemaConfigs()
	return ic
}

func NewDefaultSchemaConfigs() []*SchemaConfig {
	return []*SchemaConfig{
		&SchemaConfig{
			PathMatcher: "/*(?:.+/)*(?P<file>.+\\..+)",
			SourceId:    "{file}",
			Tags:        map[string]string{"file": "{file}"},
		},
	}
}

// Apply sets non-empty fields value from ic1 to ic
func (ic *IngestorConfig) Apply(ic1 *IngestorConfig) {
	if ic1 == nil {
		return
	}

	if ic1.Server != "" {
		ic.Server = ic1.Server
	}

	if ic1.RetrySec != 0 {
		ic.RetrySec = ic1.RetrySec
	}

	if ic1.PacketMaxRecords != 0 {
		ic.PacketMaxRecords = ic1.PacketMaxRecords
	}

	if ic1.HeartBeatMs != 0 {
		ic.HeartBeatMs = ic1.HeartBeatMs
	}

	if ic1.AccessKey != "" {
		ic.AccessKey = ic1.AccessKey
	}

	if ic1.SecretKey != "" {
		ic.SecretKey = ic1.SecretKey
	}

	if len(ic1.Schemas) != 0 {
		ic.Schemas = deepcopy.Copy(ic1.Schemas).([]*SchemaConfig)
	}
}

func NewDefaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		Ingestor:        NewDefaultIngestorConfig(),
		Collector:       geyser.NewDefaultConfig(),
		StatusFile:      cDefaultConfigStatus,
		GeyserStateFile: cDefaultGeyserStateFile,
	}
}

// LoadFromFile loads the config from file name provided in path
func (ac *AgentConfig) LoadFromFile(path string) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	toLower := strings.ToLower(path)
	if strings.HasSuffix(toLower, "yaml") {
		return yaml.Unmarshal(data, ac)
	}
	return json.Unmarshal(data, ac)
}

// Apply sets non-empty fields value from ac1 to ac
func (ac *AgentConfig) Apply(ac1 *AgentConfig) {
	if ac1 == nil {
		return
	}

	ac.Ingestor.Apply(ac1.Ingestor)
	ac.Collector.Apply(ac1.Collector)
	if ac1.StatusFile != "" {
		ac.StatusFile = ac1.StatusFile
	}
	if ac1.GeyserStateFile != "" {
		ac.GeyserStateFile = ac1.GeyserStateFile
	}
}
