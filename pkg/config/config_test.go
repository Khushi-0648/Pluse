package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsAreSafe(t *testing.T) {
	c := DefaultConfig()
	if c.Automation.Enabled {
		t.Error("automation must start off until setup is finished")
	}
	if !c.Automation.RequireOptIn || !c.Automation.Enforce24hWindow {
		t.Error("privacy and opt-in defaults are wrong")
	}
	if c.Automation.Pace != "careful" || !c.Automation.WarmupMode {
		t.Error("new installs should use the careful pace with warm-up")
	}
	if c.Server.Host != "127.0.0.1" {
		t.Error("server must bind to loopback by default")
	}
	for _, r := range c.Rules {
		for _, bad := range []string{"SAVE20", "$29", "9:00 AM", "AI", "*"} {
			if strings.Contains(r.Reply, bad) {
				t.Errorf("default rule %s contains invented or formatted text %q", r.Name, bad)
			}
		}
	}
	if len(c.SetupMissing()) == 0 {
		t.Error("a fresh install should have setup steps missing")
	}
}

func TestLegacyConfigIsNormalized(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	legacy := `server:
  host: ""
  port: 8085
  admin_password: ""
automation:
  enabled: true
  daily_limit: 500
  cooldown_seconds: 5
  typing_duration_ms: 1500
business_hours:
  enabled: true
  start_hour: 10
  end_hour: 19
  timezone: Asia/Kolkata
ai:
  enabled: true
  api_key: secret
rules:
  - name: hello
    pattern: hi
    reply: Hello
    enabled: true
`
	if err := os.WriteFile(p, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot()
	if s.Server.Host != "127.0.0.1" || s.Automation.DailyLimit != 500 || s.Automation.Pace != "custom" {
		t.Errorf("legacy values not kept: %+v", s.Automation)
	}
	if len(s.BusinessHours.Days) != 7 || s.BusinessHours.Days[0].Start != "10:00" || s.BusinessHours.Days[0].End != "19:00" {
		t.Errorf("legacy hours not migrated: %+v", s.BusinessHours.Days)
	}
	if len(s.Rules) != 1 || s.Rules[0].Type != "exact" {
		t.Errorf("rules not loaded: %+v", s.Rules)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	if strings.Contains(string(out), "api_key: secret") || strings.Contains(string(out), "start_hour") {
		t.Error("removed AI settings or legacy fields were written back")
	}
}

func TestSaveWithoutPathWritesNothing(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer os.Chdir(wd)
	c := DefaultConfig()
	if err := c.SetPassword("river7Lamp"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err == nil {
		t.Fatal("an in-memory config must not create config.yaml")
	}
}

func TestPasswordsAndRoles(t *testing.T) {
	c := DefaultConfig()
	gen, isNew := c.EnsureAdminPassword()
	if !isNew || !c.MustChange() {
		t.Fatal("first run should generate a password that must be changed")
	}
	if r, ok := c.Authenticate(gen); !ok || r != RoleAdmin {
		t.Fatal("generated password should sign in as admin")
	}
	if err := c.SetPassword("Admin1234"); err == nil {
		t.Error("server must reject passwords containing admin (S20)")
	}
	if err := c.SetPassword("river7Lamp"); err != nil || c.MustChange() {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := c.SetOperatorPassword("river7Lamp"); err == nil {
		t.Error("staff password must differ from the admin password")
	}
	if err := c.SetOperatorPassword("harbor42Blue"); err != nil {
		t.Fatal(err)
	}
	if r, ok := c.Authenticate("harbor42Blue"); !ok || r != RoleOperator {
		t.Error("staff password should sign in as operator")
	}
	if _, ok := c.Authenticate("wrong"); ok {
		t.Error("wrong password accepted")
	}
}

func TestRuleEditing(t *testing.T) {
	c := DefaultConfig()
	if err := c.SetRuleEnabled("greeting_menu", false); err != nil {
		t.Fatal(err)
	}
	for _, r := range c.GetRules() {
		if r.Name == "greeting_menu" && r.Enabled {
			t.Fatal("disabling a rule must stick (D25)")
		}
	}
	r := Rule{Name: "Delivery", Pattern: "delivery, shipping", Reply: "We deliver in 2 days.", Enabled: false}
	if err := c.AddOrUpdateRule(r); err != nil {
		t.Fatal(err)
	}
	if err := c.AddOrUpdateRule(Rule{Name: "bad<script>", Pattern: "x", Reply: "y"}); err == nil {
		t.Error("unsafe rule names must be rejected")
	}
	if err := c.AddOrUpdateRule(Rule{Name: "re", Type: "regex", Pattern: "([", Reply: "y"}); err == nil {
		t.Error("invalid regex must be rejected")
	}
	if err := c.AddOrUpdateRule(Rule{Name: "Shipping", Pattern: "ship", Reply: "z"}, "Delivery"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReorderRules([]string{"Shipping", "greeting_menu"}); err != nil {
		t.Fatal(err)
	}
	rules := c.GetRules()
	if rules[0].Name != "Shipping" || rules[0].Enabled {
		t.Errorf("rename/reorder/enabled flag wrong: %+v", rules[0])
	}
	if !c.DeleteRule("shipping") || c.DeleteRule("shipping") {
		t.Error("delete should be case-insensitive and report missing rules")
	}
}

func TestSnapshotIsIndependent(t *testing.T) {
	c := DefaultConfig()
	s := c.Snapshot()
	s.Rules[0].Reply = "changed"
	s.Automation.AllowList = append(s.Automation.AllowList, "1")
	if c.Snapshot().Rules[0].Reply == "changed" || len(c.Snapshot().Automation.AllowList) != 0 {
		t.Fatal("snapshots must not share memory with the live config")
	}
	safe := c.SafeClone()
	if safe.Server.AdminPassword != "********" {
		t.Error("admin password must be masked")
	}
}

func TestPlaintextSecretsAreHashedOnLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	old := "server:\n  admin_password: harbor42Blue\n  operator_password: staff42Desk\n  api_key: pk_legacykey123\n"
	if err := os.WriteFile(p, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p, p + ".bak"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s not written: %v", f, err)
		}
		for _, secret := range []string{"harbor42Blue", "staff42Desk", "pk_legacykey123"} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("%s still holds %q as readable text", filepath.Base(f), secret)
			}
		}
	}
	s := c.Snapshot()
	if !isHash(s.Server.AdminPassword) || !isHash(s.Server.OperatorPassword) || !strings.HasPrefix(s.Server.APIKey, apiKeyHashPrefix) {
		t.Fatalf("secrets not hashed: %+v", s.Server)
	}
	if r, ok := c.Authenticate("harbor42Blue"); !ok || r != RoleAdmin {
		t.Fatal("the migrated admin password must still sign in")
	}
	if r, ok := c.Authenticate("staff42Desk"); !ok || r != RoleOperator {
		t.Fatal("the migrated staff password must still sign in")
	}
	if !c.CheckAPIKey("pk_legacykey123") || c.CheckAPIKey("pk_other") || c.CheckAPIKey("") {
		t.Fatal("API key check wrong after migration")
	}
}

func TestPlaintextPasswordNeverCompared(t *testing.T) {
	c := DefaultConfig()
	c.Server.AdminPassword = "harbor42Blue" // set directly, bypassing load and Update
	if _, ok := c.Authenticate("harbor42Blue"); ok || c.VerifyPassword("harbor42Blue") {
		t.Fatal("a readable stored password must never be accepted")
	}
	if err := c.Update(func(x *Config) error { x.Server.APIKey = "pk_abc"; return nil }); err != nil {
		t.Fatal(err)
	}
	if s := c.Snapshot(); s.Server.APIKey == "pk_abc" || !c.CheckAPIKey("pk_abc") {
		t.Fatal("an API key saved through Update must be stored hashed and still match")
	}
}

// A settings file from the linked-device versions loads unchanged apart from the
// obsolete keys, and a token typed into the file by hand is sealed at once.
func TestLinkedDeviceConfigLoadsAndCloudSecretsAreSealed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	old := `whatsapp:
  db_path: whatsapp.db
  session_name: default
  pair_phone: "+919800000000"
business:
  name: Asha Stores
  hours: 9 to 6
automation:
  import_chat_history: true
  history_choice_made: true
cloud_api:
  phone_number_id: " 1234567890 "
  access_token: EAAG-plain-token
  app_secret: plain-app-secret
  api_version: nonsense
`
	if err := os.WriteFile(p, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot()
	if s.Business.Name != "Asha Stores" || s.CloudAPI.PhoneNumberID != "1234567890" || s.CloudAPI.APIVersion != DefaultGraphVersion {
		t.Fatalf("settings not kept or normalised: %+v %+v", s.Business, s.CloudAPI)
	}
	raw, _ := os.ReadFile(p)
	bak, _ := os.ReadFile(p + ".bak")
	for _, data := range [][]byte{raw, bak} {
		if strings.Contains(string(data), "plain-token") || strings.Contains(string(data), "plain-app-secret") {
			t.Fatal("the token or app secret is still readable in the settings file or its backup")
		}
	}
	if tok, err := c.CloudToken(); err != nil || tok != "EAAG-plain-token" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if sec, err := c.CloudAppSecret(); err != nil || sec != "plain-app-secret" {
		t.Fatalf("app secret = %q, %v", sec, err)
	}
	if safe := c.SafeClone(); safe.CloudAPI.AccessToken != "" || safe.CloudAPI.AppSecret != "" {
		t.Fatal("secrets must never be in the settings sent to the browser")
	}
	if !s.CloudAPI.Ready() || s.CloudAPI.WebhookURL() != "" {
		t.Fatal("ready without a public address must have no webhook address")
	}
	s.CloudAPI.PublicURL = "https://shop.example.com/"
	if s.CloudAPI.WebhookURL() != "https://shop.example.com/cloud/webhook" {
		t.Fatal(s.CloudAPI.WebhookURL())
	}
	for _, m := range MissingSetup(*DefaultConfig()) {
		if strings.Contains(m, "WhatsApp connection") {
			return
		}
	}
	t.Fatal("a fresh install must ask for the WhatsApp Business API connection")
}

// SetCloudCredentials is the one entry point for storing the Business API details.
func TestSetAndClearCloudCredentials(t *testing.T) {
	c := DefaultConfig()
	if err := c.SetCloudCredentials(CloudCredentials{PhoneNumberID: "12a45", AccessToken: "t"}); err == nil {
		t.Fatal("a phone number ID with letters must be refused")
	}
	if err := c.SetCloudCredentials(CloudCredentials{PhoneNumberID: "123456"}); err == nil {
		t.Fatal("a first save needs the access token")
	}
	if err := c.SetCloudCredentials(CloudCredentials{PhoneNumberID: " 123456 ", AccessToken: "EAAG1", AppSecret: "sec", PublicURL: "https://a.example.com/"}); err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot().CloudAPI
	if tok, _ := c.CloudToken(); tok != "EAAG1" || s.PhoneNumberID != "123456" || s.VerifyToken == "" || s.PublicURL != "https://a.example.com" {
		t.Fatalf("saved %+v", s)
	}
	v := s.VerifyToken
	if err := c.SetCloudCredentials(CloudCredentials{PhoneNumberID: "123456"}); err != nil || c.Snapshot().CloudAPI.VerifyToken != v {
		t.Fatal("empty secrets keep the saved ones and the verify token")
	}
	if tok, _ := c.CloudToken(); tok != "EAAG1" {
		t.Fatal("token lost")
	}
	if err := c.ClearCloudCredentials(); err != nil || c.Snapshot().CloudAPI.Ready() || c.Snapshot().CloudAPI.AppSecret != "" {
		t.Fatal("clear failed")
	}
}
