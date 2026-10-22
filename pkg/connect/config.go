// Package connect is the publisher's hosted "connect service" for Pulse. It runs Meta's
// Embedded Signup ("Connect with Facebook") so a Pulse user can link their own WhatsApp
// Business account without copying IDs and tokens, hands the resulting business token to
// that user's Pulse exactly once, and relays the incoming webhooks Meta signs with the
// publisher's app secret to the right Pulse install.
//
// The service never keeps a user's access token after the handoff, and holds incoming
// messages only until the user's Pulse fetches them (at most 7 days), encrypted.
package connect

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Config is read from the environment by ConfigFromEnv.
type Config struct {
	PublicURL    string // CONNECT_PUBLIC_URL, https address of this service
	AppID        string // META_APP_ID
	AppSecret    string // META_APP_SECRET
	ConfigID     string // META_CONFIG_ID, the Embedded Signup configuration
	GraphVersion string // META_GRAPH_VERSION, default v23.0
	VerifyToken  string // META_WEBHOOK_VERIFY_TOKEN
	DataDir      string // CONNECT_DATA_DIR
	DataKey      []byte // CONNECT_DATA_KEY, 32 bytes, base64
	Listen       string // CONNECT_LISTEN, default 127.0.0.1:8090
	// TrustedProxies are CIDRs (CONNECT_TRUSTED_PROXIES, comma separated) whose
	// X-Forwarded-For is used for rate limits, normally the local HTTPS proxy.
	TrustedProxies []string
	// GraphBaseURL and SDKURL replace Meta's addresses in tests.
	GraphBaseURL string
	SDKURL       string
}

// DefaultGraphVersion is used when META_GRAPH_VERSION is not set.
const DefaultGraphVersion = "v23.0"

var (
	digitsRe  = regexp.MustCompile(`^\d{5,30}$`)
	versionRe = regexp.MustCompile(`^v\d{1,2}\.\d$`)
)

// ConfigFromEnv reads and checks the configuration. getenv is os.Getenv in production.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		PublicURL: strings.TrimRight(strings.TrimSpace(getenv("CONNECT_PUBLIC_URL")), "/"),
		AppID:     strings.TrimSpace(getenv("META_APP_ID")), AppSecret: strings.TrimSpace(getenv("META_APP_SECRET")),
		ConfigID: strings.TrimSpace(getenv("META_CONFIG_ID")), GraphVersion: strings.TrimSpace(getenv("META_GRAPH_VERSION")),
		VerifyToken: strings.TrimSpace(getenv("META_WEBHOOK_VERIFY_TOKEN")), DataDir: strings.TrimSpace(getenv("CONNECT_DATA_DIR")),
		Listen: strings.TrimSpace(getenv("CONNECT_LISTEN")),
	}
	for _, p := range strings.Split(getenv("CONNECT_TRUSTED_PROXIES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	}
	if c.GraphVersion == "" {
		c.GraphVersion = DefaultGraphVersion
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8090"
	}
	var missing []string
	for name, v := range map[string]string{"CONNECT_PUBLIC_URL": c.PublicURL, "META_APP_ID": c.AppID, "META_APP_SECRET": c.AppSecret,
		"META_CONFIG_ID": c.ConfigID, "META_WEBHOOK_VERIFY_TOKEN": c.VerifyToken, "CONNECT_DATA_DIR": c.DataDir, "CONNECT_DATA_KEY": getenv("CONNECT_DATA_KEY")} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing required settings: %s", strings.Join(sortStrings(missing), ", "))
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(getenv("CONNECT_DATA_KEY")))
	if err != nil || len(key) != 32 {
		return c, errors.New("CONNECT_DATA_KEY must be 32 random bytes, base64 encoded (for example: openssl rand -base64 32)")
	}
	c.DataKey = key
	return c, c.Check()
}

// Check validates the configuration.
func (c Config) Check() error {
	u, err := url.Parse(c.PublicURL)
	switch {
	case err != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/"):
		return errors.New("CONNECT_PUBLIC_URL must be the service's https address without a path")
	case !digitsRe.MatchString(c.AppID) || !digitsRe.MatchString(c.ConfigID):
		return errors.New("META_APP_ID and META_CONFIG_ID are numbers")
	case !versionRe.MatchString(c.GraphVersion):
		return errors.New("META_GRAPH_VERSION looks like v23.0")
	case len(c.VerifyToken) < 16:
		return errors.New("META_WEBHOOK_VERIFY_TOKEN must be at least 16 characters")
	case len(c.DataKey) != 32:
		return errors.New("CONNECT_DATA_KEY must be 32 bytes")
	}
	return nil
}

func sortStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}
