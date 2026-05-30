package config

import (
	"encoding/json"
	"os"
	"sync"
)

type RemoteSync struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
}

type Config struct {
	Mu            sync.RWMutex `json:"-"`
	Addr          string       `json:"addr"`
	SiteName      string       `json:"siteName"`
	DataDir       string       `json:"dataDir"`
	SourceURL     string       `json:"sourceURL"`
	SourceType    string       `json:"sourceType"`
	AdminToken    string       `json:"adminToken"`
	WebhookSecret string       `json:"webhookSecret"`
	StartupSync   []RemoteSync `json:"startupSync"`
	NodeName      string       `json:"nodeName"`
}

func Default() Config {
	return Config{
		Addr:     ":8080",
		SiteName: "RED Engine",
		DataDir:  "./data",
	}
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}
