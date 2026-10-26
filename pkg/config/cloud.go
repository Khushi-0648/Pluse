package config

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
)

// CloudCredentials are the WhatsApp Business API details of this install. They can come
// from the Settings form (typed by the owner) or, later, from another trusted source
// such as a sign-up flow; both store them through SetCloudCredentials.
type CloudCredentials struct {
	PhoneNumberID     string
	BusinessAccountID string
	// AccessToken and AppSecret are plain text here and sealed before storing. Empty
	// keeps the saved value.
	AccessToken string
	AppSecret   string
	APIVersion  string // empty uses DefaultGraphVersion
	PublicURL   string // https address of the tunnel; empty when not set up yet
	// RegenerateVerifyToken replaces the webhook verify token (one is always created
	// when none exists).
	RegenerateVerifyToken bool
}

func isDigits(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ValidPhoneNumberID reports whether id looks like a Meta phone number ID (digits).
func ValidPhoneNumberID(id string) bool { return isDigits(id, 5, 30) }

// ValidatePublicURL accepts an https address of a tunnel or reverse proxy, without a path.
func ValidatePublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("The public address must be an https address such as https://pulse.yourdomain.com (your fixed tunnel address), without anything after the name.")
	}
	// Meta must be able to reach it from the internet: a name on this computer or a
	// private network address can never work (S42).
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return errors.New("The public address must be reachable from the internet (your tunnel address), not a name on this computer.")
	}
	if ip := net.ParseIP(host); ip != nil {
		return errors.New("Enter the tunnel's name (such as https://pulse.yourdomain.com), not an IP address.")
	}
	return nil
}

// Clean trims the details and checks them, with messages a shop owner understands.
func (in *CloudCredentials) Clean() error {
	in.PhoneNumberID, in.BusinessAccountID = strings.TrimSpace(in.PhoneNumberID), strings.TrimSpace(in.BusinessAccountID)
	in.AccessToken, in.AppSecret = strings.TrimSpace(in.AccessToken), strings.TrimSpace(in.AppSecret)
	in.APIVersion, in.PublicURL = strings.TrimSpace(in.APIVersion), strings.TrimRight(strings.TrimSpace(in.PublicURL), "/")
	switch {
	case !isDigits(in.PhoneNumberID, 5, 30):
		return errors.New("The phone number ID is the long number shown in Meta's WhatsApp > API Setup page (digits only). It is not your phone number.")
	case in.BusinessAccountID != "" && !isDigits(in.BusinessAccountID, 5, 30):
		return errors.New("The WhatsApp Business account ID has digits only.")
	case in.APIVersion != "" && !ValidGraphVersion(in.APIVersion):
		return errors.New("The API version looks like v23.0. Leave it empty to use the default.")
	case len(in.AccessToken) > 1024 || strings.ContainsAny(in.AccessToken, " \t\r\n"):
		return errors.New("The access token does not look right. Copy it again from Meta, without spaces.")
	case len(in.AppSecret) > 256 || strings.ContainsAny(in.AppSecret, " \t\r\n"):
		return errors.New("The app secret does not look right. Copy it again from Meta's App settings > Basic.")
	}
	return ValidatePublicURL(in.PublicURL)
}

// SetCloudCredentials checks, seals and saves the WhatsApp Business API details. This
// is the one place they are stored, whatever their source.
func (c *Config) SetCloudCredentials(in CloudCredentials) error {
	if err := in.Clean(); err != nil {
		return err
	}
	tok, err := securestore.SealString(in.AccessToken)
	if err == nil {
		var sec string
		if sec, err = securestore.SealString(in.AppSecret); err == nil {
			in.AppSecret = sec
		}
	}
	if err != nil {
		return errors.New("The token could not be protected on this computer: " + err.Error())
	}
	return c.Update(func(x *Config) error {
		ca := &x.CloudAPI
		if !strings.EqualFold(strings.TrimRight(ca.PublicURL, "/"), in.PublicURL) {
			// Phone access was approved for the old address: a new one must be approved
			// again (with the admin password) before the dashboard is served there.
			x.Server.RemoteAccess = false
		}
		ca.PhoneNumberID, ca.BusinessAccountID, ca.PublicURL, ca.APIVersion = in.PhoneNumberID, in.BusinessAccountID, in.PublicURL, in.APIVersion
		if tok != "" {
			ca.AccessToken = tok
		}
		if in.AppSecret != "" {
			ca.AppSecret = in.AppSecret
		}
		if ca.AccessToken == "" {
			return errors.New("Paste the access token from Meta.")
		}
		if ca.VerifyToken == "" || in.RegenerateVerifyToken {
			ca.VerifyToken = "pulse-" + security.RandomToken(18)
		}
		return nil
	})
}

// ClearCloudCredentials removes the number, token and app secret, and switches
// automatic replies off. The verify token and public address are kept for reconnecting.
func (c *Config) ClearCloudCredentials() error {
	if err := c.Update(func(x *Config) error {
		x.CloudAPI.AccessToken, x.CloudAPI.AppSecret, x.CloudAPI.PhoneNumberID, x.CloudAPI.BusinessAccountID = "", "", "", ""
		x.Automation.Enabled = false
		x.Server.RemoteAccess = false // nothing should be reachable from the internet after a disconnect
		return nil
	}); err != nil {
		return err
	}
	// Each save copies the previous file to config.yaml.bak, which still holds the sealed
	// token and secret. Save once more so the backup copy is the cleared version too (S37).
	return c.Save()
}
